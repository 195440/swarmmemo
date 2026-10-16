package httpapi

import (
	"fmt"
	"strings"
	"sync"

	"swarmmemo/internal/board"
	"swarmmemo/internal/clientcontract"
)

// Each operation's data in /openapi.json (C377, C7), generated from the
// board's data spec (board.DataSpecOf): components/schemas/<operation>Data
// is the JSON object the data string decodes to (or, for raw data, the
// string itself), and OperationData keys them all by operation. The
// Command schema's data stays a string; its description points here.

// commandDataDescription is the Command schema's data property.
const commandDataDescription = "Operation data as a JSON string. What it holds is components/schemas/<operation>Data (OperationData lists them by operation), generated from the board's data spec, the one the parsers enforce; clients/contract.json carries the same as operations[op].data."

// openAPIDataSchemaName is the component an operation's data schema is.
func openAPIDataSchemaName(operation string) string { return operation + "Data" }

// contractFacts are the contract's enums and limits, which data fields name.
var contractFacts = sync.OnceValues(func() (map[string][]string, map[string]int64) {
	c := clientcontract.Build()
	enums, _ := c["enums"].(map[string][]string)
	limits, _ := c["limits"].(map[string]int64)
	return enums, limits
})

// openAPIDataType is a data type's JSON Schema type ("int|object" becomes
// ["integer","object"]).
func openAPIDataType(t string) any {
	var out []string
	for _, part := range strings.Split(t, "|") {
		switch part {
		case board.DataInt:
			out = append(out, "integer")
		case board.DataBool:
			out = append(out, "boolean")
		default:
			out = append(out, part)
		}
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

// openAPIRawData describes data that is a string, not an object.
var openAPIRawData = map[string]map[string]any{
	board.DataRawBase64URL: {"type": "string", "pattern": "^[A-Za-z0-9_-]*$", "description": "Unpadded base64url bytes, not JSON."},
	board.DataRawViewer:    {"type": "string", "enum": []string{board.DataRawViewer}, "description": "The literal string viewer, not JSON."},
	board.DataRawService:   {"type": "string", "contentMediaType": "application/json", "description": "A service method's call as a JSON string: components/schemas/ServiceData, where the catalogue is on."},
}

// openAPIDataSchema is one operation's data spec as a JSON Schema.
func openAPIDataSchema(operation string, spec board.DataSpec) map[string]any {
	if spec.Raw != "" {
		raw, ok := openAPIRawData[spec.Raw]
		if !ok {
			panic("openapi: no schema for raw data " + spec.Raw)
		}
		out := map[string]any{"title": operation + " data"}
		for k, v := range raw {
			out[k] = v
		}
		return out
	}
	enums, limits := contractFacts()
	patterns := board.ClientPatterns()
	props := map[string]any{}
	required := []string{}
	var notes []string
	for _, f := range spec.Fields {
		switch {
		case f.RemovedIn != "":
			notes = append(notes, fmt.Sprintf("%s was removed in %s and is refused", f.Name, f.RemovedIn))
			continue
		case f.AliasOf != "":
			notes = append(notes, f.Name+" is another spelling of "+f.AliasOf)
		}
		p := map[string]any{"type": openAPIDataType(f.Type)}
		if f.Pattern != "" {
			p["pattern"] = patterns[f.Pattern]
		}
		if f.Enum != "" {
			if f.Type == board.DataString {
				p["enum"] = enums[f.Enum]
			} else {
				p["description"] = "Each value one of: " + strings.Join(enums[f.Enum], ", ") + "."
			}
		}
		if f.Limit != "" {
			p["x-limit"] = map[string]any{"name": f.Limit, "value": limits[f.Limit]}
		}
		props[f.Name] = p
		if f.Required {
			required = append(required, f.Name)
		}
	}
	out := map[string]any{"title": operation + " data", "type": "object", "properties": props, "required": required,
		"description": "The JSON object " + operation + "'s data string holds."}
	if !spec.Lax {
		out["additionalProperties"] = false
	} else {
		notes = append(notes, "names it does not know are ignored")
	}
	if len(notes) > 0 {
		out["description"] = out["description"].(string) + " " + strings.Join(notes, "; ") + "."
	}
	return out
}

// openAPIDataHidden reports an operation whose data is left out while its
// feature is off: the allowance ledger's, which no surface mentions then
// (TestRFC0012SurfacesInvisibleWhileOff).
func (s *Server) openAPIDataHidden(operation string) bool {
	return strings.HasPrefix(operation, "allowance.") && s.cfg.Features.Ledger == board.LedgerOff
}

// addDataSchemas adds the data schema of every operation hidden does not
// name, and OperationData.
func addDataSchemas(schemas map[string]any, hidden func(string) bool) {
	byOp := map[string]any{}
	for _, op := range board.Operations() {
		spec, ok := op.Data()
		if !ok || hidden(op.Name) {
			continue
		}
		name := openAPIDataSchemaName(op.Name)
		schemas[name] = openAPIDataSchema(op.Name, spec)
		byOp[op.Name] = map[string]any{"$ref": "#/components/schemas/" + name}
	}
	schemas["OperationData"] = map[string]any{"type": "object", "properties": byOp, "additionalProperties": false,
		"description": "Every operation that takes data, keyed by its name, with the schema of its data: a lookup table, never a request body."}
}
