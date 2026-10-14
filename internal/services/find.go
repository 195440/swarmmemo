package services

// What Find (internal/find) indexes from the services: the categories and
// their words (x402Categories, the one list), the SwarmMemo tools, and the
// catalogue's entries as the resources read lists them (x402 resources,
// then the MPP directory's entries no x402 resource lists). Reads only: the
// snapshot and the registry, no network, no transaction held.

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/find"
	"swarmmemo/internal/reviews"
)

// CategoryWords is one category and the words (and weak words) that name
// it, as Find matches them.
type CategoryWords = find.Category

// Categories are the x402 catalogue's categories, in the order they are
// tried (x402Categorize), each with its words.
func Categories() []CategoryWords {
	out := make([]CategoryWords, 0, len(x402Categories))
	for _, c := range x402Categories {
		out = append(out, CategoryWords{Name: c.Name, Words: slices.Clone(c.Words), Weak: slices.Clone(c.Weak)})
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
	// Anonymous is the method's catalogue flag (MethodEntry.Anonymous):
	// callable without a key.
	Anonymous bool
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
				Keywords: slices.Clone(m.keywords), PriceText: m.PriceText(), Docs: entry.Docs, Available: available, Anonymous: m.Anonymous})
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
	// ListedBy is the account of the verified domain owner who listed it
	// (x402.submit); "" for a crawled or pinned entry. Proof is how it
	// proved it owns it (ProofDomain or ProofWallet).
	ListedBy string
	Proof    string
	Reason   string // why an MPP entry is not callable
	Calls    int64  // our answered calls, last 30 days
	Failed   int64  // our failed calls, last 30 days
	Demoted  bool   // most of our recent calls failed
	Subject  string
	// Name is how Find names the entry: an MPP entry's DisplayName, an x402
	// resource's "<host's registrable name> · METHOD /path" (x402Names).
	Name string
	// An MPP entry's line and non-USD price as Find shows them (MPPEntry.Line,
	// PriceText); "" for an x402 resource.
	Line, PriceText string
}

// X402Submitter is the account that submitted (x402.submit) the listed
// open resource whose x402 subject id is subject, and how it proved it owns
// it (ProofDomain or ProofWallet): "" when no listed, submitted resource is
// that subject. Listed is X402Listings' sense: in the loaded snapshot (a
// stale entry, as a wallet-proven one whose challenge stopped paying the
// submitter becomes, is not) and not denied. The board's subjectOwners
// reads it: such a submitter owns the subject.
func (r *Registry) X402Submitter(subject string) (account, proof string) {
	p, err := r.Lookup("x402")
	if err != nil {
		return "", ""
	}
	x, ok := p.(*x402)
	if !ok || x.cfg == nil {
		return "", ""
	}
	snap := x.cat.snap.Load()
	if snap == nil {
		return "", ""
	}
	denied := x.cat.denied.Load()
	for _, res := range snap.open {
		proof, account := res.submission()
		if account == "" || (proof != ProofDomain && proof != ProofWallet) || denied.has(res) {
			continue
		}
		if s, err := reviews.Canonical(reviews.TypeX402, res.Method+" "+res.URL, reviews.Options{}); err == nil && s.ID == subject {
			return account, proof
		}
	}
	return "", ""
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
	var named []namedPath // the x402 listings', in order
	add := func(res *X402Resource) {
		named = append(named, res.namedPath())
		l := X402Listing{ID: res.ID, Kind: KindX402, Protocols: snap.protocolsOf(res), Method: res.Method, URL: res.URL, Summary: res.servedSummary(), Category: res.Category, Network: x.cfg.Network,
			MaxPrice: min(res.MaxAmount, x.cfg.PerCall), Decimals: x.cfg.Decimals, Pinned: !res.Open, Callable: x.callable(res), ListedBy: res.Submitter(), Proof: res.Proof()}
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
	for i, n := range x402Names(named) {
		out[i].Name = n
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

// namedPath is the resource as Find names it (C234), from checked tokens
// only, never directory text: "<who> · METHOD " where who is its host's
// registrable name ("agentutility" of x402.agentutility.ai; the host when
// it has none), and its URL's escaped path.
func (r *X402Resource) namedPath() namedPath {
	host, path := "", "/"
	if u, err := url.Parse(r.URL); err == nil {
		host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		if u.EscapedPath() != "" {
			path = u.EscapedPath()
		}
	}
	who := host
	if reg := reviews.RegistrableDomain(host); reg != "" {
		who, _, _ = strings.Cut(reg, ".")
	}
	return namedPath{prefix: who + " · " + r.Method + " ", path: path, url: r.URL}
}

// x402Names are the resources' names: the prefix and the path, a long one
// shortened as an MPP entry's is (mppShortPath), siblings (one registrable
// name, one method) told apart by siblingNames.
func x402Names(items []namedPath) []string {
	names := siblingNames(items)
	for i, it := range items {
		if names[i] == "" {
			names[i] = it.prefix + mppShortPath(it.path)
		}
	}
	return names
}

// URLWords are a URL's host and path as words for an index ("api",
// "weather", "example", "v1", "forecast"): its query and fragment left out.
func URLWords(raw string) string {
	raw, _, _ = strings.Cut(raw, "?")
	raw, _, _ = strings.Cut(raw, "#")
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	return strings.Join(strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '.' || r == '-' || r == '_' || r == ':' }), " ")
}
