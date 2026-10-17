package httpapi

import (
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/services/servicestest"
)

// escapedValue is a value well under the 64 KiB a memory value or a doc
// may hold, all of characters json.Marshal writes as six bytes each.
var escapedValue = strings.Repeat("<", 40_000)

// C409: a value within its advertised limit is not refused for how its
// sender escaped it. Over HTTP, a client that escapes < as six bytes (as Go's
// json.Marshal does) puts a 40,000-byte memory value.
func TestEscapedValueFitsOverHTTP(t *testing.T) {
	features := board.Features{Services: []string{"memory"}}
	store, err := board.Open(filepath.Join(t.TempDir(), "board.sqlite"), board.Config{ServiceID: "swarmmemo.com", Features: features})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	store.UseServiceMeter(servicestest.NewMeter(1<<30), &servicestest.Params{})
	h := New(store, nil, Config{Features: features, PublicURL: "https://swarmmemo.com", ServiceID: "swarmmemo.com"})
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	data := memoryData("put", map[string]string{"key": "escaped", "value": escapedValue}, 100_000)
	if !strings.Contains(data, `\`+"u003c") || len(data) <= 2*len(escapedValue)+1024 {
		t.Fatalf("the data is not escaped as the bug needs: %d bytes", len(data))
	}
	body, _ := json.Marshal(signService(key, board.Command{Operation: "service.call", Target: "memory", Data: data}))
	w := makeRequest(h, "POST", "https://swarmmemo.com/v1/command", string(body), "application/json")
	if res := decodeResult(t, w.Body.Bytes()); w.Code != 200 || dig(res, "data", "call", "state") != "done" {
		t.Fatalf("escaped memory put: %d %.400s", w.Code, w.Body.String())
	}
}

// The same over the hosted MCP path, which signs the args json.Marshal
// writes: a 40,000-byte doc of < is created.
func TestEscapedValueFitsOverHostedMCP(t *testing.T) {
	s, _ := hostedServicesServer(t, "docs")
	me := newIdentity(t, s, "")
	got := mustTool(t, s, "/mcp/t/"+me["token"].(string), "", "docs_create", map[string]any{"title": "angles", "text": escapedValue, "max_cost": 1000})
	if dig(got, "data", "call", "state") != "done" {
		t.Fatalf("hosted docs_create of an escaped text: %.400v", got)
	}
}
