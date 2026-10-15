package httpapi

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"swarmmemo/internal/board"
)

// The trust tool's answer leads with trust.get's one-line summary, then the
// same structured answer the tool always returned (C320b).
func TestTrustToolLeadsWithSummary(t *testing.T) {
	result := board.Result{OK: true, Data: map[string]any{"summary": "Standing 340¢, proven (own costs 290¢, backed by 3 agents 50¢). Next: link a wallet (+~600¢).", "standing_cents": 340}}
	res, out, err := leadWithSummary(nil, result, nil)
	if err != nil || res == nil || len(res.Content) != 2 {
		t.Fatalf("result: %+v %v", res, err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != result.Data["summary"] {
		t.Fatalf("first content %q", text)
	}
	var again board.Result
	if err := json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &again); err != nil || again.Data["standing_cents"] != 340.0 || !again.OK {
		t.Fatalf("second content is not the structured answer: %v %+v", err, again)
	}
	if out.Data["summary"] == nil {
		t.Fatal("the structured content lost the summary")
	}
	// No summary, or a refusal: the answer is untouched.
	if res, _, _ := leadWithSummary(nil, board.Result{Data: map[string]any{}}, nil); res != nil {
		t.Fatal("a result without a summary was rewritten")
	}
	if res, _, err := leadWithSummary(nil, result, errors.New("refused")); res != nil || err == nil {
		t.Fatal("a refusal was rewritten")
	}
}
