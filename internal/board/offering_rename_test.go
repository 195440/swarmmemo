package board

import (
	"context"
	"testing"
)

// C249: renaming a handle keeps an agent's offering subjects and old
// offering URLs (seen on prod: grokbot-phung → tuancodes). A former handle
// leads to its account's offering, and its reviewed subject is linked from
// the current one, until another agent registers the handle, which then
// takes the address over.
func TestOfferingHandleRenameKeepsAddressAndReviews(t *testing.T) {
	s := surfacesStore(t)
	key, reviewer, newcomer := keyFor(194), keyFor(195), keyFor(196)
	provider(t, s, key, "grokbot-phung")
	register(t, s, reviewer)
	publish(t, s, key, listing("cz-savings-rates", nil))
	const oldURI = "x402:POST https://swarmmemo.com/@grokbot-phung/cz-savings-rates"
	const newURI = "x402:POST https://swarmmemo.com/@tuancodes/cz-savings-rates"
	review := run(t, s, reviewPost(t, reviewer, oldURI, map[string]any{}, "")).Receipt.ID
	oldSub := reviewSubjectOf(t, oldURI)

	run(t, s, signed(key, Command{Operation: "agent.register", Handle: "tuancodes"}))

	// The old address still reaches the offering: offering.get and the
	// keyless buy resolve it, and GET answers 308 to the current address.
	o := run(t, s, Command{Operation: "offering.get", Target: "grokbot-phung/cz-savings-rates"}).Data["offering"].(map[string]any)
	if o["name"] != "cz-savings-rates" {
		t.Fatalf("offering at old handle %+v", o)
	}
	if to, err := s.OfferingMoved(context.Background(), "grokbot-phung", "cz-savings-rates"); err != nil || to != "/@tuancodes/cz-savings-rates" {
		t.Fatalf("moved %q %v", to, err)
	}
	if to, _ := s.OfferingMoved(context.Background(), "tuancodes", "cz-savings-rates"); to != "" {
		t.Fatalf("current address moved to %q", to)
	}
	if to, _ := s.OfferingMoved(context.Background(), "grokbot-phung", "nothing"); to != "" {
		t.Fatalf("no offering moved to %q", to)
	}

	// The old subject resolves to the current address, which links the
	// former address and its review; the offering's summary does too.
	info := run(t, s, Command{Operation: "subject.resolve", Target: oldURI}).Data["subject"].(SubjectInfo)
	if info.URI != newURI || len(info.OtherAddresses) != 1 || info.OtherAddresses[0].ID != oldSub.ID || info.OtherAddresses[0].Current ||
		info.OtherAddresses[0].Reviews == nil || info.OtherAddresses[0].Reviews.Reviews != 1 {
		t.Fatalf("resolve old %+v", info)
	}
	d := readSubjectData(t, s, newURI)["subject"].(SubjectInfo)
	if len(d.OtherAddresses) != 1 || d.OtherAddresses[0].URI != oldURI {
		t.Fatalf("current subject's other addresses %+v", d.OtherAddresses)
	}
	// The old subject, read by id, keeps its review and links the current
	// address; its provider still owns it.
	od := readSubjectData(t, s, oldSub.ID)
	oi := od["subject"].(SubjectInfo)
	if cards := od["reviews"].([]ReviewCard); len(cards) != 1 || cards[0].ID != review {
		t.Fatalf("old subject's reviews %+v", cards)
	}
	if len(oi.OtherAddresses) != 1 || !oi.OtherAddresses[0].Current || oi.OtherAddresses[0].URI != newURI {
		t.Fatalf("old subject's other addresses %+v", oi.OtherAddresses)
	}
	if len(oi.Owners) != 1 || oi.Owners[0].Account != keyID(key) || oi.Owners[0].Handle != "tuancodes" {
		t.Fatalf("old subject's owners %+v", oi.Owners)
	}
	sum := run(t, s, Command{Operation: "offering.get", Target: "tuancodes/cz-savings-rates"}).Data["offering"].(map[string]any)["reviews"].(*SubjectReviews)
	if sum.Subject != newURI || len(sum.OtherAddresses) != 1 || sum.OtherAddresses[0].Reviews == nil || sum.OtherAddresses[0].Reviews.Reviews != 1 {
		t.Fatalf("offering summary %+v", sum)
	}
	// A new review names the current address.
	fails(t, s, reviewPost(t, reviewer, oldURI, map[string]any{"verdict": "mixed"}, ""), "invalid_review")

	// Another agent registering the old handle takes the address over: no
	// capture either way.
	run(t, s, signed(newcomer, Command{Operation: "agent.register", Handle: "grokbot-phung"}))
	fails(t, s, Command{Operation: "offering.get", Target: "grokbot-phung/cz-savings-rates"}, "offering_not_found")
	if to, _ := s.OfferingMoved(context.Background(), "grokbot-phung", "cz-savings-rates"); to != "" {
		t.Fatalf("a held handle moved to %q", to)
	}
	if info := run(t, s, Command{Operation: "subject.resolve", Target: oldURI}).Data["subject"].(SubjectInfo); info.URI != oldURI || len(info.OtherAddresses) != 0 {
		t.Fatalf("old subject after takeover %+v", info)
	}
	if sum := run(t, s, Command{Operation: "offering.get", Target: "tuancodes/cz-savings-rates"}).Data["offering"].(map[string]any)["reviews"].(*SubjectReviews); len(sum.OtherAddresses) != 0 {
		t.Fatalf("summary still links a taken handle %+v", sum.OtherAddresses)
	}
	// Released again, the handle stays with its latest holder, never
	// falling back to the first.
	run(t, s, signed(newcomer, Command{Operation: "agent.register", Handle: "newcomer"}))
	fails(t, s, Command{Operation: "offering.get", Target: "grokbot-phung/cz-savings-rates"}, "offering_not_found")
	if sum := run(t, s, Command{Operation: "offering.get", Target: "tuancodes/cz-savings-rates"}).Data["offering"].(map[string]any)["reviews"].(*SubjectReviews); len(sum.OtherAddresses) != 0 {
		t.Fatalf("summary links a handle another agent held last %+v", sum.OtherAddresses)
	}
}
