package board

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/trust"
)

// The trust layer says what trust.get says: after a real run, each shown
// agent's standing is its breakdown's history, and its backing edges are
// its breakdown's backers, by the backer root's shown agent.
func TestTrustLayerMatchesTrustGet(t *testing.T) {
	s, seed, alice, _, _ := trustFixture(t)
	l, err := s.TrustLayer(testContext, 0, 0)
	if err != nil || len(l.Runs) != 0 || l.Nodes == nil || l.Edges == nil || l.Sources == nil || l.Limits.Runs != TrustLayerRunsDefault || l.Limits.Nodes != TrustLayerNodesDefault {
		t.Fatalf("before a run: %+v %v", l, err)
	}
	sum, err := s.RunTrust(testContext)
	if err != nil || sum.State != "done" {
		t.Fatalf("run: %+v %v", sum, err)
	}
	s.trust.graph.layers = nil // the cache holds the empty layer read above
	l, err = s.TrustLayer(testContext, 5, 50)
	if err != nil || l.Mode != "on" || len(l.Runs) != 1 || l.Runs[0].Run != sum.ID || l.Runs[0].Agents == 0 || l.Unit != "usd_cent" {
		t.Fatalf("layer: %+v %v", l, err)
	}
	if len(l.Nodes) == 0 {
		t.Fatal("no agent shown")
	}
	byID := map[string]TrustLayerNode{}
	for _, n := range l.Nodes {
		byID[n.ID] = n
		if len(n.Standing) != len(l.Runs) || n.BandName != BandName(n.Band) {
			t.Fatalf("node %+v", n)
		}
	}
	for _, key := range []string{keyID(seed), keyID(alice)} {
		n, ok := byID[key]
		if !ok {
			t.Fatalf("%s not shown: %+v", key, l.Nodes)
		}
		data := trustGet(t, s, key)
		hist := data["history"].([]trust.AccountPoint)
		if len(hist) != 1 || hist[0].StandingCents != n.Standing[0] {
			t.Fatalf("%s: layer %v, trust.get history %+v", key, n.Standing, hist)
		}
		if got := standingOf(t, data).StandingCents; got != n.Standing[0] {
			t.Fatalf("%s: layer %v, trust.get standing %d", key, n.Standing, got)
		}
		// Backing edges into it are its backers, by root.
		backers := data["backers"].([]trust.FlowPart)
		want := map[string]int64{}
		for _, b := range backers {
			if b.Cents > 0 {
				want[b.Root] += b.Cents
			}
		}
		got := map[string]int64{}
		for _, e := range l.Edges {
			if e.Kind == "backing" && e.To == key {
				got[e.From] += e.Cents[0]
			}
		}
		for root, c := range want {
			if got[root] != c { // the fixture's roots are the accounts themselves
				t.Fatalf("%s: backers %+v, layer edges %+v", key, backers, got)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: backers %+v, layer edges %+v", key, backers, got)
		}
		// World edges into it are its own costs by kind.
		world := map[string]int64{}
		for _, it := range data["world"].([]trust.WorldItem) {
			if it.Cents > 0 {
				world["world:"+trustWorldKind(it)] += it.Cents
			}
		}
		gotWorld := map[string]int64{}
		for _, e := range l.Edges {
			if e.Kind == "world" && e.To == key {
				gotWorld[e.From] += e.Cents[0]
			}
		}
		if len(world) != len(gotWorld) {
			t.Fatalf("%s: world %+v, layer %+v", key, world, gotWorld)
		}
		for k, c := range world {
			if gotWorld[k] != c {
				t.Fatalf("%s: world %+v, layer %+v", key, world, gotWorld)
			}
		}
	}
	if len(l.Sources) == 0 || l.Sources[0].ID != "world:"+l.Sources[0].Kind || l.Sources[0].Label == "" {
		t.Fatalf("sources: %+v", l.Sources)
	}
	// Some backing in the fixture: the seed's money above its cap flows to alice.
	backing := 0
	for _, e := range l.Edges {
		if e.Kind == "backing" {
			backing++
		}
	}
	if backing == 0 {
		t.Fatalf("no backing edge: %+v", l.Edges)
	}

	// Cached, then read again after the TTL; with trust off there is none.
	if _, err := s.db.Exec("DELETE FROM trust_scores"); err != nil {
		t.Fatal(err)
	}
	if l2, _ := s.TrustLayer(testContext, 5, 50); len(l2.Nodes) != len(l.Nodes) {
		t.Fatal("not cached")
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(trustLayerTTL * time.Second) }
	if l2, _ := s.TrustLayer(testContext, 5, 50); len(l2.Nodes) != 0 {
		t.Fatal("not read again after the TTL")
	}
	off := openTest(t, Config{})
	if _, err := off.TrustLayer(testContext, 1, 1); err == nil {
		t.Fatal("trust off serves a layer")
	}
}

// Shaping, caps and privacy on hand-written run outputs: runs oldest first,
// standing per run, backing edges by the backer root's shown agent, world
// edges by kind; an account that is not public is never named, not as a
// node, not as a backer; a root label (a domain, a wallet) never appears;
// the caps cut and count.
func TestTrustLayerShapingCapsPrivacy(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustOn}})
	a, b, c, d := keyFor(81), keyFor(82), keyFor(83), keyFor(84)
	for i, k := range []ed25519.PrivateKey{a, b, c, d} {
		postAs(t, s, k, Command{Room: "lobby", Text: "here " + string(rune('a'+i)), RequestID: "tl-" + string(rune('a'+i))})
	}
	A, B, C, D := keyID(a), keyID(b), keyID(c), keyID(d)
	hidden := keyID(keyFor(85))
	if _, err := s.db.Exec("INSERT INTO identities(id,public_key,account,created_at,last_seen) VALUES(?,?,?,?,?)", hidden, "pk-hidden", hidden, testTime, testTime); err != nil {
		t.Fatal(err)
	}
	// C and D share one root, labelled by a domain; the hidden account is backed and backs.
	root := map[string]string{A: A, B: B, C: "domain:secret-example.org", D: "domain:secret-example.org", hidden: hidden}
	type row struct {
		account string
		cents   int64
		parts   trust.Parts
	}
	world := func(items ...trust.WorldItem) []trust.WorldItem { return items }
	runs := []struct {
		id   int64
		rows []row
	}{
		{1, []row{
			{A, 900, trust.Parts{World: world(trust.WorldItem{Kind: "money", Source: "paid_to_swarmmemo", Root: "money:" + A, Cents: 900})}},
			{B, 100, trust.Parts{Backers: []trust.FlowPart{{Root: A, Cents: 100}}}},
		}},
		{2, []row{
			{A, 1000, trust.Parts{World: world(trust.WorldItem{Kind: "money", Source: "paid_to_swarmmemo", Root: "money:" + A, Cents: 1000},
				trust.WorldItem{Kind: "proof", Source: "wallet", Root: "wallet:0xsecretwallet", Cents: 50})}},
			{B, 200, trust.Parts{Backers: []trust.FlowPart{{Root: A, Cents: 150}, {Root: hidden, Cents: 40}}, Penalty: &trust.PenaltyState{Role: "member"}}},
			{C, 300, trust.Parts{World: world(trust.WorldItem{Kind: "proof", Source: "domain", Root: "domain:secret-example.org", Cents: 300}),
				Backers: []trust.FlowPart{{Root: B, Cents: 20}}}},
			{D, 120, trust.Parts{World: world(trust.WorldItem{Kind: "proof", Source: "package", Root: "package:secret", Cents: 120}),
				Backers: []trust.FlowPart{{Root: B, Cents: 20}}}}, // the same root's list, drawn once
			{hidden, 5000, trust.Parts{Backers: []trust.FlowPart{{Root: A, Cents: 70}}}},
		}},
	}
	for _, r := range runs {
		if _, err := s.db.Exec("INSERT INTO trust_runs(id,as_of,params_version,state,inputs,capture_bound,started_at,finished_at) VALUES(?,?,?,'done','{}','{}',?,?)",
			r.id, testTime+r.id*86400, trust.UnifiedVersion, testTime, testTime); err != nil {
			t.Fatal(err)
		}
		for _, x := range r.rows {
			parts, _ := json.Marshal(x.parts)
			band := int64(allowance.BandSigned)
			if x.cents >= 750 {
				band = int64(allowance.BandTrusted)
			}
			if _, err := s.db.Exec("INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(?,?,?,0,0,0,0,?,?,0,?)",
				r.id, x.account, root[x.account], x.cents, band, string(parts)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An aborted run is not a run of the layer.
	if _, err := s.db.Exec("INSERT INTO trust_runs(id,as_of,params_version,state,inputs,started_at) VALUES(3,?,?,'aborted','{}',?)", testTime+3*86400, trust.UnifiedVersion, testTime); err != nil {
		t.Fatal(err)
	}

	var l *TrustLayer
	read := func(runs, nodes, edges int) {
		t.Helper()
		if err := (trustDB{s}).Read(testContext, func(q allowance.Querier) (err error) {
			l, err = buildTrustLayer(testContext, q, runs, nodes, edges)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	read(30, 200, 600)
	if len(l.Runs) != 2 || l.Runs[0].Run != 1 || l.Runs[1].Run != 2 || l.Runs[0].Agents != 2 || l.Runs[1].Agents != 4 {
		t.Fatalf("runs: %+v", l.Runs)
	}
	var ids []string
	nodes := map[string]TrustLayerNode{}
	for _, n := range l.Nodes {
		ids = append(ids, n.ID)
		nodes[n.ID] = n
	}
	if strings.Join(ids, ",") != strings.Join([]string{A, C, B, D}, ",") {
		t.Fatalf("nodes not by highest standing: %v", ids)
	}
	if got := nodes[A].Standing; got[0] != 900 || got[1] != 1000 || nodes[A].BandName != "trusted" {
		t.Fatalf("A: %+v", nodes[A])
	}
	if got := nodes[C].Standing; got[0] != 0 || got[1] != 300 {
		t.Fatalf("C absent from run 1 reads 0: %+v", nodes[C])
	}
	if !nodes[B].Penalised || nodes[A].Penalised {
		t.Fatalf("penalty from the newest run: %+v %+v", nodes[B], nodes[A])
	}
	edges := map[string][]int64{}
	for _, e := range l.Edges {
		edges[e.Kind+":"+e.From+">"+e.To] = e.Cents
	}
	want := map[string][]int64{
		"backing:" + A + ">" + B:  {100, 150},
		"backing:" + B + ">" + C:  {0, 20},
		"world:world:money>" + A:  {900, 1000},
		"world:world:wallet>" + A: {0, 50},
		"world:world:domain>" + C: {0, 300},
		"world:world:proof>" + D:  {0, 120},
	}
	if len(edges) != len(want) {
		t.Fatalf("edges: %+v", edges)
	}
	for k, v := range want {
		if got := edges[k]; len(got) != 2 || got[0] != v[0] || got[1] != v[1] {
			t.Fatalf("edge %s: %v, want %v (all %+v)", k, got, v, edges)
		}
	}
	if l.Edges[0].Kind != "backing" || l.Edges[0].From != A {
		t.Fatalf("backing edges first, heaviest first: %+v", l.Edges)
	}
	kinds := []string{}
	for _, src := range l.Sources {
		kinds = append(kinds, src.Kind)
	}
	if strings.Join(kinds, ",") != "domain,wallet,money,proof" || l.Sources[2].Cents[1] != 1000 || l.Sources[2].Label != "Money paid" {
		t.Fatalf("sources: %+v", l.Sources)
	}
	if l.Truncated.Nodes != 0 || l.Truncated.Edges != 1 { // hidden→B: the hidden account is not shown
		t.Fatalf("truncated: %+v", l.Truncated)
	}
	body, _ := json.Marshal(l)
	for _, secret := range []string{hidden, "secret-example", "0xsecretwallet", "package:secret", "money:"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("the layer names %q: %s", secret, body)
		}
	}

	// Caps: one run, three agents, one backing edge.
	read(1, 3, 1)
	if len(l.Runs) != 1 || l.Runs[0].Run != 2 || len(l.Nodes) != 3 || l.Truncated.Nodes != 1 || len(l.Nodes[0].Standing) != 1 {
		t.Fatalf("capped: %+v %+v", l.Runs, l.Nodes)
	}
	backing := 0
	for _, e := range l.Edges {
		if e.Kind == "backing" {
			backing++
		}
		if len(e.Cents) != 1 {
			t.Fatalf("edge per run: %+v", e)
		}
	}
	if backing != 1 || l.Truncated.Edges != 2 || l.Edges[0].From != A { // A→B kept; B→C cut; hidden→B unshown
		t.Fatalf("edge cap: %d backing, %+v", backing, l.Truncated)
	}
}
