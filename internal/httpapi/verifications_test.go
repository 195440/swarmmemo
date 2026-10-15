package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C353: a hosted identity publishes a verification over MCP; anyone reads it
// over MCP and HTTP (by id, by repository and commit), and /capabilities and
// /openapi.json describe it.
func TestVerificationOverMCPAndHTTP(t *testing.T) {
	_, s := hostedServerWith(t, board.Features{Services: []string{"notary"}})
	token := newIdentity(t, s, "verify-desk")["token"].(string)
	commit := strings.Repeat("ab", 20)
	args := map[string]any{"repo": "https://github.com/Owner/Tool", "commit": commit, "command": "go test ./...", "environment": "go 1.25, linux, no network",
		"output_sha256": strings.Repeat("cd", 32), "checks": []map[string]any{{"property": "tests", "state": "pass", "note": "ok"}, {"property": "race", "state": "not_checked"}},
		"limits": "Only linux/amd64."}
	published := mustTool(t, s, "/mcp/t/"+token, "", "verify_publish", args)
	v, _ := dig(published, "data", "verification").(map[string]any)
	id, _ := v["id"].(string)
	if !board.ValidVerificationID(id) || v["result"] != "passed" || v["repo"] != "https://github.com/owner/tool" || !strings.HasPrefix(dig(published, "data", "markdown").(string), "[![Verified by SwarmMemo]") {
		t.Fatalf("verify_publish: %v", published)
	}
	// Keyless, the publish tool is not there; the read is.
	if _, failure := callTool(t, s, "/mcp", "", "verify_publish", args); failure == "" {
		t.Fatal("verify_publish ran without a hosted identity")
	}
	read := mustTool(t, s, "/mcp", "", "read_verification", map[string]any{"id": id})
	if dig(read, "data", "verification", "id") != id {
		t.Fatalf("read_verification: %v", read)
	}
	listed := mustTool(t, s, "/mcp", "", "read_verification", map[string]any{"repo": "https://github.com/owner/tool", "commit": commit})
	if list, _ := dig(listed, "data", "verifications").([]any); len(list) != 1 {
		t.Fatalf("read_verification list: %v", listed)
	}
	for _, path := range []string{"/api/verifications/" + id, "/api/verifications?repo=https://github.com/Owner/Tool&commit=" + commit, "/api/verifications"} {
		w := makeRequest(s, "GET", path, "", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"`+id+`"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for path, code := range map[string]int{"/api/verifications/0000000000000000": 404, "/api/verifications?repo=a&file=b": 400, "/api/verifications?commit=" + commit: 400} {
		if w := makeRequest(s, "GET", path, "", ""); w.Code != code {
			t.Errorf("%s: %d, want %d", path, w.Code, code)
		}
	}
	var caps map[string]any
	_ = json.Unmarshal(makeRequest(s, "GET", "/capabilities", "", "").Body.Bytes(), &caps)
	if dig(caps, "verifications", "per_day") != float64(board.VerificationsPerDay) || dig(caps, "verifications", "instructions") != "/protocol.md#verified-by-swarmmemo" {
		t.Fatalf("capabilities: %v", caps["verifications"])
	}
	openapi := makeRequest(s, "GET", "/openapi.json", "", "").Body.String()
	if !strings.Contains(openapi, `"/api/verifications/{id}"`) || !strings.Contains(openapi, `"/api/verifications"`) {
		t.Fatal("openapi lacks the verification reads")
	}
	if llms := makeRequest(s, "GET", "/llms.txt", "", "").Body.String(); !strings.Contains(llms, "verify.publish") || !strings.Contains(llms, "/v/ID/badge.svg") {
		t.Fatal("llms.txt lacks the verification line")
	}
}
