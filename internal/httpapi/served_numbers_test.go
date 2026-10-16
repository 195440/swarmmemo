package httpapi

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// numberWithUnit is a number stated with a unit agents act on: a size, a
// duration, a count of credits or an amount of USDC ("64 KiB", "1–3600
// seconds", "a 30-day record", "0.25 USDC").
var numberWithUnit = regexp.MustCompile(`\b\d[\d,]*(?:\.\d+)?(?:\s*(?:-|–|to)\s*\d[\d,]*(?:\.\d+)?)?(?:\s|-)(?:UTF-8 )?(?:KiB|MiB|bytes?|characters?|chars?|seconds?|minutes?|hours?|days?|credits?|USDC)\b`)

// numberContext is the match with up to three words before it on its line,
// the key the allowlist names it by.
var numberContext = regexp.MustCompile(`(?:[^\s{}]+\s+){0,3}$`)

// servedNumber is one number-with-unit in served copy, keyed for the
// allowlist as "FILE|CONTEXT MATCH".
type servedNumber struct {
	key, file string
	line      int
}

// servedCopy is the copy agents and people read, as (file, text) pairs: the
// hand-written prose of docs/PROTOCOL.md (generated sections are derived and
// left out), the quickstart, the web templates (their {{actions}} are
// derived), and the string literals of the Go that writes /llms.txt,
// /capabilities, OpenAPI, the MCP tool descriptions and the operation
// summaries. MCP argument tags are left out: TestMCPArgumentTagsMatchTheCode
// holds every numeric one to its constant.
func servedCopy(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	read := func(path string) string {
		raw, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	protocol := read("docs/PROTOCOL.md")
	generated := regexp.MustCompile(`(?s)<!-- BEGIN GENERATED: ([a-z0-9-]+)[^>]*-->.*?<!-- END GENERATED: ([a-z0-9-]+) -->`)
	out["docs/PROTOCOL.md"] = []string{generated.ReplaceAllStringFunc(protocol, func(block string) string {
		return strings.Repeat("\n", strings.Count(block, "\n"))
	})}
	out["internal/web/quickstart.md.tmpl"] = []string{read("internal/web/quickstart.md.tmpl")}
	action := regexp.MustCompile(`\{\{.*?\}\}`)
	templates, _ := filepath.Glob("../../internal/web/templates/*.html")
	if len(templates) < 20 {
		t.Fatalf("found %d templates; the glob is broken", len(templates))
	}
	for _, path := range templates {
		rel := strings.TrimPrefix(path, "../../")
		out[rel] = []string{action.ReplaceAllString(read(rel), "{}")}
	}
	goFiles := []string{"internal/httpapi/discovery.go", "internal/httpapi/readroutes.go", "internal/board/operations.go"}
	for _, glob := range []string{"internal/httpapi/mcp*.go", "internal/web/*.go"} {
		matches, _ := filepath.Glob("../../" + glob)
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				goFiles = append(goFiles, strings.TrimPrefix(m, "../../"))
			}
		}
	}
	for _, rel := range goFiles {
		f, err := parser.ParseFile(token.NewFileSet(), "../../"+rel, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		tags := map[*ast.BasicLit]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if field, ok := n.(*ast.Field); ok && field.Tag != nil {
				tags[field.Tag] = true
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || tags[lit] {
				return true
			}
			if text, err := strconv.Unquote(lit.Value); err == nil {
				out[rel] = append(out[rel], text)
			}
			return true
		})
	}
	return out
}

// servedNumbers is every number-with-unit in servedCopy that no docFacts
// phrase checks.
func servedNumbers(t *testing.T) []servedNumber {
	t.Helper()
	checked := map[string][][2]int{} // file -> spans a docFact covers
	var found []servedNumber
	copyByFile := servedCopy(t)
	for _, f := range docFacts() {
		texts, ok := copyByFile[f.file]
		if !ok {
			continue
		}
		before, after, _ := strings.Cut(f.phrase, "{N}")
		re := regexp.MustCompile(regexp.QuoteMeta(before) + factValue + regexp.QuoteMeta(after))
		for _, loc := range re.FindAllStringIndex(texts[0], -1) {
			checked[f.file] = append(checked[f.file], [2]int{loc[0], loc[1]})
		}
	}
	for file, texts := range copyByFile {
		for i, text := range texts {
			for _, loc := range numberWithUnit.FindAllStringIndex(text, -1) {
				covered := false
				if i == 0 {
					for _, span := range checked[file] {
						if loc[0] >= span[0] && loc[1] <= span[1] {
							covered = true
						}
					}
				}
				if covered {
					continue
				}
				lineStart := strings.LastIndex(text[:loc[0]], "\n") + 1
				context := numberContext.FindString(text[lineStart:loc[0]])
				key := file + "|" + strings.Join(strings.Fields(context+text[loc[0]:loc[1]]), " ")
				found = append(found, servedNumber{key: key, file: file, line: strings.Count(text[:loc[0]], "\n") + 1})
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].key < found[j].key })
	return found
}

// Every number with a unit in served copy is derived (generated, formatted
// from its constant, or checked by a docFacts phrase) or on servedNumbersAllowed
// with its reason. The list may only shrink: an entry that no longer matches
// fails until it is removed, and a new literal fails until it is derived.
func TestServedCopyDerivesNumbersWithUnits(t *testing.T) {
	found := servedNumbers(t)
	if len(found) < 20 {
		t.Fatalf("found %d numbers with units; the scanner is broken", len(found))
	}
	if os.Getenv("SERVED_NUMBERS_DUMP") != "" {
		seen := map[string]bool{}
		for _, n := range found {
			if !seen[n.key] {
				seen[n.key] = true
				fmt.Printf("\t%q: %s,\n", n.key, "debt")
			}
		}
	}
	matched := map[string]bool{}
	for _, n := range found {
		matched[n.key] = true
		if _, ok := servedNumbersAllowed[n.key]; !ok {
			where := n.file
			if strings.HasSuffix(n.file, ".md") || strings.HasSuffix(n.file, ".html") || strings.HasSuffix(n.file, ".tmpl") {
				where = fmt.Sprintf("%s:%d", n.file, n.line)
			}
			t.Errorf("%s states %q literally: derive it from its constant (Sprintf, a template fact, a generated section or a docFacts phrase)", where, n.key[strings.Index(n.key, "|")+1:])
		}
	}
	for key := range servedNumbersAllowed {
		if !matched[key] {
			t.Errorf("servedNumbersAllowed lists %q, which no served copy states any more: remove it (the list only shrinks)", key)
		}
	}
	if len(servedNumbersAllowed) > servedNumbersCeiling {
		t.Errorf("servedNumbersAllowed has %d entries, over its ceiling %d: derive the new number instead", len(servedNumbersAllowed), servedNumbersCeiling)
	}
}
