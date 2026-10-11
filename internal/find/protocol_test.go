package find

import (
	"context"
	"slices"
	"testing"
)

// The protocol filter keeps the docs that take a protocol of the family
// (mpp/tempo and mpp/evm are mpp), and drops the ones without protocols;
// each hit carries its protocols and sibling subjects as given.
func TestProtocolFilter(t *testing.T) {
	docs := []Doc{
		{Type: TypeService, Kind: KindX402, ID: "both", Name: "Weather both", Category: "weather", Price: 1000, Protocols: []string{"x402", "mpp/tempo"},
			Also: []Also{{Type: "mpp", SubjectID: "sib", ReviewURL: "/s/sib", Reviews: &Reviews{Count: 2, Word: "mostly good"}}}, Quality: Quality{LabLower: -1}},
		{Type: TypeService, Kind: KindX402, ID: "x402only", Name: "Weather x402", Category: "weather", Price: 1000, Protocols: []string{"x402"}, Quality: Quality{LabLower: -1}},
		{Type: TypeService, Kind: KindMPP, ID: "mpponly", Name: "Weather mpp", Category: "weather", Price: Unpriced, Network: "tempo", Protocols: []string{"mpp/tempo", "mpp/stripe"}, Quality: Quality{LabLower: -1}},
		{Type: TypeAgent, ID: "agent", Name: "Weather agent", Price: Unpriced, Quality: Quality{LabLower: -1}},
	}
	e := NewEngine(Config{Source: func(context.Context) ([]Doc, error) { return docs, nil }, Vocab: testVocab})
	t.Cleanup(e.Close)
	for _, c := range []struct {
		protocol string
		want     []string
	}{{"", []string{"agent", "both", "mpponly", "x402only"}}, {"mpp", []string{"both", "mpponly"}}, {"x402", []string{"both", "x402only"}}} {
		req := Request{Query: "weather", Parse: ModeOff, SetKeys: map[string]bool{"protocol": true}, Set: Filters{Protocol: c.protocol}}
		ans, err := e.Find(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		got := ids(ans)
		slices.Sort(got)
		if !slices.Equal(got, c.want) || ans.Filters.Protocol != c.protocol {
			t.Fatalf("protocol %q: %v, filters %+v", c.protocol, got, ans.Filters)
		}
	}
	// The ranking is unchanged by protocols: the same order with and without them.
	plain := slices.Clone(docs)
	for i := range plain {
		plain[i].Protocols, plain[i].Also = nil, nil
	}
	e2 := NewEngine(Config{Source: func(context.Context) ([]Doc, error) { return plain, nil }, Vocab: testVocab})
	t.Cleanup(e2.Close)
	a1, _ := e.Find(context.Background(), Request{Query: "weather mpp", Parse: ModeOff})
	a2, _ := e2.Find(context.Background(), Request{Query: "weather mpp", Parse: ModeOff})
	if !slices.Equal(ids(a1), ids(a2)) {
		t.Fatalf("order changed: %v vs %v", ids(a1), ids(a2))
	}
	ans, _ := e.Find(context.Background(), Request{Query: "weather both", Parse: ModeOff})
	if h := ans.Results[0]; h.ID != "both" || !slices.Equal(h.Protocols, []string{"x402", "mpp/tempo"}) || len(h.Also) != 1 || h.Also[0].SubjectID != "sib" {
		t.Fatalf("hit %+v", h)
	}
	if _, msg := CheckFilters(Filters{Protocol: "tempo"}, testVocab); msg == "" {
		t.Fatal("a protocol outside the families was accepted")
	}
	if f, msg := CheckFilters(Filters{Protocol: " MPP ", Network: "eip155:4217"}, testVocab); msg != "" || f.Protocol != "mpp" || f.Network != "tempo" {
		t.Fatalf("checked %+v %q", f, msg)
	}
	if got := protocolFamilies([]string{"mpp/evm", "x402", "mpp/tempo"}); got != " x402 mpp " {
		t.Fatalf("families %q", got)
	}
}
