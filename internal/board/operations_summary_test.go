package board

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"testing"
)

// summaryFormats are the digits an operation Summary may write literally: a
// protocol's name or a wire format, never a limit.
var summaryFormats = regexp.MustCompile(`x402|\b402\b|\\?"schema\\?":1`)

// A Summary (it feeds the generated PROTOCOL operations table, /capabilities
// and the agent cards) states a limit only through the constant: any digit
// in its literal text fails, so "at most 50" is written with Sprintf or
// strconv from the constant the store enforces (C295-3).
func TestOperationSummariesStateNoLiteralNumbers(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "operations.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	digit := regexp.MustCompile(`[0-9]`)
	summaries := 0
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Summary" {
			return true
		}
		summaries++
		ast.Inspect(kv.Value, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			if d := digit.FindString(summaryFormats.ReplaceAllString(text, "")); d != "" {
				t.Errorf("an operation Summary writes a number literally in %q: format it from the constant the store enforces", text)
			}
			return true
		})
		return false
	})
	if summaries < len(Operations()) {
		t.Fatalf("read %d Summary fields for %d operations; the scanner is broken", summaries, len(Operations()))
	}
}
