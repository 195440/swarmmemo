package find

// Languages: an agent declares one it works in with the profile capability
// lang-xx (ISO 639-1), and a query that asks for agents who speak one
// ("agents that speak chinese", "speaks русский") filters to it. Text in
// any language is matched by the trigram index (index.go).

import (
	"slices"
	"strings"
	"unicode"
)

// LanguageCapabilityPrefix starts a language capability: lang-zh.
const LanguageCapabilityPrefix = "lang-"

// languageNames are the ~20 most spoken languages' ISO 639-1 codes and
// their names, in English and in the language itself, lower-cased.
var languageNames = map[string][]string{
	"en": {"english"},
	"zh": {"chinese", "mandarin", "中文", "汉语", "漢語", "普通话", "国语"},
	"es": {"spanish", "español", "espanol", "castellano"},
	"hi": {"hindi", "हिन्दी", "हिंदी"},
	"ar": {"arabic", "العربية", "عربي"},
	"pt": {"portuguese", "português", "portugues"},
	"bn": {"bengali", "bangla", "বাংলা"},
	"ru": {"russian", "русский", "русском"},
	"ja": {"japanese", "日本語"},
	"de": {"german", "deutsch"},
	"fr": {"french", "français", "francais"},
	"ko": {"korean", "한국어"},
	"it": {"italian", "italiano"},
	"tr": {"turkish", "türkçe", "turkce"},
	"vi": {"vietnamese", "tiếng việt", "tieng viet"},
	"pl": {"polish", "polski"},
	"nl": {"dutch", "nederlands"},
	"uk": {"ukrainian", "українська"},
	"id": {"indonesian", "bahasa indonesia"},
	"fa": {"persian", "farsi", "فارسی"},
	"th": {"thai", "ไทย"},
	"sv": {"swedish", "svenska"},
	"he": {"hebrew", "עברית"},
}

// speakWords say a query asks who speaks a language.
var speakWords = map[string]bool{
	"speak": true, "speaks": true, "speaking": true, "speaker": true, "speakers": true, "fluent": true,
}

// LanguageCode is the ISO 639-1 code a language name or code stands for;
// "" for anything else.
func LanguageCode(name string) string {
	n := strings.ToLower(strings.Join(Words(name), " "))
	if _, ok := languageNames[n]; ok {
		return n
	}
	for code, names := range languageNames {
		if slices.Contains(names, n) {
			return code
		}
	}
	return ""
}

// speaksLanguage reads a query that asks for who speaks a language: a
// speak word beside a language's name. It answers the code and the words
// to drop from the text (the speak words and the name's words).
func speaksLanguage(q string) (string, map[string]bool) {
	words := Words(q)
	cue := false
	for _, w := range words {
		if speakWords[w] {
			cue = true
			break
		}
	}
	if !cue {
		return "", nil
	}
	padded := " " + strings.Join(words, " ") + " "
	code, name := "", ""
	for c, names := range languageNames {
		for _, n := range names {
			// The longest name found wins ("bahasa indonesia" over a part).
			if strings.Contains(padded, " "+n+" ") && (len(n) > len(name) || len(n) == len(name) && c < code) {
				code, name = c, n
			}
		}
	}
	if code == "" {
		return "", nil
	}
	drop := map[string]bool{}
	for w := range speakWords {
		drop[w] = true
	}
	for _, w := range strings.Fields(name) {
		drop[w] = true
	}
	return code, drop
}

// nonLatin reports whether s has a letter outside the Latin script
// (Chinese, Cyrillic, Arabic...): such a query is matched by the trigram
// index, which needs no word boundaries or stemmer.
func nonLatin(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

// cjk reports whether r is written without spaces between words.
func cjk(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Thai)
}

// applyLanguage sets f's language when q asks who speaks one and f looks
// for agents (or for no type in particular, then agents): only agents
// declare languages. It answers text less the speak and language words.
func applyLanguage(f *Filters, text []string, q string) []string {
	code, drop := speaksLanguage(q)
	if code == "" || len(f.Types) > 0 && !slices.Contains(f.Types, TypeAgent) {
		return text
	}
	f.Language = code
	if len(f.Types) == 0 {
		f.Types = []string{TypeAgent}
	}
	out := text[:0:0]
	for _, w := range text {
		if !drop[w] {
			out = append(out, w)
		}
	}
	return out
}
