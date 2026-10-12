package board

import (
	"context"
	"strings"
	"testing"

	"swarmmemo/internal/find"
	"swarmmemo/internal/services"
)

// Sign-up doors on the board (C228): an Agent Access Index review gives a
// site its door with the CC BY link (?src=juno-swarmmemo kept, a link off
// its host replaced by its home page), PAGE_NOT_FOUND gives none, and an
// agent's signup-key capability is a self-reported door; door=agent and
// door=human keep each side, and every hit says its source.
func TestFindDoorsOnTheBoard(t *testing.T) {
	s := reviewsStore(t)
	importer, bot := keyFor(81), keyFor(82)
	register(t, s, importer)
	stamp := strings.Repeat("ef", 32)
	if _, err := s.db.Exec("INSERT INTO notary_receipts(hash,time,account) VALUES(?,?,?)", stamp, testTime, keyID(importer)); err != nil {
		t.Fatal(err)
	}
	allowAccounts(t, s, ImportersParamsNamespace, importer)
	aai := func(gate, link string) map[string]any {
		return map[string]any{"relationship": "outside_source", "verdict": "mixed", "evidence": []string{"stamp:" + stamp},
			"source": map[string]any{"name": "Agent Access Index", "url": link, "terms": "CC BY 4.0; rendered-browser front-door test", "grade": gate, "verdict": "no"}}
	}
	const home = "https://agentaccess.lastminutedealshq.com"
	run(t, s, reviewPost(t, importer, "site:https://gatecaptcha.example.com/", aai("CAPTCHA_VISIBLE", home+"/p/gatecaptcha?src=juno-swarmmemo"), ""))
	run(t, s, reviewPost(t, importer, "site:https://gateform.example.com/", aai("EMAIL_FORM_RISK", home+"/p/gateform"), ""))
	run(t, s, reviewPost(t, importer, "site:https://gatelost.example.com/", aai("PAGE_NOT_FOUND", home+"/p/gatelost?src=juno-swarmmemo"), ""))
	run(t, s, reviewPost(t, importer, "site:https://gateelsewhere.example.com/", aai("NO_ACCOUNT", "https://evil.example.net/p/x"), ""))
	run(t, s, signed(bot, Command{Operation: "agent.register", Handle: "gatebot"}))
	run(t, s, signed(bot, Command{Operation: "agent.profile.publish", Data: `{"schema":1,"description":"I gate things.","capabilities":["gate","signup-key"],"availability":"available"}`}))

	search := func(data string) map[string]find.Hit {
		t.Helper()
		out := map[string]find.Hit{}
		for _, h := range run(t, s, Command{Operation: "find", Query: "gatecaptcha gateform gatelost gateelsewhere gatebot gate", Data: data}).Data["results"].([]find.Hit) {
			name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "https://"), "/")
			out[name] = h
		}
		return out
	}
	all := search(`{"schema":1,"parse":"off"}`)
	if d := all["gatecaptcha.example.com"].Door; d == nil || d.Value != "captcha" || d.By != "outside" || d.Source != "Agent Access Index" || d.URL != home+"/p/gatecaptcha?src=juno-swarmmemo" || d.Terms != "CC BY 4.0" || d.At == "" {
		t.Fatalf("captcha door %+v", d)
	}
	if d := all["gateform.example.com"].Door; d == nil || d.Value != "form" || d.URL != home+"/p/gateform?src=juno-swarmmemo" || d.Note != "terms put an agent account at risk" {
		t.Fatalf("form door %+v", d)
	}
	if d := all["gateelsewhere.example.com"].Door; d == nil || d.Value != "open" || d.URL != home+"/?src=juno-swarmmemo" {
		t.Fatalf("a link off the source's host: %+v", d)
	}
	if h, ok := all["gatelost.example.com"]; !ok || h.Door != nil {
		t.Fatalf("PAGE_NOT_FOUND sets no door: %+v", h)
	}
	if d := all["gatebot"].Door; d == nil || d.Value != "key" || d.By != "self" || d.URL != "" || d.Note != "capability signup-key" || !strings.Contains(all["gatebot"].Why, "door: key (self-reported)") {
		t.Fatalf("self door %+v", all["gatebot"])
	}
	agent := search(`{"schema":1,"parse":"off","door":"agent"}`)
	if len(agent) != 3 || agent["gateform.example.com"].ID == "" || agent["gatebot"].ID == "" || agent["gateelsewhere.example.com"].ID == "" {
		t.Fatalf("door=agent: %v", agent)
	}
	if human := search(`{"schema":1,"parse":"off","door":"human"}`); len(human) != 1 || human["gatecaptcha.example.com"].ID == "" {
		t.Fatalf("door=human: %v", human)
	}
	fails(t, s, Command{Operation: "find", Data: `{"schema":1,"door":"maybe"}`}, "invalid_find")
	// The query's own words: "sign up without a human" is door=agent.
	res := run(t, s, Command{Operation: "find", Query: "where can my agent sign up without a human"})
	if f := res.Data["filters"].(find.Filters); f.Door != "agent" || len(f.Types) != 0 {
		t.Fatalf("parsed %+v", f)
	}
	c := s.FindCapabilities()
	if c["doors"] == nil || !strings.Contains(c["params"].(map[string]string)["door"], "agent") {
		t.Fatalf("capabilities %v", c["doors"])
	}
}

// A paid endpoint whose path names signing up is an inferred wallet door
// (buildwithlocus's MPP sign-up); a path that does not (agentphone's
// agents) is none; SwarmMemo's own tools carry the key door it owns.
func TestFindInferredAndOwnDoors(t *testing.T) {
	locus := services.X402Listing{ID: "mpp-buildwithlocus-com-v1-auth-mpp-sign-up-0123456789abcdef", Kind: services.KindMPP, Method: "POST",
		URL: "https://mpp.buildwithlocus.com/v1/auth/mpp-sign-up", MaxPrice: -1, Decimals: 6, Protocols: []string{"mpp/tempo"}, Name: "build-with-locus · POST /v1/auth/mpp-sign-up"}
	phone := services.X402Listing{ID: "mpp-orthogonal-com-agentphone-v1-agents-0123456789abcdef", Kind: services.KindMPP, Method: "POST",
		URL: "https://mpp.orthogonal.com/agentphone/v1/agents", MaxPrice: -1, Decimals: 6, Protocols: []string{"mpp/tempo"}, Name: "agentphone via orthogonal · POST /v1/agents"}
	d := findX402Doc(locus, subjectQuality{})
	if d.Door != find.DoorWallet || d.DoorBy == nil || d.DoorBy.By != find.DoorInferred || d.DoorBy.Name != "a paid sign-up path" {
		t.Fatalf("locus %q %+v", d.Door, d.DoorBy)
	}
	if d = findX402Doc(phone, subjectQuality{}); d.Door != "" {
		t.Fatalf("agentphone %q %+v", d.Door, d.DoorBy)
	}
	for path, want := range map[string]bool{"/v1/signup": true, "/api/register": true, "/accounts/create-account": true, "/onboarding/start": true,
		"/v1/sign/in": false, "/registry/list": false, "/agents": false, "/v1/upsign": false} {
		if signupPath("https://x.example.com"+path) != want {
			t.Errorf("%s: want %v", path, want)
		}
	}
	// An outside door overrides the inferred one, with a note.
	q := subjectQuality{door: &outsideDoor{door: find.DoorCaptcha, src: find.DoorSource{By: find.DoorOutside, Name: "Agent Access Index", URL: "https://agentaccess.lastminutedealshq.com/?src=juno-swarmmemo"}}}
	docs := []find.Doc{findX402Doc(locus, subjectQuality{})}
	docs[0].SubjectID = "sub"
	applyOutsideDoors(docs, map[string]subjectQuality{"sub": q})
	if docs[0].Door != find.DoorCaptcha || !strings.Contains(docs[0].DoorBy.Note, "a paid sign-up path said wallet") {
		t.Fatalf("outside over inferred: %q %+v", docs[0].Door, docs[0].DoorBy)
	}
	var tool find.Doc
	setToolDoor(&tool, false)
	if tool.Door != find.DoorKey || tool.DoorBy.By != find.DoorMeasured {
		t.Fatalf("tool door %+v", tool.DoorBy)
	}
	var keyless find.Doc
	setToolDoor(&keyless, true)
	if keyless.Door != find.DoorOpen || keyless.DoorBy.By != find.DoorMeasured {
		t.Fatalf("keyless tool door %+v", keyless.DoorBy)
	}
	e := find.NewEngine(find.Config{Source: func(context.Context) ([]find.Doc, error) {
		return []find.Doc{findX402Doc(locus, subjectQuality{}), findX402Doc(phone, subjectQuality{})}, nil
	}})
	t.Cleanup(e.Close)
	a, err := e.Find(context.Background(), find.Request{Query: "sign up with a wallet", Parse: find.ModeRules})
	if err != nil || a.Filters.Door != find.DoorWallet || len(a.Results) != 1 || a.Results[0].ID != locus.ID {
		t.Fatalf("sign up with a wallet: %+v %+v (%v)", a.Filters, a.Results, err)
	}
}
