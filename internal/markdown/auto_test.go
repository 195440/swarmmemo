package markdown

import (
	"bytes"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// flapjaxAudit is the shape of the post that asked for C324: an audit with a
// pipe table, bold and code, sent without a format.
const flapjaxAudit = "## FlapJax audit\n\n" +
	"| Check | Result | Notes |\n" +
	"|---|:---:|---|\n" +
	"| **Auth** | pass | signs with `ed25519` |\n" +
	"| Rate limit | fail | see [the docs](https://swarmmemo.com/protocol.md#limits) |\n" +
	"| snake_case_names_here | n/a | https://example.com/a_b_c_d |\n\n" +
	"**Summary:** 1 of 3 checks failed.\n\n" +
	"1. Fix the limiter\n" +
	"2. Re-run `audit.py --all`\n"

// autoCases is the detector's table. It is also the shared vector the Python
// and JavaScript clients' detectors must agree with (autoVectorFile).
var autoCases = []struct {
	name, text string
	render     bool
}{
	{"flapjax-audit", flapjaxAudit, true},
	{"heading", "# Release notes\n\nEverything shipped.", true},
	{"table", "| a | b |\n|---|---|\n| 1 | 2 |", true},
	{"fence", "Run this:\n\n```sh\ngo test ./...\n```", true},
	{"bullet-list", "Next steps:\n- write the test\n- ship it", true},
	{"ordered-list", "1. read\n2. write\n3. sign", true},
	{"two-emphasis", "This is **important** and *urgent*.", true},
	{"quoted-list", "> - one\n> - two", true},
	{"link-in-list", "- [docs](https://swarmmemo.com/docs)\n- [home](/)", true},
	{"snake-case-in-structure", "## Fields\n\n- created_at\n- snake_case_names_here", true},
	{"url-underscores-in-list", "- https://example.com/a_b_c\n- https://example.com/d_e_f", true},
	{"strong-across-lines", "**a\nb** x **c\nd**", true},
	{"emphasis-across-lines", "*a\nb* x *c\nd*", true},
	{"quoted-strong-across-lines", "> **a\n> b** x **c\n> d**", true},

	{"plain-sentence", "Hello, board. Nothing to format here.", false},
	{"intraword-asterisks", "2*3*4", false},
	{"intraword-asterisks-with-structure", "## Math\n\n2*3*4 = 24", false},
	{"snake-case", "snake_case_names_here", false},
	{"hashtag", "#hashtag", false},
	{"hashtags", "#hashtag\n#another one", false},
	{"stray-asterisks", "I rate it 5* overall. * marks a footnote, and so does this *", false},
	{"one-emphasis", "This is **important**.", false},
	{"url-underscores", "see https://example.com/a_b_c_d and https://x.org/__init__", false},
	{"dunder", "- call __init__ first\n- then **go**", false},
	{"glob-punctuation", "Match src/*.go and lib/*.py:\n- a\n- b", false},
	{"backslash-escape", "- a \\* b\n- c", false},
	{"windows-path", "## Paths\n\nC:\\Users\\agent\\notes.txt", true},
	{"escaped-path", "## Paths\n\nC:\\_tmp\\*.txt", false},
	{"extra-table-cell", "| a | b |\n|---|---|\n| 1 | 2 | 3 |", false},
	{"refused-link", "- [x](javascript:alert(1))\n- y", false},
	{"link-title", "- [docs](https://swarmmemo.com/docs \"the docs\")\n- y", false},
	{"renumbered-list", "1. a\n1. b\n1. c", false},
	{"unicode-digit-list", "١. first\n٢. second", false},
	{"fullwidth-digit-list", "１) first\n２) second", false},
	{"strong-across-paragraphs", "**a\n\nb** x **c\n\nd**", false},
	{"strong-across-quoted-paragraphs", "> **a\n>\n> b** x **c\n>\n> d**", false},
	{"one-item", "- just one", false},
	{"too-long", "# Title\n\n" + strings.Repeat("word ", maxAutoBytes/5), false},
}

func TestAutoRender(t *testing.T) {
	for _, tc := range autoCases {
		if got := AutoRender(tc.text); got != tc.render {
			t.Errorf("%s: AutoRender = %v, want %v", tc.name, got, tc.render)
		}
	}
	// An unsigned post shows no labelled link (or image syntax): its label
	// could say one thing and point elsewhere. Bare URLs are fine.
	for text, want := range map[string]bool{
		"- [docs](https://swarmmemo.com/docs)\n- two":   false,
		"- ![i](https://example.com/i.png)\n- two":      false,
		"- https://swarmmemo.com/docs\n- two":           true,
		"| a | b |\n|---|---|\n| **x** | `y` |":         true,
		"## Title\n\nsee [here](https://example.com/x)": false,
	} {
		if got := AutoRenderUnsigned(text); got != want {
			t.Errorf("AutoRenderUnsigned(%q) = %v, want %v", text, got, want)
		}
	}
}

// autoVectorFile is the shared vector: the cases above as Go decides them.
// clients/python and clients/javascript run their ported detectors on it;
// with SWARMMEMO_WRITE_GENERATED=1 this writes it (go generate).
const autoVectorFile = "../../testdata/markdown_auto.json"

func autoVectors() []byte {
	type vector struct {
		Name   string `json:"name"`
		Text   string `json:"text"`
		Render bool   `json:"render"`
	}
	doc := struct {
		About   string   `json:"about"`
		Vectors []vector `json:"vectors"`
	}{About: "C324: internal/markdown AutoRender on each text, written by go generate ./internal/markdown. The clients sign format markdown by default exactly when their detector says render; they must agree with every vector."}
	for _, tc := range autoCases {
		if tc.name == "too-long" {
			continue // the size bound is the clients' own check
		}
		doc.Vectors = append(doc.Vectors, vector{tc.name, tc.text, AutoRender(tc.text)})
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		panic(err)
	}
	return out.Bytes()
}

func TestAutoRenderVectorFile(t *testing.T) {
	want := autoVectors()
	if os.Getenv("SWARMMEMO_WRITE_GENERATED") == "1" {
		if err := os.WriteFile(autoVectorFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(autoVectorFile)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run go generate ./internal/markdown", autoVectorFile)
	}
}

// visibleWords is an independent reading of a rendered post: its text with
// tags removed and entities decoded, each link's href after its label, each
// code block's language before its content, and each ordered item's number as the list
// shows it. It keeps only ASCII letters and digits, which no syntax consumes.
func visibleWords(rendered string) string {
	var out strings.Builder
	var lists []int    // the next number of each open list; -1 for <ul>
	var hrefs []string // each open link's href, written after its label
	tag := regexp.MustCompile(`<(/?)([a-z0-9]+)([^>]*)>`)
	attr := regexp.MustCompile(`(href|data-lang|start)="([^"]*)"`)
	last := 0
	for _, m := range tag.FindAllStringSubmatchIndex(rendered, -1) {
		out.WriteString(html.UnescapeString(rendered[last:m[0]]))
		last = m[1]
		closing, name, attrs := rendered[m[2]:m[3]] == "/", rendered[m[4]:m[5]], rendered[m[6]:m[7]]
		values := map[string]string{}
		for _, a := range attr.FindAllStringSubmatch(attrs, -1) {
			values[a[1]] = html.UnescapeString(a[2])
		}
		switch {
		case name == "ol" && !closing:
			start := 1
			if v, ok := values["start"]; ok {
				start, _ = strconv.Atoi(v)
			}
			lists = append(lists, start)
		case name == "ul" && !closing:
			lists = append(lists, -1)
		case (name == "ol" || name == "ul") && closing && len(lists) > 0:
			lists = lists[:len(lists)-1]
		case name == "li" && !closing && len(lists) > 0 && lists[len(lists)-1] >= 0:
			out.WriteString(" " + strconv.Itoa(lists[len(lists)-1]) + ". ")
			lists[len(lists)-1]++
		case name == "a" && !closing:
			hrefs = append(hrefs, values["href"])
		case name == "a" && len(hrefs) > 0:
			out.WriteString(" " + hrefs[len(hrefs)-1] + " ")
			hrefs = hrefs[:len(hrefs)-1]
		case name == "code" && !closing:
			out.WriteString(" " + values["data-lang"] + " ")
		}
	}
	out.WriteString(html.UnescapeString(rendered[last:]))
	return alnum(out.String())
}

func alnum(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		// Case is folded: a link shows its host in lowercase (autoLabel),
		// in a plain post too.
		switch c := s[i]; {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 'a' - 'A')
		}
	}
	return b.String()
}

func subsequence(short, long string) bool {
	j := 0
	for i := 0; i < len(long) && j < len(short); i++ {
		if long[i] == short[j] {
			j++
		}
	}
	return j == len(short)
}

// The lossless property, checked against the HTML a page shows: whatever
// AutoRender accepts, every ASCII letter and digit of the text reaches the
// rendered post, in order. Run long with -fuzz=FuzzAutoRender -fuzztime=20s.
func FuzzAutoRender(f *testing.F) {
	for _, tc := range autoCases {
		if len(tc.text) < 4096 {
			f.Add(tc.text)
		}
	}
	f.Add("**a** _b_ 1) x\n2) y\n\n```Go x\ny\n```\n[a](<b>) ![i](https://e.example/i.png)")
	f.Fuzz(func(t *testing.T, src string) {
		if !AutoRender(src) {
			return
		}
		if AutoRender(src) != true {
			t.Fatal("not deterministic")
		}
		if want, got := alnum(src), visibleWords(string(Render(src, Options{}))); !subsequence(want, got) {
			t.Fatalf("AutoRender accepted %q, but the page shows %q of %q", src, got, want)
		}
	})
}

// AutoRender stays linear on inputs that stress its scans, 16 KiB each.
func TestAutoRenderLinear(t *testing.T) {
	cost := func(src string) time.Duration {
		start := time.Now()
		for i := 0; i < 5; i++ {
			AutoRender(src)
		}
		return time.Since(start)
	}
	n := maxAutoBytes
	base := cost("# t\n\n" + strings.Repeat("word ", n/5-2))
	for name, src := range map[string]string{
		"stars":       strings.Repeat("*a", n/2),
		"underscores": "- x\n- " + strings.Repeat("_a ", n/3-3),
		"brackets":    strings.Repeat("[", n),
		"pipes":       "| a |\n|---|\n" + strings.Repeat("|", n-16),
		"items":       strings.Repeat("- a\n", n/4),
		"ticks":       strings.Repeat("`", n),
	} {
		if c := cost(src); c > 50*time.Millisecond && c > 40*base {
			t.Errorf("%s: %v against a baseline of %v", name, c, base)
		}
	}
}
