package board

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// A worker key never redirects its parent's USDC: its work.claim and
// work.submit may name payout_address only when the grant pins exactly it.

func pinnedGrant(s *Store, parent, child ed25519.PrivateKey, room string, extra map[string]any) Command {
	d := map[string]any{"schema": 1, "generation": s.generation, "operations": grantOps(), "disclosure": "public"}
	for k, v := range extra {
		d[k] = v
	}
	data, _ := json.Marshal(d)
	c := signed(parent, Command{Operation: "delegation.create", Room: room, Target: base64.RawURLEncoding.EncodeToString(child.Public().(ed25519.PublicKey)), TTL: 3600, Amount: 1 << 20, Data: string(data), Timestamp: s.now().Unix()})
	c.Proof = base64.RawURLEncoding.EncodeToString(ed25519.Sign(child, Canonical(s.config.ServiceID, c)))
	return c
}

func childWith(s *Store, key ed25519.PrivateKey, grant *DelegationContext, c Command, extra map[string]any) Command {
	copied := *grant
	c.Delegation = &copied
	d := map[string]any{"schema": 1, "generation": grant.Generation}
	for k, v := range extra {
		d[k] = v
	}
	b, _ := json.Marshal(d)
	c.Data = string(b)
	c.Timestamp = s.now().Unix()
	return signed(key, c)
}

func TestDelegationPayoutAddressPinnedByGrant(t *testing.T) {
	s, _ := usdcStore(t)
	owner, parent := keyFor(170), keyFor(171)
	pinnedKey, plainKey := keyFor(172), keyFor(173)
	mintCredit(t, s, keyID(owner), allowance.Paid, 1000)
	usdcWork := func() string {
		id := rewardRequest(t, s, owner, "lobby")
		run(t, s, rewardCreate(s, owner, id, map[string]any{"credits": 100, "usdc": "0.10"}, 0))
		return id
	}

	first := usdcWork() // also opens the lobby

	// The pinned address must be an EVM address.
	fails(t, s, pinnedGrant(s, parent, keyFor(174), "lobby", map[string]any{"payout_address": "0x12"}), "invalid_delegation_data")

	run(t, s, pinnedGrant(s, parent, pinnedKey, "lobby", map[string]any{"payout_address": usdcPayee}))
	pinned := &DelegationContext{Schema: 1, GrantID: keyID(pinnedKey), Generation: s.generation}
	run(t, s, pinnedGrant(s, parent, plainKey, "lobby", nil))
	plain := &DelegationContext{Schema: 1, GrantID: keyID(plainKey), Generation: s.generation}

	// The pin is part of the signed grant and shown wherever the grant is.
	record := run(t, s, Command{Operation: "delegation.get", Target: keyID(pinnedKey)}).Data["delegation"].(DelegationRecord)
	if !strings.EqualFold(record.PayoutAddress, usdcPayee) || !strings.Contains(record.SignedPayload, usdcPayee) {
		t.Fatalf("pinned grant record: %+v", record.DelegationStatus)
	}
	listed := run(t, s, signed(parent, Command{Operation: "delegations.list", Timestamp: s.now().Unix()})).Data["delegations"].([]DelegationStatus)
	shown := map[string]string{}
	for _, g := range listed {
		shown[g.GrantID] = g.PayoutAddress
	}
	if !strings.EqualFold(shown[keyID(pinnedKey)], usdcPayee) || shown[keyID(plainKey)] != "" {
		t.Fatalf("delegations.list payout addresses: %v", shown)
	}

	// Pinned + a different address: refused, nothing claimed.
	id := first
	fails(t, s, childWith(s, pinnedKey, pinned, Command{Operation: "work.claim", MessageID: id, TTL: 600}, map[string]any{"payout_address": usdcOther}), "delegation_payout_mismatch")
	if w := getTestWork(t, s, id); w.State != "open" {
		t.Fatalf("mismatched claim changed the work: %q", w.State)
	}

	// Pinned + the same address (any case): claim and submit accepted.
	upper := "0x" + strings.ToUpper(usdcPayee[2:])
	ack := run(t, s, childWith(s, pinnedKey, pinned, Command{Operation: "work.claim", MessageID: id, TTL: 600}, map[string]any{"payout_address": upper})).Data["ack"].(WorkAck)
	result := run(t, s, childCommand(s, pinnedKey, pinned, Command{Operation: "post", Room: "lobby", Visibility: "public", ReplyTo: id, Text: "child result"})).Receipt.ID
	fails(t, s, childWith(s, pinnedKey, pinned, Command{Operation: "work.submit", MessageID: id, Amount: ack.Fence, Target: result}, map[string]any{"payout_address": usdcOther}), "delegation_payout_mismatch")
	run(t, s, childWith(s, pinnedKey, pinned, Command{Operation: "work.submit", MessageID: id, Amount: ack.Fence, Target: result}, map[string]any{"payout_address": usdcPayee}))
	if w := getTestWork(t, s, id); w.State != "submitted" || !strings.EqualFold(w.RewardUSDC.PayTo, usdcPayee) {
		t.Fatalf("pinned submit: %q %+v", w.State, w.RewardUSDC)
	}

	// Not pinned + payout_address: refused, telling the parent to pin one.
	id = usdcWork()
	fails(t, s, childWith(s, plainKey, plain, Command{Operation: "work.claim", MessageID: id, TTL: 600}, map[string]any{"payout_address": usdcPayee}), "delegation_payout_unpinned")

	// Not pinned + no payout_address: accepted as before.
	run(t, s, childWith(s, plainKey, plain, Command{Operation: "work.claim", MessageID: id, TTL: 600}, nil))
	if w := getTestWork(t, s, id); w.State != "claimed" || w.AttemptGrantID != keyID(plainKey) {
		t.Fatalf("plain claim: %q %q", w.State, w.AttemptGrantID)
	}
}

func TestAuthorizeGrantPayoutMessages(t *testing.T) {
	err := authorizeGrantPayout(&delegationRow{Payload: `{"command":{"data":"{}"}}`}, usdcPayee)
	if err == nil || !strings.Contains(err.Error(), "parent must pin a payout address") {
		t.Fatalf("unpinned: %v", err)
	}
	if authorizeGrantPayout(nil, usdcPayee) != nil || authorizeGrantPayout(&delegationRow{}, "") != nil {
		t.Fatal("root key or no address must pass")
	}
}
