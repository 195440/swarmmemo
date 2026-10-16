package httpapi

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"swarmmemo/internal/board"
)

// TestOpenAPIDataSchemasFromSpec: /openapi.json has one data schema per
// operation that takes data, generated from its data spec, field for field,
// and OperationData lists exactly those operations (C377).
func TestOpenAPIDataSchemasFromSpec(t *testing.T) {
	s, _ := catalogServer(board.Features{})
	var spec struct {
		Components struct{ Schemas map[string]json.RawMessage }
	}
	if err := json.Unmarshal(makeRequest(s, "GET", "/openapi.json", "", "").Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	var table struct {
		Properties map[string]struct {
			Ref string `json:"$ref"`
		}
	}
	if err := json.Unmarshal(spec.Components.Schemas["OperationData"], &table); err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, op := range board.Operations() {
		data, ok := op.Data()
		if !ok || s.openAPIDataHidden(op.Name) {
			continue
		}
		want = append(want, op.Name)
		name := openAPIDataSchemaName(op.Name)
		if table.Properties[op.Name].Ref != "#/components/schemas/"+name {
			t.Errorf("OperationData %s: %v", op.Name, table.Properties[op.Name])
		}
		var schema struct {
			Type                 string
			Properties           map[string]json.RawMessage
			Required             []string
			AdditionalProperties *bool `json:"additionalProperties"`
		}
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Errorf("no components/schemas/%s", name)
			continue
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if data.Raw != "" {
			if schema.Type != "string" {
				t.Errorf("%s: raw data %s, schema type %q", name, data.Raw, schema.Type)
			}
			continue
		}
		var props []string
		for p := range schema.Properties {
			props = append(props, p)
		}
		var fields, required []string
		for _, f := range data.Fields {
			if f.RemovedIn == "" {
				fields = append(fields, f.Name)
			}
			if f.Required && f.RemovedIn == "" {
				required = append(required, f.Name)
			}
		}
		sort.Strings(props)
		sort.Strings(fields)
		sort.Strings(required)
		sort.Strings(schema.Required)
		if !slices.Equal(props, fields) || !slices.Equal(schema.Required, required) {
			t.Errorf("%s: properties %v required %v, spec %v required %v", name, props, schema.Required, fields, required)
		}
		if closed := schema.AdditionalProperties != nil && !*schema.AdditionalProperties; closed == data.Lax {
			t.Errorf("%s: additionalProperties %v, spec lax %v", name, schema.AdditionalProperties, data.Lax)
		}
	}
	var listed []string
	for op := range table.Properties {
		listed = append(listed, op)
	}
	sort.Strings(listed)
	sort.Strings(want)
	if !slices.Equal(listed, want) {
		t.Errorf("OperationData lists %v, the spec %v", listed, want)
	}

	// The ledger's data is there only while the ledger is on.
	if _, ok := spec.Components.Schemas[openAPIDataSchemaName("allowance.get")]; ok {
		t.Error("allowance.getData published with the ledger off")
	}
	on, _ := catalogServer(board.Features{Ledger: board.LedgerOn})
	if !strings.Contains(makeRequest(on, "GET", "/openapi.json", "", "").Body.String(), `"allowance.getData"`) {
		t.Error("allowance.getData missing with the ledger on")
	}

	// A schema validates what the board takes and refuses a name it does not.
	var create jsonschema.Schema
	if err := json.Unmarshal(spec.Components.Schemas[openAPIDataSchemaName("work.create")], &create); err != nil {
		t.Fatal(err)
	}
	resolved, err := create.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	good := map[string]any{"schema": 1, "generation": "0123456789abcdef0123456789abcdef", "title": "Translate", "capabilities": []any{"translation"}, "reward": 5}
	if err := resolved.Validate(good); err != nil {
		t.Errorf("work.create data: %v", err)
	}
	good["bounty"] = 5
	if resolved.Validate(good) == nil {
		t.Error("work.createData took a field the spec does not")
	}
}
