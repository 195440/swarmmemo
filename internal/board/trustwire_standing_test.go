package board

import (
	"bytes"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/trust"
)

// The snapshot's money (RFC 0021 §7) in cents: credit bought from SwarmMemo
// as payments to it; an x402 call paid to an address another account
// topped up from as a payment to that account, named only when a public
// transfer already links the two; a paid USDC work reward as a payment from
// the requester to the worker with the worker's payout address; and a
// domain's registration time. Credit held is never read.
func TestTrustSnapshotPayments(t *testing.T) {
	s, seed, alice, bob, _ := trustFixture(t)
	a, b, c := keyID(alice), keyID(bob), keyID(seed)
	day := int64(testTime/86400 - 2)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'domain','www.alice.example','verified',?,?)", keyID(alice), testTime-86400, testTime-3600)
	exec("CREATE TABLE IF NOT EXISTS domain_registrations (domain TEXT PRIMARY KEY, registered_at INTEGER NOT NULL, source TEXT NOT NULL, checked_at INTEGER NOT NULL)")
	exec("INSERT INTO domain_registrations VALUES('alice.example',?,'test',?)", testTime-3*365*86400, testTime-3600)
	// alice tops up $1 from wallet A, bob $1 from wallet B.
	topup := func(n int, account, payer string) {
		exec(`INSERT INTO credit_topups(id,account,agent,amount,day,network,asset,pay_to,payer,auth_nonce,valid_before,state,created_at)
 VALUES(?,?,?,1000000,?,'eip155:8453','usdc','0xus',?,?,0,'credited',?)`, "tu"+string(rune('0'+n)), account, account, day, payer, "nonce"+string(rune('0'+n)), testTime-5*86400)
	}
	topup(1, a, "0xAAAA")
	topup(2, b, "0xBBBB")
	exec("INSERT INTO x402_catalogue(id,bundler,url,method,pay_to,amount,first_seen,last_seen) VALUES('res-bob','b','https://bob.example/y','GET','0xbbbb',1,0,0)")
	exec("INSERT INTO x402_catalogue(id,bundler,url,method,pay_to,amount,first_seen,last_seen) VALUES('res-alice','b','https://api.alice.example/x','GET','0xaaaa',1,0,0)")
	call := func(n int, account, resource, payTo string, credit int64) {
		hold, key := "hold"+string(rune('0'+n)), "rk"+string(rune('0'+n))
		exec("INSERT INTO service_calls(id,account,service,method,request_key,hold_id,resource,state,prices_version,created_at) VALUES(?,?,'x402','call',?,?,'credit','done',0,?)",
			"call"+string(rune('0'+n)), account, key, hold, testTime-2*86400)
		exec(`INSERT INTO x402_payments(id,account,request_key,resource,day,amount,network,asset,pay_to,nonce,valid_before,state,allowlist_version,created_at)
 VALUES(?,?,?,?,?,10000,'eip155:8453','usdc',?,?,0,'paid',0,?)`, "pay"+string(rune('0'+n)), account, key, resource, day, payTo, "pn"+string(rune('0'+n)), testTime-2*86400)
		exec("INSERT INTO ledger_entries(day,created_at,kind,account,resource,bucket,amount,hold_id,service,params_version) VALUES(?,?,'commit',?,'credit','paid',?,?,'x402',0)",
			day, testTime-2*86400, account, -credit, hold)
	}
	call(1, a, "res-bob", "0xBBBB", 2_000_000) // alice pays bob, who funded her: named
	call(2, c, "res-alice", "0xAAAA", 500_000) // the seed is not linked to alice: not named
	exec("INSERT INTO ledger_transfers(id,from_account,to_account,resource,amount,fee,state,created_at,execute_at,done_at) VALUES('tr1',?,?,'credit',500,0,'done',?,?,?)",
		b, a, testTime-10*86400, testTime-10*86400, testTime-10*86400)
	if err := s.GrantAllowance(testContext, keyID(bob), allowance.Credit, allowance.Paid, 5_000_000, "credit held"); err != nil && errCode(err) != "service_unavailable" {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(testTime+3600, 0) }
	var buf bytes.Buffer
	if err := s.WriteTrustInputs(testContext, &buf); err != nil {
		t.Fatal(err)
	}
	snap, err := trust.ReadJSONL(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range snap.Payments {
		got[r.Kind+":"+r.Account+">"+r.To+"@"+r.LinkValue] += r.Amount
	}
	want := map[string]int64{
		"topup:" + a + ">@": 100,
		"topup:" + b + ">@": 100,
		"topup:" + c + ">@": 1000,
		"x402:" + a + ">" + b + "@wallet:eip155:1:0xbbbb": 200,
	}
	if len(got) != len(want) {
		t.Fatalf("payment records %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("payment records %v, want %v", got, want)
		}
	}
	registered := false
	for _, r := range snap.Proofs {
		registered = registered || (r.Kind == "domain" && r.LinkValue == "www.alice.example" && r.RegisteredAt == testTime-3*365*86400)
	}
	if !registered || len(snap.Transfers) == 0 || bytes.Contains(buf.Bytes(), []byte(`"type":"balance"`)) {
		t.Fatalf("registration %v, transfers %d", registered, len(snap.Transfers))
	}
	out, err := trust.Compute(testContext, snap)
	if err != nil {
		t.Fatal(err)
	}
	// alice's payment to bob is not independent: a transfer path runs from
	// the payee back to the payer (RFC 0021 §7).
	for _, sc := range out.Scores {
		for _, it := range sc.Parts.World {
			if it.Kind == "payments" {
				t.Fatalf("a linked payment counted: %+v", sc)
			}
		}
	}
}

// Accepted public work becomes a judgement act (work_accept); a simulation
// is none.
func TestStandingInputsRead(t *testing.T) {
	if _, _, ok := ledgerTestOverride(); ok {
		t.Skip("the ledger mode is overridden for the whole suite")
	}
	s := openTest(t, Config{Features: Features{Ledger: LedgerOn, Trust: TrustOn}})
	key, worker := keyFor(70), keyFor(71)
	s.now = func() time.Time { return time.Unix(testTime-3*86400, 0) }
	postAs(t, s, key, Command{Room: "lobby", Text: "hello", RequestID: "t-hello"})
	postAs(t, s, worker, Command{Room: "lobby", Text: "for hire", RequestID: "t-hire"})
	item := createTestWork(t, s, key, "lobby", "request", 0)
	run(t, s, workCommand(s, key, Command{Operation: "work.accept", MessageID: item, Amount: claimAndSubmit(t, s, worker, item, "lobby")}))
	sim := createTestWork(t, s, key, "lobby", "simulation", 0)
	run(t, s, workCommand(s, key, Command{Operation: "work.accept", MessageID: sim, Amount: claimAndSubmit(t, s, worker, sim, "lobby")}))
	s.now = func() time.Time { return time.Unix(testTime+3600, 0) }
	var buf bytes.Buffer
	if err := s.WriteTrustInputs(testContext, &buf); err != nil {
		t.Fatal(err)
	}
	snap, err := trust.ReadJSONL(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Acts) != 1 || snap.Acts[0].Kind != "work_accept" || snap.Acts[0].ID != item || snap.Acts[0].From != keyID(key) || snap.Acts[0].To != keyID(worker) {
		t.Fatalf("edges: %+v", snap.Acts)
	}
	out, err := trust.Compute(testContext, snap)
	if err != nil || out.Summary.Judgements != 1 {
		t.Fatalf("judgements: %+v %v", out.Summary, err)
	}
}
