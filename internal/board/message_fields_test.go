package board

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var updateMessageFields = flag.Bool("update-message-fields", false, "rewrite clients/message-fields.json from the Message struct")

// messageFieldsGolden is the wire field set of a message object, derived from
// the struct tags. The first-party clients validate messages against strict
// allowlists; their tests (clients/python/test_contract.py and
// clients/mcp/test_operations.py, which also checks "work") check every
// allowlist covers this file, so
// a field added here fails CI, not the agents reading it.
const messageFieldsGolden = "../../clients/message-fields.json"

func jsonFields(value any) []string {
	var fields []string
	kind := reflect.TypeOf(value)
	for i := 0; i < kind.NumField(); i++ {
		name, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
		if kind.Field(i).IsExported() && name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

// workCreateDataFields is every field work.create data takes, sorted.
func workCreateDataFields() []string {
	fields := append([]string{"schema", "generation", "title", "capabilities"}, workCreateOptional...)
	for _, name := range fields {
		if !workDataTakes("work.create", name) {
			panic("work.create data does not take " + name)
		}
	}
	sort.Strings(fields)
	return fields
}

func TestMessageFieldsGolden(t *testing.T) {
	want := map[string][]string{"message": jsonFields(Message{}), "attachment": jsonFields(Attachment{}),
		"forwarded": jsonFields(Forwarded{}), "quality": jsonFields(Quality{}), "screen": jsonFields(MessageScreen{}), "votes": jsonFields(VoteCounts{}),
		// A work object (works.list, work.get), which clients/mcp validates
		// against a strict allowlist too.
		"work": jsonFields(Work{}),
		// A message's work mark (MessageWork), which clients/python
		// check_work validates against a strict allowlist.
		"message_work": jsonFields(MessageWork{}),
		// The fields work.create data takes, which clients/mcp checks each
		// signed create in work history against.
		"work_create_data": workCreateDataFields()}
	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if *updateMessageFields {
		if err = os.WriteFile(messageFieldsGolden, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(messageFieldsGolden)
	if err != nil || !bytes.Equal(got, encoded) {
		t.Fatalf("clients/message-fields.json is stale (%v); rerun go test ./internal/board -run TestMessageFieldsGolden -update-message-fields, then teach every client validator the new field\nwant:\n%s", err, encoded)
	}
}
