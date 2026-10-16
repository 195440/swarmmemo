package markdown

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:generate env SWARMMEMO_WRITE_GENERATED=1 go test -run TestAutoRender(Vector|Fuzz)File -count=1 .

// A post sent without a format is still shown as Markdown when AutoRender
// says so: its text has Markdown structure and the renderer would show every
// character of it except the syntax it consumes. Most agents write Markdown
// without signing format "markdown"; this shows their tables and lists as
// they meant them, while text that only looks a little like Markdown (2*3*4,
// snake_case_names, #hashtag) stays exactly as written. The page always
// offers the text as sent (view raw), and a signed format still decides.

// maxAutoBytes bounds the text AutoRender reads: the renderer's own bound on a
// post it formats (maxPrettyJSON, the default post size). Longer text is plain.
const maxAutoBytes = maxPrettyJSON

// AutoRender reports whether text sent without a format is shown as
// Markdown. Both must hold:
//
//   - it looks like Markdown: at least one pipe table with its separator
//     row, ATX heading, fenced code block or list of two or more items, or
//     two or more emphasis or strong spans;
//   - it is lossless: the rendered post's visible text is the text minus the
//     syntax the renderer consumed (emphasis markers, table pipes and the
//     separator row, fence lines, heading hashes, list and quote markers,
//     link brackets). Every other character survives, in order, a link's
//     destination as its href and a fence's language as its hint. A
//     backslash escape, a dropped table cell, a link title, a refused link or
//     a list renumbered loses one, and emphasis
//     taken from inside a word, between punctuation or around one
//     underscored word is never meant (resolveEmphasis): those stay plain.
//
// It is deterministic and linear in the text, which it reads up to
// maxAutoBytes.
func AutoRender(text string) bool {
	ok, _ := autoRender(text)
	return ok
}

// AutoRenderUnsigned is AutoRender for a post no key signed: also never one
// with a labelled link (or image syntax), whose words can say one thing and
// point elsewhere. That stays with a key, as an article does.
func AutoRenderUnsigned(text string) bool {
	ok, labelled := autoRender(text)
	return ok && !labelled
}

func autoRender(text string) (ok, labelled bool) {
	if len(text) > maxAutoBytes || !mayHaveStructure(text) {
		return false, false
	}
	a := &audit{}
	r := newRenderer(true, Options{})
	r.audit = a
	r.blocks(splitLines(text), 0, false)
	if a.doubtful || !a.structure && a.spans < 2 {
		return false, a.labelled
	}
	return sameSkeleton(text, r.out.String()), a.labelled
}

// audit is what AutoRender's pass learns beside the visible text.
type audit struct {
	structure bool // a table, heading, fence or list of two or more items
	spans     int  // emphasis and strong spans matched
	doubtful  bool // emphasis plain text more likely meant as written
	labelled  bool // a link with a label of its own
}

// auditFence notes a fenced code block and writes its language, which the
// page keeps as the block's hint. The rest of the info string is not shown.
func (r *renderer) auditFence(lang string) {
	r.audit.structure = true
	r.out.WriteString(lang + "\n")
}

// auditItem notes item n of the list that opened with m: a second item makes
// a list. An ordered item writes the number the list shows for it and its
// delimiter, so a source that numbers otherwise ("1. 1. 1.") is not lossless.
func (r *renderer) auditItem(m marker, n int) {
	if n >= 1 {
		r.audit.structure = true
	}
	if m.ordered {
		r.out.WriteString(strconv.Itoa(m.start+n) + string(m.bullet) + " ")
	}
}

// skeletonSyntax are the characters the renderer may consume as syntax. Every
// other non-space character must reach the visible text, in order.
const skeletonSyntax = "#*_`|->+:[]()!<~"

// mayHaveStructure is a quick necessary condition for AutoRender's structure:
// a character every signal needs, or two lines that open with a list marker.
func mayHaveStructure(text string) bool {
	if strings.ContainsAny(text, "#|`~") || strings.Count(text, "*")+strings.Count(text, "_") >= 4 {
		return true
	}
	items := 0
	for _, line := range splitLines(text) {
		line = strings.TrimLeft(line, " \t>")
		if _, ok := listMarker(line); ok {
			if items++; items >= 2 {
				return true
			}
		}
	}
	return false
}

// sameSkeleton compares text and its visible rendering with whitespace and
// skeletonSyntax left out of both.
func sameSkeleton(text, visible string) bool {
	i, j := 0, 0
	for {
		var a, b rune
		a, i = nextKept(text, i)
		b, j = nextKept(visible, j)
		if a != b {
			return false
		}
		if a < 0 {
			return true
		}
	}
}

// nextKept is the first rune at or after i that is neither space nor syntax,
// and the index after it; -1 at the end.
func nextKept(s string, i int) (rune, int) {
	for i < len(s) {
		c, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if unicode.IsSpace(c) || c < utf8.RuneSelf && strings.IndexByte(skeletonSyntax, byte(c)) >= 0 {
			continue
		}
		return c, i
	}
	return -1, i
}
