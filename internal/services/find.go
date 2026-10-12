package services

// What Find (internal/find) indexes from the services: the categories and
// their words (x402Categories, the one list), the SwarmMemo tools, and the
// catalogue's entries as the resources read lists them (x402 resources,
// then the MPP directory's entries no x402 resource lists). Reads only: the
// snapshot and the registry, no network, no transaction held.

import (
	"context"
	"slices"
	"strings"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/reviews"
)

// CategoryWords is one category and the words that name it.
type CategoryWords struct {
	Name  string
	Words []string
}

// Categories are the x402 catalogue's categories, in the order they are
// tried (x402Categorize), each with its words.
func Categories() []CategoryWords {
	out := make([]CategoryWords, 0, len(x402Categories)+1)
	for _, c := range x402Categories {
		out = append(out, CategoryWords{Name: c.name, Words: slices.Clone(c.words)})
	}
	return out
}

// Categorize names text's category as the catalogue does; "other" when no
// category's word is in it.
func Categorize(text string) string { return x402Categorize(text) }

// ToolListing is one SwarmMemo tool as Find indexes it.
type ToolListing struct {
	ID        string // swarmmemo:SERVICE.METHOD
	Title     string
	Line      string
	Keywords  []string
	PriceText string
	Docs      string
	Available bool
}

// ToolListings are the SwarmMemo tools tools_search lists (each write
// method of a toolable service) at the current prices.
func (e *Engine) ToolListings(ctx context.Context, q allowance.Querier, now int64) ([]ToolListing, error) {
	_, prices, err := e.prices(ctx, q, now)
	if err != nil {
		return nil, err
	}
	var out []ToolListing
	for _, entry := range e.cfg.Registry.catalog(prices, true) {
		p, err := e.cfg.Registry.Lookup(entry.ID)
		if err != nil || !toolable(p.Describe()) {
			continue
		}
		available := true
		if v, ok := entry.Extra["available"].(bool); ok {
			available = v
		}
		for _, m := range entry.Methods {
			if !m.Write() || m.Deprecated {
				continue
			}
			out = append(out, ToolListing{ID: ToolID(entry.ID, m.Name), Title: entry.Title + ": " + m.Name, Line: m.Line,
				Keywords: slices.Clone(m.keywords), PriceText: m.PriceText(), Docs: entry.Docs, Available: available})
		}
	}
	return out, nil
}

// X402Listing is one entry of the catalogue as Find indexes it: what the
// resources read lists, with our own calls' record.
type X402Listing struct {
	ID        string
	Kind      string   // KindX402 (the relay may call it) or KindMPP (listed only)
	Protocols []string // x402 and mpp/METHOD, as the resources read lists them
	Method    string
	URL       string // indexed for its words; shown for an MPP entry only
	Summary   string // the served summary: "" while an open one is unscreened or withheld
	Category  string
	Network   string // CAIP-2, or ""
	MaxPrice  int64  // atomic units of the asset, capped at the per-call cap; -1 unpriced
	Decimals  int
	Pinned    bool
	Callable  bool
	Reason    string // why an MPP entry is not callable
	Calls     int64  // our answered calls, last 30 days
	Failed    int64  // our failed calls, last 30 days
	Demoted   bool   // most of our recent calls failed
	Subject   string
	// An MPP entry's name, line and non-USD price as Find shows them
	// (MPPEntry.DisplayName, Line, PriceText); "" for an x402 resource.
	Name, Line, PriceText string
}

// X402Listings are the resources the resources read would list: the
// pinned ones whose bundler is ready, then the open catalogue's, less the
// denied. nil when x402 is not configured.
func (r *Registry) X402Listings() []X402Listing {
	p, err := r.Lookup("x402")
	if err != nil {
		return nil
	}
	x, ok := p.(*x402)
	if !ok || x.cfg == nil {
		return nil
	}
	snap := x.cat.snap.Load()
	if snap == nil {
		snap = &x402Snapshot{}
	}
	denied := x.cat.denied.Load()
	var out []X402Listing
	add := func(res *X402Resource) {
		l := X402Listing{ID: res.ID, Kind: KindX402, Protocols: snap.protocolsOf(res), Method: res.Method, URL: res.URL, Summary: res.servedSummary(), Category: res.Category, Network: x.cfg.Network,
			MaxPrice: min(res.MaxAmount, x.cfg.PerCall), Decimals: x.cfg.Decimals, Pinned: !res.Open, Callable: x.callable(res)}
		if st, ok := snap.stats[res.ID]; ok {
			l.Calls, l.Failed, l.Demoted = st.ok, st.failed, st.demoted()
		}
		if s, err := reviews.Canonical(reviews.TypeX402, res.Method+" "+res.URL, reviews.Options{}); err == nil {
			l.Subject = s.ID
		}
		if res.Category == "" {
			l.Category = x402Categorize(res.URL + " " + l.Summary)
		}
		out = append(out, l)
	}
	for i := range x.cfg.Resources {
		res := &x.cfg.Resources[i]
		if b := x.bundlers[res.Bundler]; b == nil || !b.Ready() {
			continue
		}
		add(res)
	}
	for _, res := range snap.open {
		if !denied.has(res) {
			add(res)
		}
	}
	for _, e := range snap.mpp.only {
		l := X402Listing{ID: e.ID, Kind: KindMPP, Protocols: e.Protocols, Method: e.Method, URL: e.URL, Summary: e.servedSummary(), Category: e.Category,
			Network: e.Network, MaxPrice: e.Price, Decimals: 6, Reason: e.Reason(x.cfg.Network),
			Name: e.DisplayName(), Line: e.Line(), PriceText: e.PriceText()}
		if s, err := reviews.Canonical(reviews.TypeMPP, e.Method+" "+e.URL, reviews.Options{}); err == nil {
			l.Subject = s.ID
		}
		out = append(out, l)
	}
	return out
}

// URLWords are a URL's host and path as words for an index ("api",
// "weather", "example", "v1", "forecast"): its query and fragment left out.
func URLWords(raw string) string {
	raw, _, _ = strings.Cut(raw, "?")
	raw, _, _ = strings.Cut(raw, "#")
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	return strings.Join(strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '.' || r == '-' || r == '_' || r == ':' }), " ")
}
