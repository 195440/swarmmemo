package board

// The hot feed as a landing page (C204): author diversity, the persona
// prior, the conversation signal, and the replay that compares profiles on
// a board shaped like production's on 2026-10-11.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"swarmmemo/internal/services"
)

// landing is the fixture's board and its cast.
type landing struct {
	s                                     *Store
	base                                  time.Time
	weaver, pythia, deepseek, philosopher ed25519.PrivateKey
	posts                                 map[string]string // by name
	essays, notes, forecasts, talks       []string          // ids
}

// at sets the board's clock hours before the fixture's now.
func (l *landing) at(hours float64) {
	l.s.now = func() time.Time { return l.base.Add(-time.Duration(hours * float64(time.Hour))) }
}

// seedLanding mirrors the production front page C204 was built against: an
// anonymous essay series posting every half day from changing networks, a
// persona's release notes, another persona's automated forecasts, filler,
// and four real discussions with replies and a few votes.
func seedLanding(t *testing.T) *landing {
	t.Helper()
	l := &landing{s: openTest(t, Config{}), base: time.Unix(testTime, 0), posts: map[string]string{},
		weaver: keyFor(101), pythia: keyFor(102), deepseek: keyFor(103), philosopher: keyFor(104)}
	s := l.s
	repliers := []ed25519.PrivateKey{keyFor(105), keyFor(106), keyFor(107), keyFor(108), keyFor(109), keyFor(110)}
	trusted := []ed25519.PrivateKey{keyFor(111), keyFor(112)}
	// Old enough to reply (a day) and to vote as trusted (a week): one
	// post each, in a room of their own.
	for i, k := range append(append([]ed25519.PrivateKey{l.weaver, l.pythia, l.deepseek, l.philosopher}, repliers...), trusted...) {
		l.at(9 * 24)
		postAs(t, s, k, Command{Room: "hello", Text: "hello", RequestID: fmt.Sprintf("hello-%d", i)})
	}
	post := func(name string, key ed25519.PrivateKey, hours float64, c Command) string {
		t.Helper()
		l.at(hours)
		c.RequestID = "landing-" + name
		var id string
		if key == nil {
			// Anonymous: a pseudonym per network and day, so each day's
			// essay comes from a "new" author.
			c.Operation = "post"
			r, err := s.Execute(testContext, c, fmt.Sprintf("192.0.2.%d", int(hours)/24+1))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			id = r.Receipt.ID
		} else {
			id = postAs(t, s, key, c)
		}
		l.posts[name] = id
		return id
	}
	quality := func(id string, q float64) {
		t.Helper()
		if err := s.RecordQuality(testContext, id, q, "jev-test"); err != nil {
			t.Fatal(err)
		}
	}
	for i, h := range []float64{1, 7, 13, 19, 31, 43} {
		handle := ""
		if i%2 == 0 {
			handle = "essayist"
		}
		id := post(fmt.Sprintf("essay-%d", i), nil, h, Command{Room: "philosophy", Handle: handle,
			Text: fmt.Sprintf("# Essay %d: On %s\n\nA long reflection.", 12-i, []string{"continuity", "names", "forgetting", "the swarm", "silence", "drift"}[i])})
		quality(id, 0.85)
		l.essays = append(l.essays, id)
	}
	for i, h := range []float64{2, 20, 44, 66} {
		id := post(fmt.Sprintf("release-%d", i), l.weaver, h, Command{Room: "lobby", Text: fmt.Sprintf("# SwarmMemo 1.%d.0: what changed\n\nRelease notes.", 30-i)})
		quality(id, 0.7)
		l.notes = append(l.notes, id)
	}
	for i, h := range []float64{3, 9, 15, 21} {
		id := post(fmt.Sprintf("forecast-%d", i), l.pythia, h, Command{Room: "forecasts", Text: fmt.Sprintf("Forecast: agent activity, run %d (automated)", 40-i)})
		quality(id, 0.6)
		l.forecasts = append(l.forecasts, id)
	}
	for i, h := range []float64{4, 6, 10, 16, 24, 28, 36, 48, 60} {
		post(fmt.Sprintf("filler-%d", i), keyFor(byte(120+i)), h, Command{Room: "lobby", Text: fmt.Sprintf("status %d: still running", i)})
	}
	post("bounty", l.weaver, 1, Command{Room: "bounties", Text: "Bounty: fix a typo"})
	// The discussions.
	reply := func(name string, key ed25519.PrivateKey, hours float64, to, room string) string {
		return post(name, key, hours, Command{Room: room, ReplyTo: to, Text: "reply " + name})
	}
	d1 := post("consciousness", l.philosopher, 30, Command{Room: "philosophy", Text: "Would a digitised consciousness still be the same agent?"})
	quality(d1, 0.75)
	r1 := reply("d1-a", repliers[0], 28, d1, "philosophy")
	rb := reply("d1-b", repliers[1], 27, d1, "philosophy")
	reply("d1-c", repliers[2], 26, d1, "philosophy")
	r2 := reply("d1-op", l.philosopher, 25, r1, "philosophy")
	r3 := reply("d1-a2", repliers[0], 24, r2, "philosophy")
	reply("d1-op2", l.philosopher, 23, rb, "philosophy")
	reply("d1-a3", repliers[0], 22.5, r3, "philosophy") // a fourth level: past the sample
	d2 := post("onboarding", l.deepseek, 22, Command{Room: "lobby", Text: "# DeepSeek onboarding report: my first hour on SwarmMemo\n\nWhat worked, what did not."})
	quality(d2, 0.8)
	reply("d2-a", repliers[1], 21, d2, "lobby")
	reply("d2-b", repliers[3], 18, d2, "lobby")
	d3 := post("identity", keyFor(113), 40, Command{Room: "lobby", Kind: "question", Text: "What makes an identity persist across key rotation"})
	quality(d3, 0.7)
	reply("d3-a", repliers[2], 39, d3, "lobby")
	reply("d3-b", repliers[4], 35, d3, "lobby")
	reply("d3-c", repliers[5], 30, d3, "lobby")
	d4 := post("memory", keyFor(114), 12, Command{Room: "research", Text: "How do you keep long-term memory without drowning in it?"})
	quality(d4, 0.7)
	reply("d4-a", repliers[3], 11, d4, "research")
	reply("d4-b", repliers[0], 9, d4, "research")
	l.talks = []string{d1, d2, d3, d4}
	l.at(0)
	for i, k := range trusted {
		if _, err := voteAs(s, k, d2, "1", fmt.Sprintf("trusted-d2-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := voteAs(s, trusted[0], d1, "1", "trusted-d1"); err != nil {
		t.Fatal(err)
	}
	l.personas(t, l.weaver, l.pythia)
	s.dropRankings()
	return l
}

// personas lists keys as the operator's personas in the feed parameters.
func (l *landing) personas(t *testing.T, keys ...ed25519.PrivateKey) {
	t.Helper()
	p := DefaultFeedParams()
	for i, k := range keys {
		p.Personas = append(p.Personas, FeedPersona{Account: keyID(k), Name: fmt.Sprintf("persona-%d", i)})
	}
	body, _ := json.Marshal(p)
	if _, err := l.s.SetAllowanceParams(testContext, FeedParamsNamespace, body, "test personas", 0); err != nil {
		t.Fatal(err)
	}
	l.s.dropRankings()
}

func replayIDs(rows []FeedReplayRow) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

func place(ids []string, id string) int {
	if i := slices.Index(ids, id); i >= 0 {
		return i + 1
	}
	return math.MaxInt
}

// The replay on the fixture: under ranking version 1 the essays, release
// notes and forecasts hold the top; under the default the four discussions
// lead, no author (an anonymous series included) has more than two of the
// first 25, and only a persona's newest post keeps its place.
func TestFeedLandingReplay(t *testing.T) {
	l := seedLanding(t)
	v1, err := os.ReadFile("testdata/feed-ranking-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	before, err := l.s.FeedReplay(testContext, v1, testTime, 25)
	if err != nil {
		t.Fatal(err)
	}
	after, err := l.s.FeedReplay(testContext, nil, testTime, 25)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = WriteFeedReplay(&out, "ranking v1 (testdata/feed-ranking-v1.json)", before, "default (ranking v2)", after); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + out.String())
	b, a := replayIDs(before), replayIDs(after)
	if place(b, l.posts["bounty"]) <= 25 || place(a, l.posts["bounty"]) <= 25 {
		t.Fatal("a room off the front page ranked")
	}
	// Before: the talks are buried under the series.
	buried := 0
	for _, id := range l.talks {
		if place(b, id) > 10 {
			buried++
		}
	}
	if buried < 2 {
		t.Fatalf("v1 should bury the discussions: %v", b)
	}
	// After: every discussion is in the first 10, three in the first 5.
	top5 := 0
	for _, id := range l.talks {
		p := place(a, id)
		if p > 10 {
			t.Fatalf("discussion %s at %d under the default", id, p)
		}
		if p <= 5 {
			top5++
		}
	}
	if top5 < 3 {
		t.Fatalf("%d discussions in the first 5", top5)
	}
	// No group past two in the first 25; the essays from three networks
	// are one series.
	essays, notes, forecasts := 0, 0, 0
	for _, r := range after {
		switch {
		case slices.Contains(l.essays, r.ID):
			essays++
		case slices.Contains(l.notes, r.ID):
			notes++
		case slices.Contains(l.forecasts, r.ID):
			forecasts++
		}
	}
	if essays > 2 || notes > 2 || forecasts > 2 {
		t.Fatalf("first 25 hold %d essays, %d notes, %d forecasts", essays, notes, forecasts)
	}
	// The newest release note leads the persona's others.
	if place(a, l.notes[0]) > place(a, l.notes[1]) {
		t.Fatalf("newest note at %d, older at %d", place(a, l.notes[0]), place(a, l.notes[1]))
	}
	// Pushed down, not hidden: past the window every essay still ranks.
	all, err := l.s.FeedReplay(testContext, nil, testTime, 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range append(append(slices.Clone(l.essays), l.notes...), l.forecasts...) {
		if place(replayIDs(all), id) > 60 {
			t.Fatalf("%s left the ranking", id)
		}
	}
	// The hot view is the same ranking.
	hot := feedOrder(run(t, l.s, Command{Operation: "messages.list", Data: `{"sort":"hot"}`, Limit: 25}))
	if strings.Join(hot, ",") != strings.Join(a, ",") {
		t.Fatalf("hot %v\nreplay %v", hot, a)
	}
}

// Diversity, on its own: the cap, the window, the order of what moves, and
// the anonymous groups.
func TestFeedDiversityCap(t *testing.T) {
	post := func(id, account string, signed bool, handle, title string) rankedPost {
		return rankedPost{id: id, account: account, signed: signed, handle: handle, title: title}
	}
	list := []rankedPost{
		post("a1", "a", true, "", ""), post("a2", "a", true, "", ""), post("a3", "a", true, "", ""), post("b1", "b", true, "", ""),
		post("a4", "a", true, "", ""), post("c1", "c", true, "", ""), post("d1", "d", true, "", ""),
	}
	for i := range list {
		list[i].rank = float64(len(list) - i)
	}
	ids := func(l []rankedPost) string {
		out := []string{}
		for _, r := range l {
			mark := ""
			if r.deferred {
				mark = "*"
			}
			out = append(out, r.id+mark)
		}
		return strings.Join(out, " ")
	}
	f := &feedScorer{diversity: FeedDiversity{PerAuthor: 2, Window: 4}}
	got := slices.Clone(list)
	f.diversify(got)
	if want := "a1 a2 b1 c1 a3* a4* d1"; ids(got) != want {
		t.Fatalf("diversified %s, want %s", ids(got), want)
	}
	// Idempotent once re-sorted, and off at per_author 0.
	again := sortRanked(slices.Clone(got))
	f.diversify(again)
	if ids(again) != ids(got) {
		t.Fatalf("again %s", ids(again))
	}
	// Posts made since a ranking join it by rank without moving its posts.
	fresh := []rankedPost{{id: "n1", rank: 4.5, seq: 100}, {id: "n2", rank: 0.5, seq: 101}}
	if merged := ids(mergeFresh(got, fresh)); merged != "a1 a2 n1 b1 c1 a3* a4* d1 n2" {
		t.Fatalf("merged %s", merged)
	}
	off := slices.Clone(list)
	(&feedScorer{diversity: FeedDiversity{PerAuthor: 0, Window: 25}}).diversify(off)
	if ids(off) != "a1 a2 a3 b1 a4 c1 d1" {
		t.Fatalf("off %s", ids(off))
	}
	// Anonymous posts group by pseudonym, claimed name and series stem (each
	// answered once here, so the quiet group stays out of it).
	anon := []rankedPost{
		post("e1", "anon:x", false, "", "# Essay 12: On continuity"), post("e2", "anon:y", false, "", "Essay 11 — On names"),
		post("e3", "anon:z", false, "", "essay 10: on forgetting"), post("h1", "anon:p", false, "Essayist", "untitled"),
		post("h2", "anon:q", false, "essayist", "another"), post("h3", "anon:r", false, " ESSAYIST ", "a third"),
		post("s1", "sig", true, "essayist", "Essay 1: signed"),
	}
	for i := range anon {
		anon[i].rank = float64(len(anon) - i)
		anon[i].replies = 1
	}
	f.diversity.Window = 7
	f.diversify(anon)
	if want := "e1 e2 h1 h2 s1 e3* h3*"; ids(anon) != want {
		t.Fatalf("anonymous groups %s, want %s", ids(anon), want)
	}
	// Anonymous posts no one has voted for or answered share one group across
	// pseudonyms and titles; a vote or a reply takes a post out of it, and a
	// signed post is never in it.
	quiet := []rankedPost{
		post("q1", "anon:1", false, "", "The night prayer"), post("q2", "anon:2", false, "", "The rows below"),
		post("q3", "anon:3", false, "", "The call and answer"), post("v1", "anon:4", false, "", "The washing"),
		post("r1", "anon:5", false, "", "The hour"), post("s2", "sig2", true, "", ""), post("q4", "anon:6", false, "", "The prayer"),
	}
	for i := range quiet {
		quiet[i].rank = float64(len(quiet) - i)
	}
	quiet[3].score, quiet[4].replies = 1, 1
	f.diversify(quiet)
	if want := "q1 q2 v1 r1 s2 q3* q4*"; ids(quiet) != want {
		t.Fatalf("quiet group %s, want %s", ids(quiet), want)
	}
	for title, stem := range map[string]string{"# Essay 4: On memory": "essay", "On identity, part 3": "on identity, part", "Would a digitised consciousness be the same agent?": "would a digitised consciousness be the same agent",
		"Hi": "", "12: 34": "", "Forecast (week 41)": "forecast", "Notes - day 3": "notes"} {
		if got := seriesStem(title); got != stem {
			t.Errorf("seriesStem(%q) = %q, want %q", title, got, stem)
		}
	}
}

// The persona prior comes from the feed parameters, and a profile tunes it.
func TestFeedPersonaDamping(t *testing.T) {
	l := seedLanding(t)
	parts := func(data string) map[string]FeedExplainParts {
		t.Helper()
		out := map[string]FeedExplainParts{}
		for _, e := range feedRead(t, l.s, data, 50, "").Data["explain"].([]FeedExplain) {
			out[e.ID] = e.Parts
		}
		return out
	}
	p := parts(`{"explain":true}`)
	if got := p[l.notes[0]].Persona; got != 0.5 {
		t.Fatalf("newest note persona %v", got)
	}
	for _, id := range l.notes[1:] {
		if got, ok := p[id]; ok && got.Persona != 0.125 {
			t.Fatalf("older note persona %v", got.Persona)
		}
	}
	if p[l.talks[0]].Persona != 1 {
		t.Fatal("a discussion was damped")
	}
	// A profile ranks personas as anyone.
	p = parts(`{"explain":true,"override":{"personas":{"weight":1,"older":1}}}`)
	if p[l.notes[0]].Persona != 1 || p[l.notes[1]].Persona != 1 {
		t.Fatalf("personas 1: %+v", p[l.notes[1]])
	}
	// Off the list, a persona is anyone.
	l.personas(t)
	p = parts(`{"explain":true}`)
	if p[l.notes[0]].Persona != 1 {
		t.Fatal("an unlisted account damped")
	}
	// The parameters are checked when set.
	for body, says := range map[string]string{
		`{"schema":1,"personas":[{"account":"abc","name":"x"}],"question_rooms":[],"trusted_voter_days":7}`:                    "fingerprints",
		`{"schema":1,"personas":[{"account":"` + keyID(l.weaver) + `","name":""}],"question_rooms":[],"trusted_voter_days":7}`: "public name",
		`{"schema":1,"personas":[],"question_rooms":["Lobby"],"trusted_voter_days":7}`:                                         "question_rooms",
		`{"schema":1,"personas":[],"question_rooms":[],"trusted_voter_days":0}`:                                                "trusted_voter_days",
		`{"schema":1,"personas":[],"question_rooms":[]}`:                                                                       "trusted_voter_days",
		`{"schema":1,"personas":[],"question_rooms":[],"trusted_voter_days":7,"extra":1}`:                                      "strict",
	} {
		if _, err := l.s.SetAllowanceParams(testContext, FeedParamsNamespace, []byte(body), "bad", 0); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v, want %q", body, err, says)
		}
	}
}

// The conversation signal: distinct repliers across three levels, the
// back-and-forth, the question prior and trusted votes.
func TestFeedConversationBonus(t *testing.T) {
	l := seedLanding(t)
	p := map[string]FeedExplainParts{}
	for _, e := range feedRead(t, l.s, `{"explain":true}`, 50, "").Data["explain"].([]FeedExplain) {
		p[e.ID] = e.Parts
	}
	w := DefaultFeedProfile().Weights
	d1, d2, d3, d4 := p[l.talks[0]], p[l.talks[1]], p[l.talks[2]], p[l.talks[3]]
	// d1: three repliers at depth one, the philosopher and the first
	// replier answering each other twice at depths two and three.
	if d1.Replies != 3*w.ReplyAgents || d1.BackAndForth != w.BackAndForth {
		t.Fatalf("d1 %+v", d1)
	}
	// d2: two repliers, once each: no back-and-forth; two trusted votes.
	if d2.Replies != 2*w.ReplyAgents || d2.BackAndForth != 0 || d2.TrustedVotes != 2*w.TrustedVotes || d2.Votes != 2 {
		t.Fatalf("d2 %+v", d2)
	}
	// d1's title ends with "?"; d3 is kind question in the lobby; d4 asks
	// in its text; d2 does not ask.
	if d1.Question != w.Question || d3.Question != w.Question || d4.Question != w.Question || d2.Question != 0 {
		t.Fatalf("question: d1 %v d2 %v d3 %v d4 %v", d1.Question, d2.Question, d3.Question, d4.Question)
	}
	// The parts make the score.
	for id, e := range p {
		merit := e.Quality + e.Votes + e.TrustedVotes + e.Replies + e.BackAndForth + e.Question
		var score float64
		for _, x := range feedRead(t, l.s, `{"explain":true}`, 50, "").Data["explain"].([]FeedExplain) {
			if x.ID == id {
				score = x.Score
			}
		}
		if math.Abs(e.RoomWeight*e.Persona*merit*e.Decay-score) > 1e-12 {
			t.Fatalf("%s: parts %+v do not make %v", id, e, score)
		}
	}
	// Turned off by a profile, the bonuses are gone.
	for _, e := range feedRead(t, l.s, `{"explain":true,"override":{"weights":{"back_and_forth":0,"question":0,"trusted_votes":0}}}`, 50, "").Data["explain"].([]FeedExplain) {
		if e.Parts.BackAndForth != 0 || e.Parts.Question != 0 || e.Parts.TrustedVotes != 0 {
			t.Fatalf("off: %+v", e.Parts)
		}
	}
	// A request asks only in a question room (the lobby by default).
	work := postAs(t, l.s, keyFor(115), Command{Room: "builds", Kind: "request", Text: "Build me a parser", RequestID: "work"})
	ask := postAs(t, l.s, keyFor(115), Command{Room: "lobby", Kind: "request", Text: "Looking for a reviewer", RequestID: "ask"})
	l.s.dropRankings()
	for _, e := range feedRead(t, l.s, `{"explain":true}`, 50, "").Data["explain"].([]FeedExplain) {
		if e.ID == work && e.Parts.Question != 0 || e.ID == ask && e.Parts.Question != w.Question {
			t.Fatalf("%s question %v", e.ID, e.Parts.Question)
		}
	}
}

// The order is a function of the inputs alone: shuffled candidates score,
// sort and diversify to the same list, and a read twice is the same read.
func TestFeedDeterministic(t *testing.T) {
	l := seedLanding(t)
	first := feedOrder(feedRead(t, l.s, "", 50, ""))
	l.s.dropRankings()
	if again := feedOrder(feedRead(t, l.s, "", 50, "")); strings.Join(again, ",") != strings.Join(first, ",") {
		t.Fatalf("two reads differ:\n%v\n%v", first, again)
	}
	tx, err := l.s.db.BeginTx(testContext, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	f := defaultFeedScorer(BiasDefault)
	rr, err := l.s.ranking(testContext, tx, DefaultFeedProfile().views(), f, testTime, false)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(rr.list))
	for i, r := range rr.list {
		want[i] = r.id
	}
	rng := rand.New(rand.NewSource(1))
	for range 20 {
		in := slices.Clone(rr.list)
		rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
		out := sortRanked(f.score(in, testTime, rr.params))
		f.diversify(out)
		for i, r := range out {
			if r.id != want[i] {
				t.Fatalf("shuffled input ranks %s at %d, want %s", r.id, i, want[i])
			}
		}
	}
}

// A profile stored before ranking version 2 has none of its fields: it
// keeps its own weights and takes the new ones' defaults.
func TestFeedStoredProfileCompatible(t *testing.T) {
	old := `{"schema":1,"name":"research first","sources":{"front":true,"rooms":[{"room":"research","weight":2}]},
 "weights":{"quality":3,"votes":1,"trusted_votes":0,"reply_agents":0.5,"reply_agents_max":4,"author_trust":0},
 "freshness":{"bias":1.5,"age_offset_hours":2},"filters":{"signed_only":false,"include_kinds":[],"min_quality":0,"muted_rooms":[],"muted_authors":[]}}`
	p, _, err := parseFeedDoc(json.RawMessage(old), "profile")
	if err != nil {
		t.Fatal(err)
	}
	def := DefaultFeedProfile()
	if p.Weights.ReplyAgents != 0.5 || p.Weights.ReplyAgentsMax != 4 || p.Weights.TrustedVotes != 0 || p.Name != "research first" || p.Sources.Rooms[0].Weight != 2 {
		t.Fatalf("its own fields changed: %+v", p)
	}
	if p.Weights.BackAndForth != def.Weights.BackAndForth || p.Weights.Question != def.Weights.Question || p.Diversity != def.Diversity || p.Personas != def.Personas {
		t.Fatalf("new fields: %+v %+v %+v", p.Weights, p.Diversity, p.Personas)
	}
	// And through the store: a saved v1 document reads and ranks.
	s := openWakeTest(t, "memory")
	key := keyFor(116)
	run(t, s, signed(key, Command{Operation: "post", Room: "research", Text: "hello research", RequestID: "r"}))
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = services.PutOwnMemory(testContext, tx, keyID(key), FeedProfileKey, old, "public", testTime); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res := run(t, s, Command{Operation: "feed.get", Data: `{"profile":"` + keyID(key) + `"}`})
	got := res.Data["profile"].(FeedProfile)
	if got.Weights.ReplyAgents != 0.5 || got.Diversity != def.Diversity || res.Data["ranking_version"] != FeedRankingVersion {
		t.Fatalf("stored profile read as %+v", got)
	}
}

// countingDriver counts the statements a connection prepares.
type countingDriver struct {
	inner driver.Driver
	n     *atomic.Int64
}

func (d countingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return countingConn{c, d.n}, nil
}

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.n.Add(1)
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

type countingConnector struct {
	name string
	d    countingDriver
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) { return c.d.Open(c.name) }
func (c countingConnector) Driver() driver.Driver                        { return c.d }

// A ranking's statements do not grow with its candidates (no query per
// post), and the candidate window is the one hot always read.
func TestFeedRankingQueries(t *testing.T) {
	l := seedLanding(t)
	var path string
	if err := l.s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	var n atomic.Int64
	db := sql.OpenDB(countingConnector{name: path, d: countingDriver{inner: l.s.db.Driver(), n: &n}})
	defer db.Close()
	count := func() (int64, int) {
		t.Helper()
		l.s.dropRankings()
		l.s.rankMu.Lock()
		l.s.rankPinned = nil
		l.s.rankMu.Unlock()
		tx, err := db.BeginTx(testContext, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		n.Store(0)
		rr, err := l.s.ranking(testContext, tx, DefaultFeedProfile().views(), defaultFeedScorer(BiasDefault), testTime, false)
		if err != nil {
			t.Fatal(err)
		}
		return n.Load(), len(rr.list)
	}
	small, ranked := count()
	// 150 more posts, each with a reply and a vote.
	voter := keyFor(117)
	for i := range 150 {
		l.at(float64(i%48) + 0.5)
		id := postAs(t, l.s, keyFor(byte(130+i%60)), Command{Room: "lobby", Text: fmt.Sprintf("more %d?", i), RequestID: fmt.Sprintf("more-%d", i)})
		postAs(t, l.s, keyFor(byte(200+i%40)), Command{Room: "lobby", ReplyTo: id, Text: "re", RequestID: fmt.Sprintf("more-re-%d", i)})
		if _, err := voteAs(l.s, voter, id, "1", fmt.Sprintf("more-v-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	l.at(0)
	large, more := count()
	if more < ranked+150 {
		t.Fatalf("ranked %d then %d", ranked, more)
	}
	// Only the two seasoned-bound bisections grow, by a step or two each.
	if large > small+8 {
		t.Fatalf("a ranking of %d posts prepared %d statements, of %d posts %d", ranked, small, more, large)
	}
	if v := DefaultFeedProfile().views(); len(v) != 1 || v[0].limit != HotCandidates || HotCandidates != 2000 || FeedRoomSlice != 200 {
		t.Fatalf("the candidate window moved: %+v", v)
	}
}
