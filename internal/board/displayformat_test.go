package board

import (
	"strings"
	"testing"
)

// C324: every read carries display_format, decided by DisplayFormatOf: a
// signed format, or unformatted text markdown.AutoRender accepts (unsigned:
// without a labelled link), is markdown; anything else, a tombstone too, plain.
func TestDisplayFormatOnReads(t *testing.T) {
	s := openTest(t, Config{})
	key := keyFor(204)
	table := "| a | b |\n|---|---|\n| **x** | `y` |"
	for _, tc := range []struct {
		name string
		c    Command
		want string
	}{
		{"signed table", signed(key, Command{Operation: "post", Text: table}), PostFormatMarkdown},
		{"anonymous table", Command{Operation: "post", Text: table + "\n"}, PostFormatMarkdown},
		{"signed format", signed(key, Command{Operation: "post", Text: "plain words", Data: MarkdownPostData}), PostFormatMarkdown},
		{"plain", signed(key, Command{Operation: "post", Text: "plain words, 2*3*4"}), DisplayPlain},
		{"lossy", signed(key, Command{Operation: "post", Text: "## Math\n\n2*3*4"}), DisplayPlain},
		{"signed labelled link", signed(key, Command{Operation: "post", Text: "- [a](https://example.com/a)\n- b"}), PostFormatMarkdown},
		{"anonymous labelled link", Command{Operation: "post", Text: "- [a](https://example.com/a)\n- c"}, DisplayPlain},
	} {
		id := run(t, s, tc.c).Receipt.ID
		if got := get(t, s, id).DisplayFormat; got != tc.want {
			t.Errorf("%s: display_format %q, want %q", tc.name, got, tc.want)
		}
		if tc.name == "signed table" {
			if err := s.Moderate(testContext, id, "test", true); err != nil {
				t.Fatal(err)
			}
			if got := get(t, s, id).DisplayFormat; got != DisplayPlain {
				t.Errorf("tombstone display_format %q", got)
			}
		}
	}
	if SignedFormat(MarkdownPostData) != PostFormatMarkdown || SignedFormat("") != "" || SignedFormat(dataJSON(`"supersedes":"`+strings.Repeat("a", 32)+`"`)) != "" {
		t.Error("SignedFormat")
	}
}
