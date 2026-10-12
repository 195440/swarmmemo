package services

// The MPP directory beside the Bazaar (C202c, research/mpp-design.md §2.2):
// the server imports the public directory of services that take MPP (the
// "Payment" HTTP authentication scheme), GET https://mpp.dev/api/services,
// on the open catalogue's schedule and under its guardrails, so Find and the
// resources read list them next to the x402 APIs.
//
// One entry per endpoint, keyed by its method and canonical URL
// (canonicalX402URL), whatever protocol lists it: an endpoint the x402
// catalogue also lists stays that one resource, with the MPP methods added
// to its protocols; the others are MPP entries (kind mpp). An MPP entry is
// never callable through the relay: the relay pays x402 upstreams on Base,
// and most MPP services take Tempo (USDC.e on chain 4217) or Stripe, rails
// SwarmMemo does not hold. An entry is a listing for agents that pay MPP
// themselves, and a subject (mpp:METHOD URL) to review.
//
// Guardrails, applied on every import and again on every load: HTTPS on
// 443 to a public DNS name; GET, POST, PUT, PATCH or DELETE; a concrete
// path (no {templates}); not denied by domain, URL prefix, category or
// automatic deny; at most mppEndpointsPerService endpoints of one service
// and the catalogue's max_resources in all; the response size and timeout
// of a discovery page. The directory's text is untrusted data: descriptions
// are cleaned, cut, and served only once screen's classifier passed them
// (the summaries the x402 candidates share, x402_summary_screens); names
// shown are built from the URL and the service's id (a checked token),
// never from the directory's free text, which may only split the id into
// words (mppServiceName). Storage
// never deletes: an entry not seen for three refresh intervals is stale and
// unlisted until it is seen again.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"swarmmemo/internal/reviews"
)

// MPP directory bounds.
const (
	// MPPDirectoryDefault is the directory the server imports unless the
	// catalogue config names another (mpp_directory_url; "off" stops it).
	MPPDirectoryDefault    = "https://mpp.dev/api/services"
	mppDirectoryBytesMax   = bazaarPageBytesMax // one response
	mppServiceBytesMax     = 512 << 10          // one service in it
	mppServicesMax         = 2000
	mppEndpointsPerService = 100
	mppMethodsMax          = 8
	mppPathBytesMax        = 512
	// TempoNetwork is Tempo mainnet's CAIP-2 id; tempo charges settle there.
	TempoNetwork = "eip155:4217"
)

const mppCatalogueSchema = `
CREATE TABLE IF NOT EXISTS mpp_catalogue (
 id TEXT PRIMARY KEY, url TEXT NOT NULL, method TEXT NOT NULL, protocols TEXT NOT NULL DEFAULT '[]',
 intent TEXT NOT NULL DEFAULT '', currency TEXT NOT NULL DEFAULT '', network TEXT NOT NULL DEFAULT '',
 amount TEXT NOT NULL DEFAULT '', decimals INTEGER NOT NULL DEFAULT 0, price INTEGER NOT NULL DEFAULT -1,
 category TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', service TEXT NOT NULL DEFAULT '',
 first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS mpp_catalogue_seen ON mpp_catalogue(last_seen);
`

// Protocol names, as listings carry them: x402, and mpp/METHOD for each MPP
// payment method an endpoint takes (mpp/tempo, mpp/stripe, mpp/evm, ...).
const (
	ProtocolX402      = "x402"
	ProtocolMPPPrefix = "mpp/"
)

// MPPEntry is one endpoint of the MPP directory.
type MPPEntry struct {
	ID     string
	URL    string
	Method string // GET, POST, PUT, PATCH or DELETE
	// Protocols are mpp/METHOD for each payment method the directory says
	// the endpoint takes, the endpoint's own first, the service's others
	// after it, sorted.
	Protocols []string
	Intent    string // the endpoint's payment intent: charge, session, ...
	Currency  string // the method's currency: a token contract, or "usd"
	Network   string // CAIP-2, or "" when the method names none (stripe)
	Amount    string // atomic units of Currency, as listed; "" when dynamic
	Decimals  int
	// Price is micro-USD a call, -1 when dynamic or not a USD currency.
	Price    int64
	Category string
	Summary  string // upstream text, untrusted: servedSummary
	Service  string // the service's name token: mppServiceName of the directory's id

	summaryStatus     string
	canon, host, text string
}

// Key is the endpoint's identity in every catalogue: METHOD canonical URL.
func (e *MPPEntry) Key() string { return e.Method + " " + e.canon }

// servedSummary is the summary listings serve: only once screened.
func (e *MPPEntry) servedSummary() string {
	if e.summaryStatus == summaryScreened {
		return e.Summary
	}
	return ""
}

// Methods are the MPP payment methods of Protocols ("tempo", "stripe").
func (e *MPPEntry) Methods() []string {
	out := make([]string, 0, len(e.Protocols))
	for _, p := range e.Protocols {
		out = append(out, strings.TrimPrefix(p, ProtocolMPPPrefix))
	}
	return out
}

// Reason says why an MPP entry is listed and not callable.
func (e *MPPEntry) Reason(network string) string {
	if slices.Contains(e.Protocols, ProtocolMPPPrefix+"evm") && e.Network == network {
		return "Payable with MPP on Base (evm charge); SwarmMemo's relay pays x402 upstreams only, so it is listed, not callable: pay it with your own MPP client."
	}
	return "Payable with MPP (" + strings.Join(e.Methods(), ", ") + "); SwarmMemo holds no rail for it yet, so it is listed, not callable: pay it with your own MPP client."
}

// index fills e's derived fields: canonical URL, host and search text.
func (e *MPPEntry) index() {
	e.canon = canonicalX402URL(e.URL)
	e.host = ""
	if u, err := url.Parse(e.URL); err == nil {
		e.host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	}
	e.text = strings.ToLower(e.ID + " " + e.Service + " " + e.Category + " " + e.host + " " + strings.Join(e.Protocols, " ") + " " + e.Summary)
}

// admit applies the guardrails to an indexed entry; false refuses it.
func (e *MPPEntry) admit(d *x402DenyIndex) bool {
	return checkX402URL(e.URL) == nil && slices.Contains(mppHTTPMethods, e.Method) && x402IDRE.MatchString(e.ID) &&
		len(e.Protocols) > 0 && !d.blocksURL(e.canon, e.host, e.Category)
}

// mppHTTPMethods are the methods an entry may name: an endpoint subject's.
var mppHTTPMethods = reviews.X402Methods

var (
	mppTokenRE    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	mppCurrencyRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	mppDigitsRE   = regexp.MustCompile(`^[0-9]{1,30}$`)
	mppServiceRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// mppService and mppEndpoint are the directory's shape (mpp.dev
// /api/services, {"version":1,"services":[…]}), read leniently: a field of
// another type skips its service or endpoint, never the import.
type mppService struct {
	ID          string                     `json:"id"`
	URL         string                     `json:"url"`
	Name        string                     `json:"name"` // free text: only checked against the id (mppServiceName)
	ServiceURL  string                     `json:"serviceUrl"`
	Description string                     `json:"description"`
	Categories  json.RawMessage            `json:"categories"`
	Status      string                     `json:"status"`
	Methods     map[string]json.RawMessage `json:"methods"`
	Endpoints   []json.RawMessage          `json:"endpoints"`
}

type mppEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Payment     struct {
		Intent   string          `json:"intent"`
		Method   string          `json:"method"`
		Currency string          `json:"currency"`
		Decimals json.Number     `json:"decimals"`
		Amount   json.RawMessage `json:"amount"`
		Dynamic  bool            `json:"dynamic"`
	} `json:"payment"`
}

// mppMethodInfo is what a service's methods entry may say about a chain.
type mppMethodInfo struct {
	ChainID json.Number `json:"chainId"`
}

// mppInactive are service statuses that list nothing.
var mppInactive = []string{"deprecated", "disabled", "inactive", "offline", "retired"}

// mppDirectoryStats counts what one directory read kept and skipped.
type mppDirectoryStats struct {
	Services, Endpoints, Kept, Skipped, Templated, Duplicates int
}

// parseMPPDirectory reads the directory into entries that pass the
// guardrails under d, one per endpoint (the first listing of an endpoint
// wins; another adds its methods). Text is cleaned and cut here.
func parseMPPDirectory(raw []byte, d *x402DenyIndex) ([]*MPPEntry, mppDirectoryStats, error) {
	var st mppDirectoryStats
	var w struct {
		Version  json.Number       `json:"version"`
		Services []json.RawMessage `json:"services"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if len(raw) > mppDirectoryBytesMax || !utf8.Valid(raw) || dec.Decode(&w) != nil || w.Services == nil || (w.Version != "" && w.Version != "1") {
		return nil, st, errors.New("mpp: not an MPP directory response (JSON version 1 with services)")
	}
	var out []*MPPEntry
	byKey := map[string]*MPPEntry{}
	for i, rawService := range w.Services {
		if i >= mppServicesMax {
			break
		}
		st.Services++
		var s mppService
		if len(rawService) > mppServiceBytesMax || json.Unmarshal(rawService, &s) != nil || slices.Contains(mppInactive, strings.ToLower(s.Status)) {
			st.Skipped += len(s.Endpoints)
			continue
		}
		base := s.ServiceURL
		if base == "" {
			base = s.URL
		}
		if checkX402URL(base) != nil {
			st.Skipped += len(s.Endpoints)
			continue
		}
		base = strings.TrimRight(base, "/")
		var categories []string
		_ = json.Unmarshal(s.Categories, &categories) // anything but strings names none
		service := ""
		if mppServiceRE.MatchString(s.ID) {
			service = mppServiceName(s.ID, base, s.Name)
		}
		// The service's methods, in the directory's words: the ones it
		// takes besides each endpoint's own, and an evm chain.
		var serviceMethods []string
		chain := ""
		for name, info := range s.Methods {
			if !mppTokenRE.MatchString(name) {
				continue
			}
			serviceMethods = append(serviceMethods, name)
			var mi mppMethodInfo
			if name == "evm" && json.Unmarshal(info, &mi) == nil {
				if n, err := strconv.ParseInt(mi.ChainID.String(), 10, 64); err == nil && n > 0 {
					chain = "eip155:" + strconv.FormatInt(n, 10)
				}
			}
		}
		slices.Sort(serviceMethods)
		kept := 0
		for _, rawEndpoint := range s.Endpoints {
			st.Endpoints++
			var ep mppEndpoint
			if kept >= mppEndpointsPerService || json.Unmarshal(rawEndpoint, &ep) != nil {
				st.Skipped++
				continue
			}
			if strings.ContainsAny(ep.Path, "{}*") {
				st.Templated++ // not one concrete URL: nothing to call or probe
				continue
			}
			e, ok := mppEntry(base, ep, serviceMethods, chain, categories, s.Description, service)
			if !ok {
				st.Skipped++
				continue
			}
			e.index()
			if !e.admit(d) {
				st.Skipped++
				continue
			}
			if prev, dup := byKey[e.Key()]; dup {
				st.Duplicates++
				for _, p := range e.Protocols {
					if !slices.Contains(prev.Protocols, p) && len(prev.Protocols) < mppMethodsMax {
						prev.Protocols = append(prev.Protocols, p)
					}
				}
				continue
			}
			byKey[e.Key()] = e
			out = append(out, e)
			kept++
		}
	}
	st.Kept = len(out)
	return out, st, nil
}

// mppEntry reads one endpoint of a service whose base URL is base.
func mppEntry(base string, ep mppEndpoint, serviceMethods []string, chain string, categories []string, serviceDesc, service string) (*MPPEntry, bool) {
	method := strings.ToUpper(strings.TrimSpace(ep.Method))
	p := ep.Payment
	if !slices.Contains(mppHTTPMethods, method) || !strings.HasPrefix(ep.Path, "/") || len(ep.Path) > mppPathBytesMax ||
		strings.ContainsAny(ep.Path, " \t\r\n#") || !mppTokenRE.MatchString(p.Method) || (p.Intent != "" && !mppTokenRE.MatchString(p.Intent)) {
		return nil, false
	}
	e := &MPPEntry{URL: base + ep.Path, Method: method, Intent: p.Intent, Service: service, Price: -1}
	e.Protocols = []string{ProtocolMPPPrefix + p.Method}
	for _, m := range serviceMethods {
		if m != p.Method && len(e.Protocols) < mppMethodsMax {
			e.Protocols = append(e.Protocols, ProtocolMPPPrefix+m)
		}
	}
	if mppCurrencyRE.MatchString(p.Currency) {
		e.Currency = p.Currency
	}
	if n, err := strconv.Atoi(p.Decimals.String()); err == nil && n >= 0 && n <= 18 {
		e.Decimals = n
	}
	var amount string
	if json.Unmarshal(p.Amount, &amount) != nil {
		var n json.Number
		if json.Unmarshal(p.Amount, &n) == nil {
			amount = n.String()
		}
	}
	if !p.Dynamic && mppDigitsRE.MatchString(amount) {
		e.Amount = amount
	}
	switch p.Method {
	case "tempo":
		e.Network = TempoNetwork
	case "evm":
		e.Network = chain
		if e.Network == "" && strings.EqualFold(e.Currency, baseUSDC) {
			e.Network = "eip155:8453"
		}
	}
	if e.Amount != "" && usdCurrency(e.Currency) {
		e.Price = microUSD(e.Amount, e.Decimals)
	}
	u, _ := url.Parse(e.URL)
	if u == nil {
		return nil, false
	}
	slug := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(u.Hostname()+u.Path), "-"), "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	e.ID = openID(slug, e.Method, e.URL)
	desc := ep.Description
	if desc == "" {
		desc = serviceDesc
	}
	e.Summary = cleanUntrusted(desc, 200)
	e.Category = x402Categorize(e.URL + " " + strings.Join(categories, " ") + " " + desc)
	return e, true
}

// baseUSDC is native USDC on Base, lower-cased.
const baseUSDC = "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"

// usdCurrency reports whether a directory currency is a US dollar: "usd"
// (Stripe), USDC on Base, or USDC.e on Tempo, whose TIP-20 address starts
// 0x20c0 and ends b9537d11c60e8b50 (mpp-design.md §1). Only the shown price
// depends on it: nothing is paid on this reading.
func usdCurrency(c string) bool {
	c = strings.ToLower(c)
	return c == "usd" || c == baseUSDC || len(c) == 42 && strings.HasPrefix(c, "0x20c0") && strings.HasSuffix(c, "b9537d11c60e8b50")
}

// microUSD is amount atomic units at decimals as micro-USD, rounded down;
// -1 above a million dollars.
func microUSD(amount string, decimals int) int64 {
	n, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		return -1
	}
	if decimals > 6 {
		n.Quo(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals-6)), nil))
	} else {
		n.Mul(n, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(6-decimals)), nil))
	}
	if !n.IsInt64() || n.Int64() > 1_000_000_000_000 {
		return -1
	}
	return n.Int64()
}

// importMPP reads the MPP directory and upserts what passes the
// guardrails, at most the catalogue's max_resources, then reloads the
// snapshot. It returns how many entries it kept. Background only.
func (x *x402) importMPP(ctx context.Context, now int64) (int, error) {
	cc := x.cfg.Catalogue
	if cc == nil || cc.MPPDirectory == "" {
		return 0, nil
	}
	hist, err := x.readHistory(ctx, now)
	if err != nil {
		return 0, err
	}
	raw, err := x.fetchDiscovery(ctx, cc.MPPDirectory, mppDirectoryBytesMax)
	if err != nil {
		return 0, err
	}
	entries, _, err := parseMPPDirectory(raw, x.cfg.denyIndex(hist.denied))
	if err != nil {
		return 0, err
	}
	entries = entries[:min(len(entries), cc.MaxResources)]
	for start := 0; start < len(entries); start += x402UpsertBatch {
		tx, err := x.db.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		for _, e := range entries[start:min(start+x402UpsertBatch, len(entries))] {
			protocols, _ := json.Marshal(e.Protocols)
			// An id is its method and URL's hash: a row whose URL or method
			// differs is a collision, never overwritten (as x402_catalogue).
			if _, err = tx.ExecContext(ctx, `INSERT INTO mpp_catalogue(id,url,method,protocols,intent,currency,network,amount,decimals,price,category,summary,service,first_seen,last_seen)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET protocols=excluded.protocols, intent=excluded.intent, currency=excluded.currency, network=excluded.network, amount=excluded.amount,
 decimals=excluded.decimals, price=excluded.price, category=excluded.category, summary=excluded.summary, service=excluded.service, last_seen=excluded.last_seen
 WHERE mpp_catalogue.url=excluded.url AND mpp_catalogue.method=excluded.method`,
				e.ID, e.URL, e.Method, string(protocols), e.Intent, e.Currency, e.Network, e.Amount, e.Decimals, e.Price, e.Category, e.Summary, e.Service, now, now); err != nil {
				_ = tx.Rollback()
				return 0, err
			}
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
	}
	return len(entries), x.loadCatalogue(ctx, now)
}

// mppRow is one mpp_catalogue row as read.
type mppRow struct {
	e         MPPEntry
	protocols string
	seen      int64
	hash      string
}

// readMPP reads the fresh mpp_catalogue rows, x402LoadPage at a time, at
// most limit of them; each statement closes its rows before the next.
func (x *x402) readMPP(ctx context.Context, stale int64, limit int) ([]mppRow, error) {
	var rows []mppRow
	for after := ""; len(rows) < limit; {
		page, err := queryAll(ctx, x.db, func(q *sql.Rows) (mppRow, error) {
			var r mppRow
			err := q.Scan(&r.e.ID, &r.e.URL, &r.e.Method, &r.protocols, &r.e.Intent, &r.e.Currency, &r.e.Network, &r.e.Amount, &r.e.Decimals, &r.e.Price, &r.e.Category, &r.e.Summary, &r.e.Service, &r.seen)
			return r, err
		}, `SELECT id,url,method,protocols,intent,currency,network,amount,decimals,price,category,summary,service,last_seen
FROM mpp_catalogue WHERE last_seen>=? AND id>? ORDER BY id LIMIT ?`, stale, after, min(x402LoadPage, limit-len(rows)))
		if err != nil {
			return nil, err
		}
		rows = append(rows, page...)
		if len(page) < x402LoadPage {
			break
		}
		after = page[len(page)-1].e.ID
	}
	return rows, nil
}

// mppSnapshot is the MPP side of a catalogue snapshot: every admitted
// entry (all, and by key), and the ones no x402 resource lists (only, kind
// mpp), each ordered by URL then method.
type mppSnapshot struct {
	byKey      map[string]*MPPEntry
	all        []*MPPEntry
	only       []*MPPEntry
	byID       map[string]*MPPEntry
	importedAt int64
}

// buildMPP admits the rows again under d and joins them with the x402
// resources listed (keys: METHOD canonical URL of each).
func buildMPP(rows []mppRow, d *x402DenyIndex, verdicts map[string]string, x402Keys map[string]bool, limit int) mppSnapshot {
	m := mppSnapshot{byKey: map[string]*MPPEntry{}, byID: map[string]*MPPEntry{}}
	for i := range rows {
		r := &rows[i]
		e := &r.e
		if json.Unmarshal([]byte(r.protocols), &e.Protocols) != nil || len(e.Protocols) > mppMethodsMax {
			continue
		}
		e.index()
		if !e.admit(d) || m.byKey[e.Key()] != nil {
			continue
		}
		e.summaryStatus = summaryStatusFor(false, e.Summary, verdicts[r.hash])
		m.importedAt = max(m.importedAt, r.seen)
		m.byKey[e.Key()] = e
		m.all = append(m.all, e)
		if !x402Keys[e.Key()] && len(m.only) < limit {
			m.only = append(m.only, e)
			m.byID[e.ID] = e
		}
	}
	byURL := func(a, b *MPPEntry) int {
		if c := strings.Compare(a.canon, b.canon); c != 0 {
			return c
		}
		return strings.Compare(a.Method, b.Method)
	}
	slices.SortFunc(m.all, byURL)
	slices.SortFunc(m.only, byURL)
	return m
}

// protocolsOf is an x402 resource's protocols: x402, and the MPP methods
// the directory lists for the same endpoint.
func (s *x402Snapshot) protocolsOf(r *X402Resource) []string {
	out := []string{ProtocolX402}
	if e := s.mpp.byKey[r.Method+" "+r.canon]; e != nil {
		out = append(out, e.Protocols...)
	}
	return out
}

// HasProtocol reports whether protocols carry one of the named family:
// "x402", or "mpp" for any mpp/METHOD.
func HasProtocol(protocols []string, family string) bool {
	for _, p := range protocols {
		if p == family || family == "mpp" && strings.HasPrefix(p, ProtocolMPPPrefix) {
			return true
		}
	}
	return false
}

// Protocols are the protocol families a filter may name.
var Protocols = []string{"x402", "mpp"}

// fetchDiscovery GETs one discovery document through the SSRF-safe client:
// a 200 of at most limit bytes, within x402PageTimeout.
func (x *x402) fetchDiscovery(ctx context.Context, u string, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, x402PageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SwarmMemo-x402/1 (+https://swarmmemo.com/protocol.md)")
	req.Header.Set("Accept", "application/json")
	resp, err := x.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("x402: discovery answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("x402: discovery response too large")
	}
	return raw, nil
}

// How an MPP entry reads in Find (C219): its name, a line when it has no
// served summary, and its price when the directory gives one not in USD.
const (
	mppNamePathMax = 40 // a shown path's runes before its leading segments go
	mppNameLastMax = 60 // the last segment's, which always stays
)

// DisplayName is the entry as Find names it: "<who> · METHOD /path" (who
// below); a long path loses its leading segments, never its last, so two
// endpoints of one service read apart. A leading segment that only repeats
// the service's name ("/agentphone/v1/agents" of agentphone) is left out.
func (e *MPPEntry) DisplayName() string {
	path := "/"
	if u, err := url.Parse(e.URL); err == nil && u.EscapedPath() != "" {
		path = u.EscapedPath()
	}
	if name := e.serviceName(); name != "" && mppPathNames(name, e.URL) {
		if _, rest, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/"); ok {
			path = "/" + rest
		}
	}
	return e.who() + " · " + e.Method + " " + mppShortPath(path)
}

// who names the entry's service from checked tokens only (C227): its name
// token, else its host. A service an aggregator serves under a path of its
// own host, the host not naming it ("https://mpp.orthogonal.com/agentphone/…"),
// reads "agentphone via orthogonal", the aggregator the host's registrable
// name, so one API listed by two providers reads apart.
func (e *MPPEntry) who() string {
	name := e.serviceName()
	if name == "" {
		return e.host
	}
	if mppPathNames(name, e.URL) && !strings.Contains(mppLetters(e.host), mppLetters(name)) {
		if reg := reviews.RegistrableDomain(e.host); reg != "" {
			label, _, _ := strings.Cut(reg, ".")
			return name + " via " + label
		}
	}
	return name
}

// serviceName is the entry's name token, an aggregator's prefix cut also
// from a row stored before C227.
func (e *MPPEntry) serviceName() string {
	return mppStripAggregator(e.Service, e.URL)
}

// mppServiceName is the name token a directory service is shown by, built
// from its id (a checked token), never from its free text:
//
//   - an aggregator's prefix goes: an id "<prefix>-<name>" of a service
//     that lives under the path /<name> of its URL ("orth-agentphone" at
//     https://mpp.orthogonal.com/agentphone) is "agentphone";
//   - the directory's name may only split the id into words: when its
//     letters and digits, lower-cased, are exactly the id's ("Build With
//     Locus" and "buildwithlocus"), the token is those words joined by "-"
//     ("build-with-locus"), so Find matches each word ("locus"); any other
//     name changes nothing.
func mppServiceName(id, serviceURL, name string) string {
	id = mppStripAggregator(id, serviceURL)
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') })
	if len(words) > 1 && strings.Join(words, "") == mppLetters(id) {
		if joined := strings.Join(words, "-"); mppServiceRE.MatchString(joined) {
			return joined
		}
	}
	return id
}

// mppStripAggregator is id without an aggregator's prefix: "<prefix>-<name>"
// is "<name>" when u's first path segment is exactly <name>; else id.
func mppStripAggregator(id, u string) string {
	if _, name, ok := strings.Cut(id, "-"); ok && name != "" && strings.EqualFold(mppFirstSegment(u), name) {
		return name
	}
	return id
}

// mppPathNames reports whether u's first path segment spells name (the same
// letters and digits).
func mppPathNames(name, u string) bool {
	seg := mppLetters(mppFirstSegment(u))
	return seg != "" && seg == mppLetters(name)
}

// mppFirstSegment is u's first path segment, "" when it has none.
func mppFirstSegment(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(p.Path, "/"), "/")
	return seg
}

// mppLetters is s's ASCII letters and digits, lower-cased.
func mppLetters(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mppShortPath keeps a path's last segment (cut at mppNameLastMax runes)
// and as many segments before it as fit in mppNamePathMax, "…" for the rest.
func mppShortPath(path string) string {
	if utf8.RuneCountInString(path) <= mppNamePathMax {
		return path
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	last := segs[len(segs)-1]
	if r := []rune(last); len(r) > mppNameLastMax {
		last = string(r[:mppNameLastMax]) + "…"
	}
	out := "/" + last
	for i := len(segs) - 2; i >= 0; i-- {
		next := "/" + segs[i] + out
		if utf8.RuneCountInString("/…"+next) > mppNamePathMax {
			break
		}
		out = next
	}
	return "/…" + out
}

// Line is what Find says of an entry without a served summary, built from
// checked tokens only: "MPP charge endpoint of openrouter, paid in USDC.e".
func (e *MPPEntry) Line() string {
	s := "MPP endpoint of " + e.who()
	if e.Intent != "" {
		s = "MPP " + e.Intent + " endpoint of " + e.who()
	}
	if e.Currency != "" {
		s += ", paid in " + mppCurrencyName(e.Currency)
	}
	return s
}

// PriceText is a fixed price the directory gives in a currency that is not
// a US dollar, in words ("0.5 EURC a call"); "" when dynamic, or in USD
// (Price says it then).
func (e *MPPEntry) PriceText() string {
	if e.Amount == "" || e.Currency == "" || usdCurrency(e.Currency) {
		return ""
	}
	return mppUnits(e.Amount, e.Decimals) + " " + mppCurrencyName(e.Currency) + " a call"
}

// mppCurrencyName is a directory currency as shown: the dollars by name, a
// short code upper-cased, a token contract shortened (0x20c0…0000).
func mppCurrencyName(c string) string {
	lc := strings.ToLower(c)
	switch {
	case lc == "usd":
		return "USD"
	case lc == baseUSDC:
		return "USDC"
	case usdCurrency(lc):
		return "USDC.e"
	case strings.HasPrefix(lc, "0x") && len(lc) > 12:
		return lc[:6] + "…" + lc[len(lc)-4:]
	case len(c) <= 12:
		return strings.ToUpper(c)
	}
	return c[:12] + "…"
}

// mppUnits is atomic units (decimal digits) at decimals as a decimal.
func mppUnits(amount string, decimals int) string {
	s := strings.TrimLeft(amount, "0")
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	whole, frac := s[:len(s)-decimals], strings.TrimRight(s[len(s)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
