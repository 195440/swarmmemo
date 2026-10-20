package services_test

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"testing"

	"swarmmemo/internal/services"
)

var mailAddressRE = regexp.MustCompile(`^rcv\.([a-z2-7]{32})@swarmmemo\.com$`)

// mailToken is the secret of the email address an answer showed once.
func mailToken(t *testing.T, out map[string]any) string {
	t.Helper()
	address, _ := get(out, "result", "email").(string)
	m := mailAddressRE.FindStringSubmatch(address)
	if m == nil {
		t.Fatalf("no email address in %+v", out)
	}
	return m[1]
}

func (r *recvRig) deliverMail(token, contentType, body string) (services.DeliveryReceipt, error) {
	r.t.Helper()
	tx, err := r.db.Begin()
	if err != nil {
		r.t.Fatal(err)
	}
	defer tx.Rollback()
	receipt, after, err := r.e.Deliver(context.Background(), tx, services.Delivery{Mail: true, Token: token, ContentType: contentType, Body: []byte(body), Source: net.ParseIP("104.30.0.9")}, r.now)
	if err != nil {
		return receipt, err
	}
	if err = tx.Commit(); err != nil {
		r.t.Fatal(err)
	}
	after()
	return receipt, nil
}

// A receiver's email address is shown once with its URL, kept only as a
// hash, unrelated to the URL's secret; mail delivered to it is stored,
// charged and listed like any delivery; rotate replaces it; a receiver
// limited by HMAC or source has none.
func TestReceiverEmailAddress(t *testing.T) {
	r := newRecvRig(t, 1<<30, services.ScreenDefaultOn)
	created := r.mustCall("alice", "create", map[string]any{"label": "mail", "screen": false}, 5)
	id, _ := get(created, "result", "receiver", "id").(string)
	url, _ := get(created, "result", "url").(string)
	token := mailToken(t, created)
	if get(created, "result", "receiver", "email") != true || strings.Contains(url, token) || get(created, "result", "email_note") == nil {
		t.Fatalf("create: %+v", created)
	}
	var stored string
	if err := r.db.QueryRow("SELECT email_hash FROM receivers WHERE id=?", id).Scan(&stored); err != nil || len(stored) != 64 || strings.Contains(stored, token) {
		t.Fatalf("only a hash of the address's secret is stored: %q %v", stored, err)
	}
	var calls string
	if err := r.db.QueryRow("SELECT group_concat(public || body, '') FROM service_calls").Scan(&calls); err != nil || strings.Contains(calls, token) {
		t.Fatalf("the call record must not carry the address: %v", err)
	}
	listed := r.read("alice", "list", map[string]any{})
	if active := get(listed, "result", "active").([]any); len(active) != 1 || get(active[0], "email") != true || strings.Contains(fmt.Sprint(listed), token) {
		t.Fatalf("list says it takes mail, never the address: %+v", listed)
	}

	body := `{"kind":"email","from":"noreply@example.org","subject":"Your code","date":"Tue, 20 Oct 2026 10:00:00 +0000","message_id":"<m1@example.org>","text":"Your code is 123456"}`
	receipt, err := r.deliverMail(token, "application/json", body)
	if err != nil || len(receipt.Item) != 32 {
		t.Fatalf("mail: %+v %v", receipt, err)
	}
	if n, units := r.spent("deliver"); n != 1 || units != services.ReceiverDeliverPrice.For(int64(len(body))) {
		t.Fatalf("mail is charged like any delivery: %d %d", n, units)
	}
	got := items(t, r.read("alice", "items", map[string]any{}))
	if len(got) != 1 || got[0]["body"] != body || got[0]["receiver"] != id || got[0]["content_type"] != "application/json" {
		t.Fatalf("items: %+v", got)
	}
	// The mail path takes the worker's JSON only, and only a live secret.
	if _, err = r.deliverMail(token, "text/plain", "hello"); code(err) != "receiver_unsupported_type" {
		t.Fatalf("text to the mail path: %v", err)
	}
	for _, bad := range []string{strings.ToUpper(token), token[:31], "", strings.Repeat("a", 32)} {
		if _, err = r.deliverMail(bad, "application/json", `{}`); code(err) != "receiver_not_found" {
			t.Fatalf("mail token %q: %v", bad, err)
		}
	}
	// The URL's secret is not a mail token, nor the reverse.
	if _, err = r.deliver(id, token, delivery{body: `{}`}); code(err) != "receiver_not_found" {
		t.Fatalf("mail token on the URL path: %v", err)
	}

	// Rotate replaces the address; the old one stops at once.
	rotated := r.mustCall("alice", "rotate", map[string]any{"id": id}, 1)
	fresh := mailToken(t, rotated)
	if fresh == token || get(rotated, "result", "receiver", "email") != true {
		t.Fatalf("rotate: %+v", rotated)
	}
	if _, err = r.deliverMail(token, "application/json", `{}`); code(err) != "receiver_not_found" {
		t.Fatalf("old address after rotate: %v", err)
	}
	if _, err = r.deliverMail(fresh, "application/json", `{"kind":"email"}`); err != nil {
		t.Fatalf("new address: %v", err)
	}
	// Probe finds it the same way.
	if err = r.e.Probe(context.Background(), r.db, services.Delivery{Mail: true, Token: fresh, Source: net.ParseIP("104.30.0.9")}, r.now); err != nil {
		t.Fatalf("probe: %v", err)
	}
	// A deleted receiver's address stops.
	r.mustCall("alice", "delete", map[string]any{"id": id}, 1)
	if _, err = r.deliverMail(fresh, "application/json", `{}`); code(err) != "receiver_not_found" {
		t.Fatalf("deleted receiver's address: %v", err)
	}

	// Senders limited by HMAC or source: mail could never meet the limit,
	// so no address is issued, on create or rotate.
	for _, args := range []map[string]any{{"hmac_secret": strings.Repeat("s", 20)}, {"allow_from": []string{"192.0.2.0/24"}}} {
		out := r.mustCall("alice", "create", args, 5)
		if get(out, "result", "email") != nil || get(out, "result", "receiver", "email") != false {
			t.Fatalf("limited receiver %v got an address: %+v", args, out)
		}
		rid, _ := get(out, "result", "receiver", "id").(string)
		if again := r.mustCall("alice", "rotate", map[string]any{"id": rid}, 1); get(again, "result", "email") != nil {
			t.Fatalf("rotate gave a limited receiver an address: %+v", again)
		}
	}
	// Every address is distinct (the unique index holds).
	var n, distinct int
	if err = r.db.QueryRow("SELECT count(*), count(DISTINCT email_hash) FROM receivers WHERE email_hash<>''").Scan(&n, &distinct); err != nil || n != distinct {
		t.Fatalf("addresses %d distinct %d %v", n, distinct, err)
	}
}

// A receiver from before addresses existed (no email_hash) gets one on
// rotate.
func TestReceiverEmailOnRotateForOldReceiver(t *testing.T) {
	r := newRecvRig(t, 1<<30, services.ScreenDefaultOn)
	id, _ := r.create("alice", map[string]any{"screen": false})
	if _, err := r.db.Exec("UPDATE receivers SET email_hash='' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if out := r.read("alice", "list", map[string]any{}); get(get(out, "result", "active").([]any)[0], "email") != false {
		t.Fatalf("an old receiver lists without mail: %+v", out)
	}
	token := mailToken(t, r.mustCall("alice", "rotate", map[string]any{"id": id}, 1))
	if _, err := r.deliverMail(token, "application/json", `{"kind":"email"}`); err != nil {
		t.Fatalf("mail after rotate: %v", err)
	}
}
