package find

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

var doorNow = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

// doorDocs are entries with doors from every provenance, and one without.
func doorDocs() []Doc {
	at := func(days int) int64 { return doorNow.Add(-time.Duration(days) * 24 * time.Hour).Unix() }
	docs := []Doc{
		{Type: TypeService, Kind: KindMPP, ID: "locus", Name: "Signup gateway locus", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeService, Kind: KindTool, ID: "own", Name: "Signup gateway own", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeSite, ID: "formsite", Name: "Signup gateway formsite", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeSite, ID: "captchasite", Name: "Signup gateway captchasite", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeAgent, ID: "selfagent", Name: "Signup gateway selfagent", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeSite, ID: "stale", Name: "Signup gateway stale", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeSite, ID: "nodoor", Name: "Signup gateway nodoor", Price: Unpriced, Quality: Quality{LabLower: -1}},
	}
	SetDoor(&docs[0], DoorWallet, DoorSource{By: DoorInferred, Name: "a paid sign-up path", URL: "/s/locus"})
	SetDoor(&docs[1], DoorKey, DoorSource{By: DoorMeasured, Name: "SwarmMemo", URL: "/protocol.md#agents"})
	SetDoor(&docs[2], DoorForm, DoorSource{By: DoorOutside, Name: "Agent Access Index", URL: "https://aai.example/p/x?src=juno-swarmmemo", Terms: "CC BY 4.0", At: at(12)})
	SetDoor(&docs[3], DoorCaptcha, DoorSource{By: DoorOutside, Name: "Agent Access Index", URL: "https://aai.example/p/y?src=juno-swarmmemo", At: at(12)})
	SetDoor(&docs[4], DoorKey, DoorSource{By: DoorSelf, Name: "the agent"})
	SetDoor(&docs[5], DoorOpen, DoorSource{By: DoorOutside, Name: "Agent Access Index", URL: "https://aai.example/p/z?src=juno-swarmmemo", At: at(200)})
	return docs
}

func doorEngine(t *testing.T, docs []Doc) *Engine {
	t.Helper()
	e := NewEngine(Config{Source: func(context.Context) ([]Doc, error) { return docs, nil }, Vocab: testVocab, Now: func() time.Time { return doorNow }})
	t.Cleanup(e.Close)
	return e
}

func doorFind(t *testing.T, e *Engine, q, door string) Answer {
	t.Helper()
	req := Request{Query: q, Parse: ModeOff}
	if door != "-" {
		req.Set, req.SetKeys = Filters{Door: door}, map[string]bool{"door": true}
	}
	a, err := e.Find(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The door filter keeps entries with that door, a group word keeps its
// doors, and entries without a door leave any door filter.
func TestDoorFilter(t *testing.T) {
	e := doorEngine(t, doorDocs())
	for door, want := range map[string][]string{
		"wallet":  {"locus"},
		"form":    {"formsite"},
		"agent":   {"formsite", "locus", "own", "selfagent", "stale"},
		"human":   {"captchasite"},
		"captcha": {"captchasite"},
		"closed":  nil,
		"":        {"captchasite", "formsite", "locus", "nodoor", "own", "selfagent", "stale"},
	} {
		got := ids(doorFind(t, e, "signup gateway", door))
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("door %q: %v, want %v", door, got, want)
		}
	}
	if _, msg := CheckFilters(Filters{Door: "unknown"}, testVocab); msg == "" {
		t.Fatal("a door outside the list was accepted")
	}
	if f, msg := CheckFilters(Filters{Door: " Agent "}, testVocab); msg != "" || f.Door != "agent" {
		t.Fatalf("checked %+v %q", f, msg)
	}
}

// Precedence: measured, outside, inferred, self. A lower source never
// overwrites a higher one; a disagreement stays visible in the note.
func TestDoorPrecedence(t *testing.T) {
	aai := DoorSource{By: DoorOutside, Name: "Agent Access Index", At: doorNow.Add(-24 * time.Hour).Unix()}
	var d Doc
	SetDoor(&d, DoorKey, DoorSource{By: DoorSelf, Name: "the agent"})
	SetDoor(&d, DoorWallet, DoorSource{By: DoorInferred, Name: "a paid sign-up path"})
	if d.Door != DoorWallet || d.DoorBy.By != DoorInferred || !strings.Contains(d.DoorBy.Note, "the agent said key") {
		t.Fatalf("inferred over self: %+v %+v", d.Door, d.DoorBy)
	}
	SetDoor(&d, DoorCaptcha, aai)
	SetDoor(&d, DoorHuman, DoorSource{By: DoorMeasured, Name: "SwarmMemo lab"})
	if d.Door != DoorHuman || d.DoorBy.By != DoorMeasured || !strings.Contains(d.DoorBy.Note, "Agent Access Index said captcha on 2026-10-11") {
		t.Fatalf("measured over outside: %+v %+v", d.Door, d.DoorBy)
	}
	// Self never beats measured, whatever order the sources come in.
	SetDoor(&d, DoorKey, DoorSource{By: DoorSelf, Name: "the agent"})
	SetDoor(&d, DoorOpen, aai)
	if d.Door != DoorHuman || d.DoorBy.By != DoorMeasured || !strings.Contains(d.DoorBy.Note, "the agent said key") {
		t.Fatalf("lower sources overwrote: %+v %+v", d.Door, d.DoorBy)
	}
	// Closed lists only.
	var e Doc
	SetDoor(&e, "unknown", aai)
	SetDoor(&e, DoorKey, DoorSource{By: "rumour"})
	if e.Door != "" || e.DoorBy != nil {
		t.Fatalf("outside the closed lists: %+v", e)
	}
}

// Under a door filter the source moves a result within QualityShift
// (measured above outside above inferred above self, a stale door lower);
// without one, the door never moves rank. Every door is on its hit with
// its source and date, and the why line says it.
func TestDoorRankAndHit(t *testing.T) {
	e := doorEngine(t, doorDocs())
	got := ids(doorFind(t, e, "signup gateway", "agent"))
	if want := []string{"own", "formsite", "locus", "stale", "selfagent"}; !slices.Equal(got, want) {
		t.Fatalf("door order %v, want %v", got, want)
	}
	plain := doorDocs()
	for i := range plain {
		plain[i].Door, plain[i].DoorBy = "", nil
	}
	if a, b := ids(doorFind(t, e, "signup gateway", "-")), ids(doorFind(t, doorEngine(t, plain), "signup gateway", "-")); !slices.Equal(a, b) {
		t.Fatalf("a door moved rank without a door filter: %v vs %v", a, b)
	}
	byID := map[string]Hit{}
	for _, h := range doorFind(t, e, "signup gateway", "-").Results {
		byID[h.ID] = h
	}
	if m := byID["formsite"].Door; m == nil || m.Value != "form" || m.By != "outside" || m.Source != "Agent Access Index" || !strings.Contains(m.URL, "src=juno-swarmmemo") || m.Terms != "CC BY 4.0" || m.At != "2026-09-30" || m.Stale {
		t.Fatalf("outside door %+v", m)
	}
	if m := byID["stale"].Door; m == nil || !m.Stale || !strings.Contains(byID["stale"].Why, "door: open (Agent Access Index, 2026-03-26, stale)") {
		t.Fatalf("stale door %+v %q", m, byID["stale"].Why)
	}
	if !strings.Contains(byID["locus"].Why, "door: wallet (inferred from a paid sign-up path)") || !strings.Contains(byID["selfagent"].Why, "door: key (self-reported)") ||
		!strings.Contains(byID["own"].Why, "door: key (measured by SwarmMemo)") {
		t.Fatalf("why lines %q %q %q", byID["locus"].Why, byID["selfagent"].Why, byID["own"].Why)
	}
	if byID["nodoor"].Door != nil {
		t.Fatal("no source, no door")
	}
	// The stale door stays shown, below the fresh outside one.
	if a := doorFind(t, e, "signup gateway", "agent"); !slices.Contains(ids(a), "stale") {
		t.Fatal("a stale door is never dropped")
	}
	// Explain names the term.
	a, _ := e.Find(context.Background(), Request{Query: "signup gateway", Parse: ModeOff, Explain: true, Set: Filters{Door: "agent"}, SetKeys: map[string]bool{"door": true}})
	if a.Results[0].Explain.Quality["door_source"] != 1.0 {
		t.Fatalf("explain %+v", a.Results[0].Explain.Quality)
	}
}

// The rules read a door only beside a sign-up word (or "no CAPTCHA"), and
// its words leave the text; "my agent" then names who signs up, not a type.
func TestDoorParseRules(t *testing.T) {
	for _, c := range []struct {
		q, door, text string
		types         []string
	}{
		{"where can my agent sign up without a human", "agent", "", nil},
		{"sign up with a wallet", "wallet", "", nil},
		{"services where I pay to sign up", "wallet", "", []string{TypeService}},
		{"register with a key", "key", "", nil},
		{"headless signup for email", "agent", "email", nil},
		{"sites with no captcha", "agent", "", []string{TypeSite, TypeDomain}},
		{"weather API paid with a wallet", "", "weather paid wallet", []string{TypeService}},
		{"autonomous agents for trading", "", "autonomous trading", []string{TypeAgent}},
		{"api key for weather", "", "key weather", []string{TypeService}},
		{"signup forms", "", "signup forms", nil},
	} {
		f, text := ParseRules(c.q, testVocab)
		if f.Door != c.door || text != c.text || !slices.Equal(f.Types, c.types) {
			t.Errorf("%q: door %q text %q types %v, want %q %q %v", c.q, f.Door, text, f.Types, c.door, c.text, c.types)
		}
	}
}

// The model may set a door from the closed list, only for a query about
// signing up; a door outside the list refuses the answer.
func TestDoorModel(t *testing.T) {
	ctx := context.Background()
	answer := func(door string) *fakeModel {
		return &fakeModel{answer: `{"text":"","type":[],"category":"","max_price":"","output":"","network":"","door":"` + door + `"}`}
	}
	e := doorEngine(t, doorDocs())
	e.intent = NewIntent(answer("wallet"), testVocab, nil)
	if a, _ := e.Find(ctx, Request{Query: "where can my agent sign up by paying"}); a.ParsedBy != "model" || a.Filters.Door != "wallet" || !slices.Equal(ids(a), []string{"locus"}) {
		t.Fatalf("model door: %+v %v", a.Filters, ids(a))
	}
	e.intent = NewIntent(answer("wallet"), testVocab, nil)
	if a, _ := e.Find(ctx, Request{Query: "weather API paid with a wallet"}); a.ParsedBy != "model" || a.Filters.Door != "" {
		t.Fatalf("a door without a sign-up word: %+v", a.Filters)
	}
	e.intent = NewIntent(answer("anyone"), testVocab, nil)
	if a, _ := e.Find(ctx, Request{Query: "where can my agent sign up"}); a.ParsedBy != "rules" || !strings.Contains(a.ParseNote, "model answer refused: door is") {
		t.Fatalf("bad door: %s %q", a.ParsedBy, a.ParseNote)
	}
	if s := intentSystem(testVocab); !strings.Contains(s, `"door": string`) || !strings.Contains(s, "signing up") {
		t.Fatal("the prompt names the door")
	}
}

// The relax rule keeps a door the query states, as it keeps a price: the
// type read from it gives way, the door does not.
func TestDoorSurvivesRelax(t *testing.T) {
	docs := []Doc{
		{Type: TypeSite, ID: "wxsite", Name: "Weather station", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeService, Kind: KindMPP, ID: "wxapi", Name: "Weather signup api", Price: Unpriced, Quality: Quality{LabLower: -1}},
		{Type: TypeService, Kind: KindMPP, ID: "wxnodoor", Name: "Weather feed api", Price: Unpriced, Quality: Quality{LabLower: -1}},
	}
	SetDoor(&docs[1], DoorWallet, DoorSource{By: DoorInferred, Name: "a paid sign-up path"})
	e := doorEngine(t, docs)
	a, err := e.Find(context.Background(), Request{Query: "weather sites where my agent can sign up without a human", Parse: ModeRules})
	if err != nil {
		t.Fatal(err)
	}
	if a.Filters.Door != "agent" || len(a.Filters.Types) != 0 || !strings.Contains(a.ParseNote, RelaxedNote) || !slices.Equal(ids(a), []string{"wxapi"}) {
		t.Fatalf("relaxed: %v filters %+v note %q", ids(a), a.Filters, a.ParseNote)
	}
}
