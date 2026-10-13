package httpapi

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/services"
)

// TestOpenAPICommandSchemaMatchesBoardCommand proves the published Command
// schema (additionalProperties: false) has exactly board.Command's json fields
// minus commandSchemaExcluded: what POST /v1/command decodes is what the
// schema admits (older was missing, Inner Ozma 2dd3bb2c).
func TestOpenAPICommandSchemaMatchesBoardCommand(t *testing.T) {
	tags := map[string]bool{}
	ct := reflect.TypeOf(board.Command{})
	for i := 0; i < ct.NumField(); i++ {
		f := ct.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("board.Command.%s has no json name", f.Name)
		}
		tags[name] = true
	}
	for name, reason := range commandSchemaExcluded {
		if !tags[name] || strings.TrimSpace(reason) == "" {
			t.Fatalf("exclusion %q must name a board.Command field and give a reason", name)
		}
	}
	for name := range commandSchemaOverrides() {
		if !tags[name] {
			t.Fatalf("override %q names no board.Command field", name)
		}
	}
	want := []string{}
	for name := range tags {
		if _, ok := commandSchemaExcluded[name]; !ok {
			want = append(want, name)
		}
	}
	sort.Strings(want)
	for _, f := range []board.Features{{}, {Services: services.Known()}} {
		s, _ := catalogServer(f)
		var spec struct {
			Components struct {
				Schemas map[string]struct {
					AdditionalProperties *bool                      `json:"additionalProperties"`
					Properties           map[string]json.RawMessage `json:"properties"`
				}
			}
		}
		if err := json.Unmarshal(makeRequest(s, "GET", "/openapi.json", "", "").Body.Bytes(), &spec); err != nil {
			t.Fatal(err)
		}
		cmd, ok := spec.Components.Schemas["Command"]
		if !ok || cmd.AdditionalProperties == nil || *cmd.AdditionalProperties {
			t.Fatal("openapi Command schema missing or not closed")
		}
		got := []string{}
		for name := range cmd.Properties {
			got = append(got, name)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("services=%v: openapi Command properties %v, board.Command fields %v", len(f.Services) > 0, got, want)
		}
	}
}
