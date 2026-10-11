package httpapi

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// Find at parity (C188): GET /api/find, the MCP tool find and the /find
// page make one read and agree on its answer; the query names map onto
// its data; /capabilities, /llms.txt and openapi.json say how to use it.
func TestFindSurfaces(t *testing.T) {
	s := reviewServer(t, true)
	key := ed25519.NewKeyFromSeed(append(make([]byte, 31), 211))
	for i, c := range []board.Command{{Operation: "agent.register", Handle: "lisbonweather"},
		{Operation: "agent.profile.publish", Data: `{"schema":1,"description":"Weather for Lisbon, as a forecast.","capabilities":["forecast"],"availability":"available"}`}} {
		if w := topupRequest(s, signService(key, c), ""); w.Code != 200 {
			t.Fatalf("seed %d: %d %s", i, w.Code, w.Body)
		}
	}

	code, body := reviewGet(t, s, "/api/find?q="+url.QueryEscape("lisbon forecast agents")+"&max_price=any&explain=true")
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	data := body["data"].(map[string]any)
	results := data["results"].([]any)
	first := results[0].(map[string]any)
	if first["url"] != "/@lisbonweather" || first["type"] != "agent" || first["why"] == "" || first["explain"] == nil {
		t.Fatalf("first %v", first)
	}
	filters := data["filters"].(map[string]any)
	if len(filters["type"].([]any)) != 1 || filters["max_price"] != "" || data["parsed_by"] != "rules" {
		t.Fatalf("filters %v %v", filters, data["parsed_by"])
	}
	if code, body = reviewGet(t, s, "/api/find?type=people"); code != 400 || body["error"].(map[string]any)["code"] != "invalid_find" {
		t.Fatalf("refusal %d %v", code, body)
	}
	if code, _ = reviewGet(t, s, "/api/find?type=agent&type=mcp"); code != 400 {
		t.Fatal("a name given twice is refused")
	}

	// MCP: the same first result.
	server := httptest.NewServer(s)
	defer server.Close()
	out := mcpCall(t, server.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"find","arguments":{"query":"lisbon forecast agents"}}}`)
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"/@lisbonweather"`) || strings.Contains(string(raw), `"isError":true`) {
		t.Fatalf("mcp find %s", raw)
	}

	// The page: the same result, its filter chip, and Find current in the header.
	r := httptest.NewRequest("GET", "https://swarmmemo.com/find?q="+url.QueryEscape("lisbon forecast agents"), nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	page := w.Body.String()
	if w.Code != 200 || !strings.Contains(page, `href="/@lisbonweather"`) || !strings.Contains(page, `<a href="/find" aria-current="page">Find</a>`) || !strings.Contains(page, `class="find-chip is-current"`) {
		t.Fatalf("page %d", w.Code)
	}

	// Discovery.
	_, caps := reviewGet(t, s, "/capabilities")
	if f, _ := caps["find"].(map[string]any); f["http"] != "GET /api/find?q=TEXT" || f["mcp"] != "find" {
		t.Fatalf("capabilities find %v", caps["find"])
	}
	r = httptest.NewRequest("GET", "https://swarmmemo.com/llms.txt", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "/api/find?q=weather+under+a+cent+returns+JSON") {
		t.Fatal("llms.txt names /api/find with a verbatim example")
	}
	r = httptest.NewRequest("GET", "https://swarmmemo.com/openapi.json", nil)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"/api/find"`) {
		t.Fatal("openapi.json documents /api/find")
	}
}
