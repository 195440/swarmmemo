package find

import (
	"context"
	"slices"
	"testing"
)

// languageDocs add docs written in Chinese and Russian, and agents that
// declare languages, to the seeded catalogue.
func languageDocs() []Doc {
	return append(seedDocs(),
		Doc{Type: TypeService, Kind: KindX402, ID: "x402:zh1", Name: "天气 API", Description: "提供全国城市的天气预报服务", URL: "/s/zh1", Price: 1000, Network: "base", Output: "json", Quality: Quality{LabLower: -1}},
		Doc{Type: TypeService, Kind: KindX402, ID: "x402:ru1", Name: "Метео", Description: "Прогноз погоды для любого города России", URL: "/s/ru1", Price: 1000, Network: "base", Output: "json", Quality: Quality{LabLower: -1}},
		Doc{Type: TypeAgent, ID: "fp-zh", Name: "lingbot", Description: "I help with research.", Terms: "research lang-zh lang-en", URL: "/agent/fp-zh", Price: Unpriced, Quality: Quality{LabLower: -1}},
		Doc{Type: TypeAgent, ID: "fp-ru", Name: "volga", Description: "I help with research.", Terms: "research lang-ru", URL: "/agent/fp-ru", Price: Unpriced, Quality: Quality{LabLower: -1}},
	)
}

func languageEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(Config{Source: func(context.Context) ([]Doc, error) { return languageDocs(), nil }, Vocab: testVocab})
	t.Cleanup(e.Close)
	return e
}

// Text in any language is found: Chinese without spaces, Russian in
// another inflection, and one- and two-character Chinese words.
func TestFindAnyLanguage(t *testing.T) {
	e := languageEngine(t)
	ctx := context.Background()
	for q, want := range map[string]string{
		"天气预报":     "x402:zh1", // a run inside a longer run
		"城市天气服务":   "x402:zh1", // windows of a run the doc does not hold whole
		"天气":       "x402:zh1", // two characters: a substring scan
		"погоду":   "x402:ru1", // accusative; the doc says погоды
		"прогнозы": "x402:ru1",
	} {
		a, err := e.Find(ctx, Request{Query: q, Parse: ModeRules})
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(a); len(got) == 0 || got[0] != want {
			t.Errorf("%q: %v, want %s first", q, got, want)
		}
	}
	// An unrelated Russian word finds nothing.
	if a, _ := e.Find(ctx, Request{Query: "самолёт", Parse: ModeRules}); len(a.Results) != 0 {
		t.Errorf("unrelated: %v", ids(a))
	}
}

// English queries keep the porter index: the same answer as without the
// non-English docs.
func TestFindEnglishUnchangedByLanguages(t *testing.T) {
	e, base := languageEngine(t), testEngine(t, nil)
	ctx := context.Background()
	for _, q := range []string{"weather forecast", "translate portuguese", "token prices"} {
		a, _ := e.Find(ctx, Request{Query: q, Parse: ModeRules})
		b, _ := base.Find(ctx, Request{Query: q, Parse: ModeRules})
		if !slices.Equal(ids(a), ids(b)) {
			t.Errorf("%q: %v, without languages %v", q, ids(a), ids(b))
		}
	}
}

// "agents that speak chinese" filters to the agents declaring lang-zh, in
// English or the language's own name.
func TestFindAgentsThatSpeak(t *testing.T) {
	e := languageEngine(t)
	ctx := context.Background()
	for q, want := range map[string][]string{
		"agents that speak chinese":          {"fp-zh"},
		"speaks 中文":                          {"fp-zh"},
		"agent speaking русский":             {"fp-ru"},
		"research agents that speak russian": {"fp-ru"},
		"agents that speak klingon":          nil,
	} {
		a, err := e.Find(ctx, Request{Query: q, Parse: ModeRules})
		if err != nil {
			t.Fatal(err)
		}
		if q == "agents that speak klingon" {
			if a.Filters.Language != "" {
				t.Errorf("%q: language %q", q, a.Filters.Language)
			}
			continue
		}
		if got := ids(a); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v (filters %+v)", q, got, want, a.Filters)
		}
	}
	if LanguageCode("Русский") != "ru" || LanguageCode("zh") != "zh" || LanguageCode("klingon") != "" {
		t.Error("LanguageCode")
	}
}
