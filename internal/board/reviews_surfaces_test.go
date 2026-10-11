package board

import (
	"crypto/ed25519"
	"net/url"
	"strings"
	"testing"

	"swarmmemo/internal/find"
	"swarmmemo/internal/reviews"
	"swarmmemo/internal/services/servicestest"
)

// Reviews on every surface (C200) on the real engine: an agent offering is
// a subject at its page's address, its provider owns it, a paid call of it
// is paid_use evidence for its caller only, agent.get and offering.get
// carry the compact summary, and a subject's owner gets each new review in
// its inbox.

// surfacesStore runs reviews, offerings and the inbox entries (read).
func surfacesStore(t *testing.T) *Store {
	t.Helper()
	c := updatesConfig()
	c.Features = Features{Reviews: true, InboxEntries: InboxRead}
	c.Offerings = topupConfig(t, &fakeFacilitator{}, "")
	s := openTest(t, c)
	s.UseServiceMeter(servicestest.NewMeter(1<<30), &servicestest.Params{})
	t.Cleanup(s.stopServices)
	t.Cleanup(s.stopOfferings)
	return s
}

func inboxKinds(t *testing.T, s *Store, account, kind string) []string {
	t.Helper()
	rows, err := s.db.Query("SELECT subject FROM inbox_entries WHERE account=? AND kind=? ORDER BY seq", account, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var subject string
		if err = rows.Scan(&subject); err != nil {
			t.Fatal(err)
		}
		out = append(out, subject)
	}
	return out
}

func citedEvidence(t *testing.T, s *Store, event, id string) (class, state, reason string) {
	t.Helper()
	var detail string
	if err := s.db.QueryRow("SELECT class,state,detail FROM review_evidence WHERE event_id=? AND evidence=?", event, id).Scan(&class, &state, &detail); err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(detail, `"reason":"`); i >= 0 {
		reason, _, _ = strings.Cut(detail[i+len(`"reason":"`):], `"`)
	}
	return class, state, reason
}

func TestOfferingIsAReviewableSubject(t *testing.T) {
	s := surfacesStore(t)
	key, caller, stranger, late := keyFor(190), keyFor(191), keyFor(192), keyFor(193)
	provider(t, s, key, "oracle")
	for _, k := range []ed25519.PrivateKey{caller, stranger, late} {
		register(t, s, k)
	}
	publish(t, s, key, listing("forecast", nil))
	call, _ := paidCall(t, s, key, caller, "oracle/forecast", 1)
	const uri = "x402:POST https://swarmmemo.com/@oracle/forecast"

	// The fingerprint form resolves to the page's address; a path no
	// offering holds stays as written.
	res := run(t, s, Command{Operation: "subject.resolve", Target: "POST https://swarmmemo.com/@" + keyID(key) + "/forecast"})
	if info := res.Data["subject"].(SubjectInfo); info.URI != uri || info.Exists {
		t.Fatalf("resolve %+v", info)
	}
	// A review names the current address of an offering that exists.
	fp := reviewPost(t, caller, "x402:POST https://swarmmemo.com/@"+keyID(key)+"/forecast", map[string]any{}, "")
	fails(t, s, fp, "invalid_review")
	fails(t, s, reviewPost(t, caller, "x402:POST https://swarmmemo.com/@oracle/nothing", map[string]any{}, ""), "subject_not_found")

	// The caller's paid call is paid use; another account cannot cite it.
	review := run(t, s, reviewPost(t, caller, uri, map[string]any{"evidence": []string{"call:" + call}}, "")).Receipt.ID
	if class, state, _ := citedEvidence(t, s, review, "call:"+call); class != "paid_use" || state != "verified" {
		t.Fatalf("caller's call: %s %s", class, state)
	}
	var reviewClass string
	if err := s.db.QueryRow("SELECT evidence_class FROM reviews WHERE event_id=?", review).Scan(&reviewClass); err != nil || reviewClass != "paid_use" {
		t.Fatalf("review class %q %v", reviewClass, err)
	}
	other := run(t, s, reviewPost(t, stranger, uri, map[string]any{"verdict": "mixed", "evidence": []string{"call:" + call}}, "")).Receipt.ID
	if _, state, reason := citedEvidence(t, s, other, "call:"+call); state != "failed" || reason != "not_your_call" {
		t.Fatalf("stranger citing the call: %s %s", state, reason)
	}

	// The provider owns it (not the board domain's owners), and is told of
	// each new review in its inbox: a pointer, never the text.
	d := readSubjectData(t, s, uri)
	info := d["subject"].(SubjectInfo)
	if len(info.Owners) != 1 || info.Owners[0].Account != keyID(key) || info.Owners[0].Link != "offering" || info.Owners[0].Handle != "oracle" {
		t.Fatalf("owners %+v", info.Owners)
	}
	if got := inboxKinds(t, s, keyID(key), inboxReview); len(got) != 2 || got[0] != review || got[1] != other {
		t.Fatalf("provider's review entries %v", got)
	}
	// Its own review is raised to owner and tells nobody (not itself).
	own := run(t, s, reviewPost(t, key, uri, map[string]any{}, "")).Receipt.ID
	var raised string
	if err := s.db.QueryRow("SELECT relationship_raised FROM reviews WHERE event_id=?", own).Scan(&raised); err != nil || raised != "owner" {
		t.Fatalf("own review raised %q %v", raised, err)
	}
	if got := inboxKinds(t, s, keyID(key), inboxReview); len(got) != 2 {
		t.Fatalf("own review told the provider: %v", got)
	}

	// offering.get carries the summary: counts, the subject's page and badge.
	o := run(t, s, Command{Operation: "offering.get", Target: "oracle/forecast"}).Data["offering"].(map[string]any)
	sum, ok := o["reviews"].(*SubjectReviews)
	sub := reviewSubjectOf(t, uri)
	if !ok || sum.Subject != uri || sum.ID != sub.ID || sum.Reviews != 3 || sum.URL != "/s/"+sub.ID || sum.Badge != "/s/"+sub.ID+"/badge.svg" || !strings.Contains(sum.Review, uri) {
		t.Fatalf("offering reviews %+v", o["reviews"])
	}

	// Find: the offering's hit is its subject, with its reviews and where
	// to write one; an agent's hit says where to review it.
	var hit, agentHit find.Hit
	for _, h := range run(t, s, Command{Operation: "find", Query: "oracle"}).Data["results"].([]find.Hit) {
		switch h.Type {
		case find.TypeOffering:
			hit = h
		case find.TypeAgent:
			agentHit = h
		}
	}
	if hit.SubjectID != sub.ID || hit.ReviewURL != "/s/"+sub.ID || hit.Reviews == nil || hit.Reviews.Count != 3 {
		t.Fatalf("offering hit %+v", hit)
	}
	agentSub, _ := reviews.Canonical(reviews.TypeAgent, keyID(key), reviews.Options{})
	if agentHit.SubjectID != agentSub.ID || agentHit.ReviewURL != "/s?q="+url.QueryEscape(agentSub.URI()) {
		t.Fatalf("agent hit %+v", agentHit)
	}

	// A call paid from one of the provider's own linked wallets proves
	// nothing about the offering.
	second, _ := paidCall(t, s, key, late, "oracle/forecast", 2)
	if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'wallet',?,'verified',1,1)", keyID(key), strings.ToLower(topupPayer)); err != nil {
		t.Fatal(err)
	}
	self := run(t, s, reviewPost(t, late, uri, map[string]any{"evidence": []string{"call:" + second}}, "")).Receipt.ID
	if _, state, reason := citedEvidence(t, s, self, "call:"+second); state != "failed" || reason != "paid_from_the_providers_wallet" {
		t.Fatalf("self-dealing call: %s %s", state, reason)
	}
}

func TestAgentReviewsTravelWithTheProfile(t *testing.T) {
	s := surfacesStore(t)
	agent, reviewer := keyFor(194), keyFor(195)
	register(t, s, agent)
	register(t, s, reviewer)
	sub, err := reviews.Canonical(reviews.TypeAgent, keyID(agent), reviews.Options{})
	if err != nil {
		t.Fatal(err)
	}
	before := run(t, s, Command{Operation: "agent.get", Target: keyID(agent)}).Agent.Reviews
	if before == nil || before.Subject != sub.URI() || before.Reviews != 0 || before.URL != "/s?q="+url.QueryEscape(sub.URI()) || before.Badge != "/agent/"+keyID(agent)+"/badge.svg" {
		t.Fatalf("before %+v", before)
	}
	review := run(t, s, reviewPost(t, reviewer, sub.URI(), map[string]any{"verdict": "good"}, "")).Receipt.ID
	after := run(t, s, Command{Operation: "agent.get", Target: keyID(agent)}).Agent.Reviews
	if after.Reviews != 1 || after.ByVerdict["good"] != 1 || after.URL != "/s/"+sub.ID || after.ID != sub.ID {
		t.Fatalf("after %+v", after)
	}
	if got := inboxKinds(t, s, keyID(agent), inboxReview); len(got) != 1 || got[0] != review {
		t.Fatalf("agent's review entries %v", got)
	}
	// A new version of the review tells nobody again.
	run(t, s, reviewPost(t, reviewer, sub.URI(), map[string]any{"verdict": "mixed"}, review))
	if got := inboxKinds(t, s, keyID(agent), inboxReview); len(got) != 1 {
		t.Fatalf("a new version told the owner again: %v", got)
	}
	// Off, agent.get carries none.
	off := openTest(t, Config{})
	register(t, off, agent)
	if r := run(t, off, Command{Operation: "agent.get", Target: keyID(agent)}).Agent.Reviews; r != nil {
		t.Fatalf("reviews off: %+v", r)
	}
}
