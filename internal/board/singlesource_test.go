package board

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// goSource is one non-test Go file under internal/ or cmd/, parsed.
type goSource struct {
	path string // relative to the repository root
	file *ast.File
	fset *token.FileSet
}

// repoGoSources parses every non-test Go file under internal/ and cmd/
// (opt-out: a new file is scanned without being listed).
func repoGoSources(t *testing.T) []goSource {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var out []goSource
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "boardlist" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			out = append(out, goSource{filepath.ToSlash(rel), f, fset})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 200 {
		t.Fatalf("found %d Go files; the scanner is broken", len(out))
	}
	return out
}

// stringLit is the value of a string literal, or false.
func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// A refusal's code and message are written once: a text more than one
// place returns is a constructor (errortexts.go, httpapi/errortexts.go), so
// rewording it rewords every wire. Opt-out: any problem(status, code, msg)
// or Error{Code, Message} literal pair written twice fails.
func TestErrorTextsAreWrittenOnce(t *testing.T) {
	seen := map[string][]string{}
	for _, src := range repoGoSources(t) {
		ast.Inspect(src.file, func(n ast.Node) bool {
			var code, msg ast.Expr
			switch x := n.(type) {
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "problem" && len(x.Args) == 3 {
					code, msg = x.Args[1], x.Args[2]
				}
			case *ast.CompositeLit:
				for _, e := range x.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Code" {
							code = kv.Value
						} else if ok && k.Name == "Message" {
							msg = kv.Value
						}
					}
				}
			}
			c, ok1 := stringLit(code)
			m, ok2 := stringLit(msg)
			if ok1 && ok2 {
				key := c + ": " + m
				seen[key] = append(seen[key], src.fset.Position(n.Pos()).String())
			}
			return true
		})
	}
	if len(seen) < 300 {
		t.Fatalf("found %d error texts; the scanner is broken", len(seen))
	}
	for key, at := range seen {
		if len(at) > 1 {
			t.Errorf("%q is written %d times (%s): make it one constructor in errortexts.go and call that", key, len(at), strings.Join(at, ", "))
		}
	}
}

// The enums with one Go source are not restated as literal lists: the work
// states live in clientfacts.go (WorkStates), the band names in
// allowance.BandNames, the default room in DefaultRoom. One composite
// literal holding five or more work states, three or more band names in one
// function or declaration, or a "lobby" literal anywhere else fails.
func TestEnumsAreWrittenOnce(t *testing.T) {
	tiers := allowance.BandNames[:]
	states := WorkStates()
	for _, src := range repoGoSources(t) {
		for _, decl := range src.file.Decls {
			var tierSeen []string
			ast.Inspect(decl, func(n ast.Node) bool {
				if f, ok := n.(*ast.Field); ok && f.Tag != nil {
					// Struct tags are prose, held by equality tests
					// (httpapi.TestMCPEnumTagsMatchTheCode).
					return false
				}
				s, ok := n.(ast.Expr)
				if !ok {
					return true
				}
				v, ok := stringLit(s)
				if !ok {
					return true
				}
				if v == DefaultRoom && src.path != "internal/board/clientfacts.go" {
					t.Errorf("%s: %q is board.DefaultRoom", src.fset.Position(n.Pos()), v)
				}
				if slices.Contains(tiers, v) && !slices.Contains(tierSeen, v) {
					tierSeen = append(tierSeen, v)
				}
				return true
			})
			if at := src.fset.Position(decl.Pos()); len(tierSeen) >= 3 && !strings.HasPrefix(src.path, "internal/allowance/") {
				t.Errorf("%s names tiers %v: use allowance.BandNames or Band.Name", at, tierSeen)
			}
		}
		if src.path == "internal/board/clientfacts.go" {
			continue
		}
		// Work states are a list when one literal holds five: a
		// transition's rule names a few, a restated list most of them.
		ast.Inspect(src.file, func(n ast.Node) bool {
			list, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			var seen []string
			ast.Inspect(list, func(n ast.Node) bool {
				if e, ok := n.(ast.Expr); ok {
					if v, ok := stringLit(e); ok && slices.Contains(states, v) && !slices.Contains(seen, v) {
						seen = append(seen, v)
					}
				}
				return true
			})
			if len(seen) >= 5 {
				t.Errorf("%s names work states %v: use board.WorkStates or WorkListKinds", src.fset.Position(n.Pos()), seen)
			}
			return true
		})
	}
}
