package clientcontract

import (
	"slices"
	"testing"

	"swarmmemo/internal/board"
)

// Every enum, pattern and limit a data field names is an entry of this
// contract, and every operation that takes data carries its spec.
func TestDataSpecReferences(t *testing.T) {
	c := Build()
	enums := c["enums"].(map[string][]string)
	patterns := c["patterns"].(map[string]string)
	limits := c["limits"].(map[string]int64)
	operations := c["operations"].(map[string]Operation)
	fields := 0
	for name, op := range operations {
		if slices.Contains(op.Fields, "data") != (op.Data != nil) {
			t.Errorf("%s: fields name data %t, data spec %t", name, slices.Contains(op.Fields, "data"), op.Data != nil)
		}
		if op.Data == nil {
			continue
		}
		all := map[string]DataField{}
		for field, f := range op.Data.Fields {
			all[field] = f
			if f.Items != nil {
				for item, g := range f.Items.Fields {
					all[field+"[]."+item] = g
				}
			}
		}
		for field, f := range all {
			fields++
			if _, ok := enums[f.Enum]; f.Enum != "" && !ok {
				t.Errorf("%s data.%s: enum %q is not in the contract", name, field, f.Enum)
			}
			if _, ok := patterns[f.Pattern]; f.Pattern != "" && !ok {
				t.Errorf("%s data.%s: pattern %q is not in the contract", name, field, f.Pattern)
			}
			if _, ok := limits[f.Limit]; f.Limit != "" && !ok {
				t.Errorf("%s data.%s: limit %q is not in the contract", name, field, f.Limit)
			}
		}
	}
	if fields == 0 {
		t.Fatal("no data fields in the contract")
	}
	// work_create_data stays, the spec's work.create fields.
	want := []string{}
	for field := range operations["work.create"].Data.Fields {
		want = append(want, field)
	}
	slices.Sort(want)
	if got := c["work_create_data"].([]string); !slices.Equal(got, want) {
		t.Errorf("work_create_data %v, spec %v", got, want)
	}
	if _, ok := board.DataSpecOf("ledger.list"); ok {
		t.Error("ledger.list has a data spec")
	}
}
