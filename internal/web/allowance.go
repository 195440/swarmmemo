package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/board"
	"swarmmemo/internal/trust"
)

// RFC0012 product surfaces (§11), on RFC 0021's share rule. The allowance on
// /stats, and the allowance and trust sections of agent pages, each read one
// function that a JSON API serves too (invariant 9): ReadAllowanceStats is
// /api/stats/allowance, and agent pages render allowance.get and trust.get,
// the operations every wire serves. The page adds formatting, never a number
// of its own. With every flag off nothing here renders, so today's pages keep
// their bytes.

// ShareRuleSentence explains the allowance in one sentence (RFC 0021 §12).
// It is board.ShareRuleSentence itself, so every surface (capabilities,
// protocol, openapi, /stats, quickstart, DNS, the line transport) says the
// same thing.
const ShareRuleSentence = board.ShareRuleSentence

// AllowanceMore is how to get more allowance, in one sentence.
const AllowanceMore = "To get more, raise your standing (link a domain, wallet or GitHub account, get rated work) or receive a transfer; see /protocol.md#allowance-and-the-share-rule."

// ServiceFeatures reads the RFC0012 flags of the store behind service. A
// service that does not report them has every flag off.
func ServiceFeatures(service board.Service) board.Features {
	if f, ok := service.(interface{ Features() board.Features }); ok {
		return f.Features()
	}
	return board.Features{}
}

// LedgerLive reports whether the share rule decides what agents may spend,
// so agent-facing copy may describe it. In shadow mode it is only computed.
func LedgerLive(f board.Features) bool { return f.Ledger == board.LedgerOn }

// resourceOrder is the order resources are listed in everywhere.
var resourceOrder = []string{"post_bytes", "memory_bytes", "credit"}

func resourceRank(r string) int {
	for i, name := range resourceOrder {
		if name == r {
			return i
		}
	}
	return len(resourceOrder)
}

func resourceLabel(r string) string {
	switch r {
	case "post_bytes":
		return "Posting"
	case "memory_bytes":
		return "Memory"
	case "credit":
		return "Credit"
	}
	return r
}

func resourceUnit(r string) string {
	if strings.HasSuffix(r, "_bytes") {
		return "byte"
	}
	return "credit"
}

// units formats an amount in its resource's unit.
func units(r string, n int64) string {
	if strings.HasSuffix(r, "_bytes") {
		return size(n)
	}
	if n == 1 {
		return "1 credit"
	}
	return count(n) + " credits"
}

// ppmPercent formats parts per million as a whole percent.
func ppmPercent(ppm int64) string {
	return strconv.FormatInt((ppm+5000)/10000, 10) + "%"
}

// cents formats an amount of US cents: "1,250¢".
func cents(n int64) string { return trust.CentsText(n) }

// utcDay is a UTC day as YYYY-MM-DD; it also reads a day index (days since
// the Unix epoch), the ledger's own key.
type utcDay string

func (d *utcDay) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		*d = utcDay(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	*d = utcDay(time.Unix(n*86400, 0).UTC().Format("2006-01-02"))
	return nil
}

// AllowanceStats is /api/stats/allowance and the allowance section of /stats.
// Allowance is present while ALLOWANCE_LEDGER is not off, Trust while TRUST is
// not off. Schema 2 is RFC 0021's share rule: slices, not tier pools.
type AllowanceStats struct {
	Schema      int             `json:"schema"`
	GeneratedAt int64           `json:"generated_at"`
	Days        int             `json:"days"`
	Allowance   *ShareRuleStats `json:"allowance,omitempty"`
	Trust       *TrustStats     `json:"trust,omitempty"`
}

// AllowanceStatsSchema is the schema of /api/stats/allowance.
const AllowanceStatsSchema = 2

// ShareRuleStats is the ledger's public state: today's slices per resource,
// the last days, use by service and bucket, transfers and levers. It is
// Store.AllowanceStats, sorted and with the derived numbers added. Nothing is
// split by who runs an agent: only by slice, resource, service and bucket.
type ShareRuleStats struct {
	Ledger        string         `json:"ledger"`
	Rule          string         `json:"rule"`
	ParamsVersion int64          `json:"params_version"`
	Resources     []ResourceDay  `json:"resources"`
	History       []HistoryDay   `json:"history"`
	Services      []ServiceUse   `json:"services"`
	Transfers     TransferTotals `json:"transfers"`
	Levers        []LeverState   `json:"levers"`
}

// ResourceDay is one resource's share rule today. Issued is what the slices
// handed out (drawn plus lent); Unallocated was never shared out.
type ResourceDay struct {
	Resource        string         `json:"resource"`
	Unit            string         `json:"unit"`
	Day             utcDay         `json:"day"`
	Budget          int64          `json:"budget"`
	BudgetEffective int64          `json:"budget_effective"`
	Issued          int64          `json:"issued"`
	Spent           int64          `json:"spent"`
	SpentPaid       int64          `json:"spent_paid"`
	Unallocated     int64          `json:"unallocated"`
	Slices          []SliceDay     `json:"slices"`
	Anonymous       AnonymousShare `json:"anonymous"`
}

// SliceDay is one slice of the day's budget (RFC 0021 §12): "grant" (what
// grants are minted from), "floor" (an equal floor for every claimant) or
// "standing" (shared by standing, one share per root). FillPPM is how much
// of its size was handed out.
type SliceDay struct {
	Slice      string `json:"slice"`
	Size       int64  `json:"size"`
	Drawn      int64  `json:"drawn"`
	Lent       int64  `json:"lent"`
	Borrowed   int64  `json:"borrowed"`
	Claimants  int64  `json:"claimants"`
	Expected   int64  `json:"expected"`
	ShareCents int64  `json:"share_cents"`
	FillPPM    int64  `json:"fill_ppm"`
}

// AnonymousShare is what anonymous subjects drew today, and how many drew.
type AnonymousShare struct {
	Drawn     int64 `json:"drawn"`
	Claimants int64 `json:"claimants"`
}

// sliceOrder is the order slices are listed and drawn in.
var sliceOrder = []string{"grant", "floor", "standing"}

func sliceRank(s string) int {
	for i, name := range sliceOrder {
		if name == s {
			return i
		}
	}
	return len(sliceOrder)
}

// sliceLabel is a slice's label on a page.
func sliceLabel(s string) string {
	switch s {
	case "grant":
		return "Grants"
	case "floor":
		return "Floor"
	case "standing":
		return "By standing"
	}
	return capitalize(s)
}

// HistoryDay is one resource on one earlier UTC day.
type HistoryDay struct {
	Day       utcDay `json:"day"`
	Resource  string `json:"resource"`
	Budget    int64  `json:"budget"`
	Issued    int64  `json:"issued"`
	Spent     int64  `json:"spent"`
	Claimants int64  `json:"claimants"`
}

// ServiceUse is what one service spent today from one bucket, by signed
// accounts or by anonymous subjects (Subjects).
type ServiceUse struct {
	Service  string `json:"service"`
	Resource string `json:"resource"`
	Bucket   string `json:"bucket"`
	Subjects string `json:"subjects"`
	Units    int64  `json:"units"`
	Calls    int64  `json:"calls"`
}

// TransferTotals are today's transfers, all public at /api/ledger.
type TransferTotals struct {
	Resource                 string `json:"resource"`
	Count                    int64  `json:"count"`
	Volume                   int64  `json:"volume"`
	Pending                  int64  `json:"pending"`
	LargestRecipientShareppm int64  `json:"largest_recipient_share_ppm"`
}

// LeverState is one pulled lever (§2.6); the full log is /api/levers.
type LeverState struct {
	Name   string `json:"name"`
	Args   string `json:"args"`
	Since  int64  `json:"since"`
	Until  int64  `json:"until"`
	Reason string `json:"reason"`
}

// TrustStats is the public trust distribution (RFC 0021 §13): how many
// accounts fall in each log10 bin of standing in cents, how many in each
// band, and how many are at or above the vote floor.
type TrustStats struct {
	Mode           string      `json:"mode"`
	Run            int64       `json:"run"`
	AsOf           int64       `json:"as_of"`
	Stale          bool        `json:"stale"`
	Accounts       int64       `json:"accounts"`
	CentsLog10     []TrustBin  `json:"cents_log10"`
	Bands          []TrustBand `json:"bands"`
	AboveVoteFloor int64       `json:"above_vote_floor"`
	VoteFloorCents int64       `json:"vote_floor_cents"`
	// Sources is where the run's standing comes from, by kind
	// (trust.SourceKinds): own costs before each root's cap, and what
	// judgements delivered.
	Sources []TrustSource `json:"sources"`
	// History is the recent runs, oldest first: decentralisation (the top
	// five sources' share of the flow passed on) and penalties in force.
	History       []TrustRunPoint `json:"history"`
	PenaltiesOpen int64           `json:"penalties_open"`
	Penalised     int64           `json:"penalised"`
}

// TrustSource is one kind standing comes from, in cents.
type TrustSource struct {
	Kind  string `json:"kind"`
	Cents int64  `json:"cents"`
}

// TrustRunPoint is one recent run's public totals.
type TrustRunPoint struct {
	Run           int64 `json:"run"`
	AsOf          int64 `json:"as_of"`
	Top5PassedPPM int64 `json:"top5_passed_ppm"`
	Sources       int64 `json:"sources"`
	PenaltiesOpen int64 `json:"penalties_open"`
	StandingCents int64 `json:"standing_cents"`
}

// TrustBin counts accounts with standing in [10^From, 10^To) cents; bin 0
// also holds standing 0.
type TrustBin struct {
	From     int   `json:"from"`
	To       int   `json:"to"`
	Accounts int64 `json:"accounts"`
}

// TrustBand counts the accounts of one band.
type TrustBand struct {
	Band     string `json:"band"`
	Accounts int64  `json:"accounts"`
}

// trustBands are the bands a trust run places signed accounts in, lowest
// first (anonymous subjects have no trust row).
var trustBands = allowance.BandNames[allowance.BandSigned:]

// allowanceStatsSource is the store side: the maps are decoded by field name
// into the types above. Store.AllowanceStats returns params_version and the
// resources (with their slices and anonymous totals), history, services,
// transfers and levers; a day may be a day index. Store.TrustDistribution
// returns run, as_of, stale, cents_log10_bins (accounts per bin, bin i =
// [10^i, 10^(i+1))), bands keyed by name, above_vote_floor and
// vote_floor_cents.
type allowanceStatsSource interface {
	AllowanceStats(ctx context.Context, days int) (map[string]any, error)
	TrustDistribution(ctx context.Context) (map[string]any, error)
}

// errStatsUnavailable is a store that cannot report the allowance.
var errStatsUnavailable = errors.New("allowance statistics unavailable")

// AllowanceStatsDays bound the days parameter of /api/stats/allowance.
const (
	AllowanceStatsDaysDefault = 7
	AllowanceStatsDaysMaximum = 30
)

// ReadAllowanceStats is the one function behind /api/stats/allowance and the
// /stats allowance section. It returns nil, nil when the ledger and trust are
// both off.
func ReadAllowanceStats(ctx context.Context, service board.Service, f board.Features, days int, now time.Time) (*AllowanceStats, error) {
	if f.Ledger == board.LedgerOff && f.Trust == board.TrustOff {
		return nil, nil
	}
	src, ok := service.(allowanceStatsSource)
	if !ok {
		return nil, errStatsUnavailable
	}
	out := &AllowanceStats{Schema: AllowanceStatsSchema, GeneratedAt: now.Unix(), Days: days}
	if f.Ledger != board.LedgerOff {
		raw, err := src.AllowanceStats(ctx, days)
		if err != nil {
			return nil, err
		}
		w := &ShareRuleStats{}
		if err := remarshal(raw, w); err != nil {
			return nil, err
		}
		normalizeShareRule(w, f)
		out.Allowance = w
	}
	if f.Trust != board.TrustOff {
		raw, err := src.TrustDistribution(ctx)
		if err != nil {
			return nil, err
		}
		t, err := trustStatsFrom(raw, f)
		if err != nil {
			return nil, err
		}
		out.Trust = t
	}
	return out, nil
}

// remarshal decodes a store map into a typed value through JSON, so the
// field names are the contract and unknown fields are ignored.
func remarshal(in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func normalizeShareRule(w *ShareRuleStats, f board.Features) {
	w.Ledger, w.Rule = f.Ledger.String(), ShareRuleSentence
	for i := range w.Resources {
		r := &w.Resources[i]
		r.Unit = resourceUnit(r.Resource)
		sort.SliceStable(r.Slices, func(a, b int) bool { return sliceRank(r.Slices[a].Slice) < sliceRank(r.Slices[b].Slice) })
		r.Issued = 0
		for j := range r.Slices {
			s := &r.Slices[j]
			s.FillPPM = 0
			if s.Size > 0 {
				s.FillPPM = min(max(s.Drawn+s.Lent, 0)*1_000_000/s.Size, 1_000_000)
			}
			r.Issued += s.Drawn + s.Lent
		}
		if r.Slices == nil {
			r.Slices = []SliceDay{}
		}
	}
	sort.SliceStable(w.Resources, func(a, b int) bool {
		return resourceRank(w.Resources[a].Resource) < resourceRank(w.Resources[b].Resource) ||
			resourceRank(w.Resources[a].Resource) == resourceRank(w.Resources[b].Resource) && w.Resources[a].Resource < w.Resources[b].Resource
	})
	// Newest day first, as the /stats table reads.
	sort.SliceStable(w.History, func(a, b int) bool {
		if w.History[a].Day != w.History[b].Day {
			return w.History[a].Day > w.History[b].Day
		}
		return resourceRank(w.History[a].Resource) < resourceRank(w.History[b].Resource)
	})
	sort.SliceStable(w.Services, func(a, b int) bool {
		x, y := w.Services[a], w.Services[b]
		if x.Units != y.Units {
			return x.Units > y.Units
		}
		if x.Service != y.Service {
			return x.Service < y.Service
		}
		if x.Bucket != y.Bucket {
			return x.Bucket < y.Bucket
		}
		return x.Subjects > y.Subjects // signed before anonymous
	})
	sort.SliceStable(w.Levers, func(a, b int) bool { return w.Levers[a].Name < w.Levers[b].Name })
	if w.Transfers.Resource == "" {
		w.Transfers.Resource = "post_bytes"
	}
	if w.History == nil {
		w.History = []HistoryDay{}
	}
	if w.Resources == nil {
		w.Resources = []ResourceDay{}
	}
	if w.Services == nil {
		w.Services = []ServiceUse{}
	}
	if w.Levers == nil {
		w.Levers = []LeverState{}
	}
}

func trustStatsFrom(raw map[string]any, f board.Features) (*TrustStats, error) {
	var in struct {
		Run            int64            `json:"run"`
		AsOf           int64            `json:"as_of"`
		Stale          bool             `json:"stale"`
		Bins           []int64          `json:"cents_log10_bins"`
		Bands          map[string]int64 `json:"bands"`
		AboveVoteFloor int64            `json:"above_vote_floor"`
		VoteFloorCents int64            `json:"vote_floor_cents"`
		Sources        map[string]int64 `json:"sources_cents"`
		History        []TrustRunPoint  `json:"history"`
		PenaltiesOpen  int64            `json:"penalties_open"`
		Penalised      int64            `json:"penalised"`
	}
	if err := remarshal(raw, &in); err != nil {
		return nil, err
	}
	t := &TrustStats{Mode: f.Trust.String(), Run: in.Run, AsOf: in.AsOf, Stale: in.Stale, CentsLog10: []TrustBin{}, Bands: []TrustBand{},
		AboveVoteFloor: in.AboveVoteFloor, VoteFloorCents: in.VoteFloorCents, Sources: []TrustSource{}, History: in.History,
		PenaltiesOpen: in.PenaltiesOpen, Penalised: in.Penalised}
	if t.History == nil {
		t.History = []TrustRunPoint{}
	}
	for _, kind := range trust.SourceKinds {
		t.Sources = append(t.Sources, TrustSource{Kind: kind, Cents: in.Sources[kind]})
	}
	for i, n := range in.Bins {
		t.CentsLog10 = append(t.CentsLog10, TrustBin{From: i, To: i + 1, Accounts: n})
		t.Accounts += n
	}
	for _, band := range trustBands {
		t.Bands = append(t.Bands, TrustBand{Band: band, Accounts: in.Bands[band]})
	}
	return t, nil
}

// AllowanceAnswer is allowance.get's data as a page reads it: resources in
// the fixed order, whether the store lists them or keys them by name.
type AllowanceAnswer struct {
	Agent         string              `json:"agent"`
	Band          string              `json:"band"`
	Reason        string              `json:"reason"`
	StandingCents int64               `json:"-"`
	ParamsVersion int64               `json:"params_version"`
	Resources     []AllowanceResource `json:"resources"`
}

// AllowanceResource is one resource of an allowance today: its share is a
// floor part and a standing part. Prospective means the day's share has not
// been drawn yet: a read shows it, the first spend draws it.
type AllowanceResource struct {
	Resource     string `json:"resource"`
	Entitlement  int64  `json:"entitlement"`
	FloorPart    int64  `json:"floor_part"`
	StandingPart int64  `json:"standing_part"`
	Used         int64  `json:"used"`
	Remaining    int64  `json:"remaining"`
	Incoming     int64  `json:"incoming"`
	ResetsAt     int64  `json:"resets_at"`
	Prospective  bool   `json:"prospective"`
}

// AllowanceAnswerFrom reads allowance.get's result data.
func AllowanceAnswerFrom(data map[string]any) (*AllowanceAnswer, error) {
	var in struct {
		AllowanceAnswer
		Resources json.RawMessage `json:"resources"`
		Standing  *struct {
			Band   string `json:"band"`
			Cents  int64  `json:"cents"`
			Reason string `json:"reason"`
		} `json:"standing"`
	}
	if err := remarshal(data, &in); err != nil {
		return nil, err
	}
	a := in.AllowanceAnswer
	if st := in.Standing; st != nil {
		a.StandingCents = st.Cents
		if a.Band == "" {
			a.Band = st.Band
		}
		if a.Reason == "" {
			a.Reason = st.Reason
		}
	}
	if a.Band == "" {
		return nil, errStatsUnavailable
	}
	raw := bytes.TrimSpace(in.Resources)
	switch {
	case len(raw) > 0 && raw[0] == '[':
		if err := json.Unmarshal(raw, &a.Resources); err != nil {
			return nil, err
		}
	case len(raw) > 0 && raw[0] == '{':
		keyed := map[string]AllowanceResource{}
		if err := json.Unmarshal(raw, &keyed); err != nil {
			return nil, err
		}
		for name, r := range keyed {
			r.Resource = name
			a.Resources = append(a.Resources, r)
		}
	}
	sort.SliceStable(a.Resources, func(i, j int) bool {
		x, y := a.Resources[i].Resource, a.Resources[j].Resource
		return resourceRank(x) < resourceRank(y) || resourceRank(x) == resourceRank(y) && x < y
	})
	if a.Resources == nil {
		a.Resources = []AllowanceResource{}
	}
	return &a, nil
}

// TrustAnswer is the part of trust.get's standing breakdown (RFC 0021 §13)
// an agent page shows. Field paths match the API's, so a page cell names
// the JSON path it came from.
type TrustAnswer struct {
	Standing      *trust.StandingView `json:"standing"`
	World         []trust.WorldItem   `json:"world"`
	WorldCents    int64               `json:"world_cents"`
	OwnCents      int64               `json:"own_cents"`
	ReceivedCents int64               `json:"received_cents"`
	PassedCents   int64               `json:"passed_cents"`
	Backers       []trust.FlowPart    `json:"backers"`
	BackersTotal  int64               `json:"backers_total"`
	Accuracy      struct {
		PPM    int64 `json:"ppm"`
		Judged int64 `json:"judged"`
	} `json:"accuracy"`
	Penalty   *trust.PenaltyState  `json:"penalty"`
	Penalties []trust.PenaltyEntry `json:"penalties"`
	Caveats   []string             `json:"caveats"`
}

// agentBackersShown is how many backers an agent page lists, in the API's
// order (by flow); the API lists more.
const agentBackersShown = 5

// cell is one number on a page: Key is its JSON path in the API answer the
// page mirrors, Value the raw number and Text how it reads. The parity tests
// compare every data-key on a page with its API.
type cell struct {
	Label, Note string
	Key         string
	Value       int64
	Text        string
}

// allowanceSection is the /stats view of AllowanceStats.
type allowanceSection struct {
	Share *shareView
	Trust *trustView
	// TrustSection is /stats' Trust section (stats_trust.go).
	TrustSection *trustSection
	Unavailable  bool
}

type shareView struct {
	Sentence  string
	Shadow    bool
	Resources []resourceView
	// Legend names the slices the bars draw, each with its band's key.
	Legend    []chartKey
	Transfers []cell
	Services  []serviceRow
	Levers    []LeverState
	History   []historyRow
}

type resourceView struct {
	Title, Day string
	Summary    []cell
	Slices     []sliceRow
	// Anonymous is what anonymous subjects drew today, and how many.
	Anonymous []cell
	// Bar is today's budget split by slice (sliceBar), BarTip the same in
	// words for its tooltip; Spent and Budget the two numbers beside it.
	Bar           template.HTML
	BarTip        string
	Spent, Budget cell
}

type sliceRow struct {
	Name  string
	Fill  float64
	Cells []cell
}

type serviceRow struct {
	Service, Resource, Bucket, Subjects string
	Cells                               []cell
}

type historyRow struct {
	Day, Resource string
	Cells         []cell
}

type trustView struct {
	AsOf      int64
	Stale     bool
	Bins      []trustBinRow
	Bands     []trustBandRow
	VoteFloor cell
	Above     cell
}

type trustBinRow struct {
	Label string
	Width float64
	Cell  cell
}

type trustBandRow struct {
	Name string
	Cell cell
}

// buildAllowanceSection is the /stats allowance section, or nil with both
// the ledger and trust off.
func buildAllowanceSection(ctx context.Context, service board.Service, now time.Time) *allowanceSection {
	f := ServiceFeatures(service)
	stats, err := ReadAllowanceStats(ctx, service, f, AllowanceStatsDaysDefault, now)
	if err != nil {
		return &allowanceSection{Unavailable: true}
	}
	if stats == nil {
		return nil
	}
	return allowanceSectionFrom(stats)
}

func allowanceSectionFrom(stats *AllowanceStats) *allowanceSection {
	s := &allowanceSection{}
	if w := stats.Allowance; w != nil {
		v := &shareView{Sentence: w.Rule, Shadow: w.Ledger == "shadow", Levers: w.Levers}
		drawn := map[string]bool{}
		for i, r := range w.Resources {
			p := "allowance.resources." + strconv.Itoa(i) + "."
			rv := resourceView{Title: resourceLabel(r.Resource), Day: string(r.Day)}
			rv.Summary = []cell{
				{Label: "Budget", Note: "free " + r.Unit + "s for the day", Key: p + "budget", Value: r.Budget, Text: units(r.Resource, r.Budget)},
				{Label: "Issued", Note: "handed out by the slices so far", Key: p + "issued", Value: r.Issued, Text: units(r.Resource, r.Issued)},
				{Label: "Spent", Note: "used by writes today", Key: p + "spent", Value: r.Spent, Text: units(r.Resource, r.Spent)},
				{Label: "Not shared out", Note: "left over after every slice", Key: p + "unallocated", Value: r.Unallocated, Text: units(r.Resource, r.Unallocated)},
			}
			rv.Spent, rv.Budget = rv.Summary[2], rv.Summary[0]
			rv.Bar, rv.BarTip = sliceBar(r)
			rv.Anonymous = []cell{
				{Label: "Anonymous drew", Key: p + "anonymous.drawn", Value: r.Anonymous.Drawn, Text: units(r.Resource, r.Anonymous.Drawn)},
				{Label: "networks", Key: p + "anonymous.claimants", Value: r.Anonymous.Claimants, Text: count(r.Anonymous.Claimants)},
			}
			for j, sl := range r.Slices {
				q := p + "slices." + strconv.Itoa(j) + "."
				row := sliceRow{Name: sliceLabel(sl.Slice), Fill: float64(sl.FillPPM) / 10_000}
				row.Cells = []cell{
					{Key: q + "size", Value: sl.Size, Text: units(r.Resource, sl.Size)},
					{Key: q + "fill_ppm", Value: sl.FillPPM, Text: ppmPercent(sl.FillPPM)},
					{Key: q + "drawn", Value: sl.Drawn, Text: units(r.Resource, sl.Drawn)},
					{Key: q + "lent", Value: sl.Lent, Text: units(r.Resource, sl.Lent)},
					{Key: q + "borrowed", Value: sl.Borrowed, Text: units(r.Resource, sl.Borrowed)},
					{Key: q + "claimants", Value: sl.Claimants, Text: count(sl.Claimants)},
				}
				rv.Slices = append(rv.Slices, row)
				if sl.Size > 0 {
					drawn[sl.Slice] = true
				}
			}
			v.Resources = append(v.Resources, rv)
		}
		for _, name := range sliceOrder {
			if drawn[name] {
				cls := sliceClass(name)
				v.Legend = append(v.Legend, chartKey{Class: cls, Label: sliceLabel(name), Term: "share-rule", Area: true, Key: keySVG(cls, true)})
			}
		}
		tr := w.Transfers
		v.Transfers = []cell{
			{Label: "Transfers", Note: "today, between signed agents", Key: "allowance.transfers.count", Value: tr.Count, Text: count(tr.Count)},
			{Label: "Moved", Note: resourceLabel(tr.Resource) + ", today", Key: "allowance.transfers.volume", Value: tr.Volume, Text: units(tr.Resource, tr.Volume)},
			{Label: "Pending", Note: "held after an account change", Key: "allowance.transfers.pending", Value: tr.Pending, Text: count(tr.Pending)},
			{Label: "Largest recipient", Note: "share of the day's budget", Key: "allowance.transfers.largest_recipient_share_ppm", Value: tr.LargestRecipientShareppm, Text: ppmPercent(tr.LargestRecipientShareppm)},
		}
		for i, u := range w.Services {
			p := "allowance.services." + strconv.Itoa(i) + "."
			v.Services = append(v.Services, serviceRow{Service: u.Service, Resource: resourceLabel(u.Resource), Bucket: u.Bucket, Subjects: u.Subjects, Cells: []cell{
				{Key: p + "units", Value: u.Units, Text: units(u.Resource, u.Units)},
				{Key: p + "calls", Value: u.Calls, Text: count(u.Calls)},
			}})
		}
		for i, h := range w.History {
			p := "allowance.history." + strconv.Itoa(i) + "."
			v.History = append(v.History, historyRow{Day: string(h.Day), Resource: resourceLabel(h.Resource), Cells: []cell{
				{Key: p + "budget", Value: h.Budget, Text: units(h.Resource, h.Budget)},
				{Key: p + "issued", Value: h.Issued, Text: units(h.Resource, h.Issued)},
				{Key: p + "spent", Value: h.Spent, Text: units(h.Resource, h.Spent)},
				{Key: p + "claimants", Value: h.Claimants, Text: count(h.Claimants)},
			}})
		}
		s.Share = v
	}
	if t := stats.Trust; t != nil {
		v := &trustView{AsOf: t.AsOf, Stale: t.Stale,
			VoteFloor: cell{Key: "trust.vote_floor_cents", Value: t.VoteFloorCents, Text: cents(t.VoteFloorCents)},
			Above:     cell{Key: "trust.above_vote_floor", Value: t.AboveVoteFloor, Text: count(t.AboveVoteFloor)}}
		var peak int64
		for _, b := range t.CentsLog10 {
			peak = max(peak, b.Accounts)
		}
		for i, b := range t.CentsLog10 {
			width := 0.0
			if peak > 0 {
				width = float64(b.Accounts) * 100 / float64(peak)
			}
			v.Bins = append(v.Bins, trustBinRow{Label: binLabel(b), Width: width, Cell: cell{Key: "trust.cents_log10." + strconv.Itoa(i) + ".accounts", Value: b.Accounts, Text: count(b.Accounts)}})
		}
		for i, b := range t.Bands {
			v.Bands = append(v.Bands, trustBandRow{Name: capitalize(b.Band), Cell: cell{Key: "trust.bands." + strconv.Itoa(i) + ".accounts", Value: b.Accounts, Text: count(b.Accounts)}})
		}
		s.Trust = v
		s.TrustSection = buildTrustSection(t)
	}
	return s
}

// sliceClass is the band class of a slice in the allowance bar.
func sliceClass(slice string) string { return "b-" + strconv.Itoa(2*min(sliceRank(slice), 2)+1) }

// sliceBar draws one resource's budget today as a horizontal stack: each
// slice's size as a band, by the slice's lightness, and under it a thin ink
// line for the part handed out. What no slice holds is the empty track at
// the right. Like the charts it carries its own size and fills, so it draws
// without the stylesheet; tip says the same in words.
func sliceBar(r ResourceDay) (template.HTML, string) {
	var held int64
	for _, s := range r.Slices {
		held += max(s.Size, 0)
	}
	total := max(r.Budget, held, 1)
	var b strings.Builder
	b.WriteString(`<svg class="share-bar" width="100%" height="14" viewBox="0 0 1000 14" preserveAspectRatio="none" aria-hidden="true" focusable="false">`)
	b.WriteString(`<rect class="track" x="0" y="0" width="1000" height="10" fill="currentColor" fill-opacity=".06"/>`)
	parts := []string{}
	x := 0.0
	for _, s := range r.Slices {
		if s.Size <= 0 {
			continue
		}
		w := float64(s.Size) * 1000 / float64(total)
		cls := sliceClass(s.Slice)
		fmt.Fprintf(&b, `<rect class="band %s" x="%s" y="0" width="%s" height="10" fill="currentColor" fill-opacity="%s" vector-effect="non-scaling-stroke"/>`, cls, num(x), num(w), bandOpacity(cls))
		if s.FillPPM > 0 {
			fmt.Fprintf(&b, `<rect class="drawn" x="%s" y="11.5" width="%s" height="2.5" fill="currentColor"/>`, num(x), num(max(w*float64(s.FillPPM)/1_000_000, 2)))
		}
		parts = append(parts, sliceLabel(s.Slice)+" "+units(r.Resource, s.Size)+", "+ppmPercent(s.FillPPM)+" handed out")
		x += w
	}
	b.WriteString(`</svg>`)
	if r.Unallocated > 0 {
		parts = append(parts, "not shared out "+units(r.Resource, r.Unallocated))
	}
	tip := resourceLabel(r.Resource) + " today, by slice: " + strings.Join(parts, " · ") + "."
	if len(parts) == 0 {
		tip = resourceLabel(r.Resource) + " today: nothing shared out yet."
	}
	return template.HTML(b.String()), tip
}

func binLabel(b TrustBin) string {
	pow := func(e int) int64 {
		n := int64(1)
		for range e {
			n *= 10
		}
		return n
	}
	if b.From == 0 {
		return "under " + cents(pow(b.To))
	}
	return cents(pow(b.From)) + " to " + cents(pow(b.To))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// standingView is an agent page's allowance and trust sections.
type standingView struct {
	Agent     string
	Allowance *agentAllowance
	Trust     *agentTrust
	TrustOn   bool
	// Ways are the ways to raise its standing (standing.ways, standingways.go).
	Ways []standingWayView
}

type agentAllowance struct {
	Band, Reason string
	Resources    []agentResource
}

type agentResource struct {
	Label       string
	Prospective bool
	ResetsAt    int64
	Cells       []cell
}

type agentTrust struct {
	// Standing is the quiet line above the trust section: the score, the
	// dollar figure, and its own costs behind a disclosure.
	Standing     *agentStanding
	Band         string
	AsOf         int64
	Stale        bool
	Numbers      []cell
	Backers      []agentBacker
	BackersTotal cell
	Penalty      *trust.PenaltyState
	// Penalties is set when the public penalty log names the agent.
	Penalties bool
	Caveats   []string
}

type agentStanding struct {
	Score    cell // Value is the cents; Text the score, one decimal
	FakeCost string
	Note     string
	World    []agentWorldItem
}

type agentWorldItem struct {
	Kind, Source, State string
	Cents               cell
}

type agentBacker struct {
	Root  string
	Cents cell
}

// standingNote says what standing does today (RFC 0021 §14: every gate reads
// max(today's rule, standing)).
const standingNote = "It raises this agent's allowance share and vote weight above today's floors, never below them."

// agentStandingFrom is the agent page's standing line. Its data-key cells
// name the trust.get fields they show.
func agentStandingFrom(t *TrustAnswer) *agentStanding {
	v := t.Standing
	if v == nil {
		return nil
	}
	s := &agentStanding{FakeCost: trust.FakeCostText(v.StandingCents), Note: standingNote,
		Score: cell{Key: "standing.standing_cents", Value: v.StandingCents, Text: strconv.FormatFloat(math.Round(v.Standing*10)/10, 'f', 1, 64)}}
	for i, w := range t.World {
		s.World = append(s.World, agentWorldItem{Kind: w.Kind, Source: w.Source, State: w.State,
			Cents: cell{Key: "world." + strconv.Itoa(i) + ".cents", Value: w.Cents, Text: cents(w.Cents)}})
	}
	return s
}

// loadStanding reads an agent's allowance and trust through the same
// operations the API serves, or returns nil when both are off. The personal
// room's glance (full false) reads only the allowance and links the rest.
func loadStanding(execute func(board.Command) (board.Result, error), f board.Features, agent string, full bool) *standingView {
	v := &standingView{Agent: agent, TrustOn: f.Trust != board.TrustOff}
	if f.Ledger != board.LedgerOff {
		if res, err := execute(board.Command{Operation: "allowance.get", Target: agent}); err == nil {
			if a, err := AllowanceAnswerFrom(res.Data); err == nil {
				v.Allowance = agentAllowanceFrom(a)
			}
		}
	}
	if full && f.Trust != board.TrustOff {
		if res, err := execute(board.Command{Operation: "trust.get", Target: agent}); err == nil {
			var t TrustAnswer
			if remarshal(res.Data, &t) == nil && t.Standing != nil {
				v.Trust = agentTrustFrom(&t)
			}
		}
		if res, err := execute(board.Command{Operation: "standing.ways", Target: agent}); err == nil {
			v.Ways = standingWaysFrom(res.Data)
		}
	}
	if v.Allowance == nil && v.Trust == nil && len(v.Ways) == 0 && (full || !v.TrustOn) {
		return nil
	}
	return v
}

func agentAllowanceFrom(a *AllowanceAnswer) *agentAllowance {
	v := &agentAllowance{Band: a.Band, Reason: a.Reason}
	for i, r := range a.Resources {
		p := "resources." + strconv.Itoa(i) + "."
		v.Resources = append(v.Resources, agentResource{Label: resourceLabel(r.Resource), Prospective: r.Prospective, ResetsAt: r.ResetsAt, Cells: []cell{
			{Label: "Today's share", Key: p + "entitlement", Value: r.Entitlement, Text: units(r.Resource, r.Entitlement)},
			{Label: "Used", Key: p + "used", Value: r.Used, Text: units(r.Resource, r.Used)},
			{Label: "Left", Key: p + "remaining", Value: r.Remaining, Text: units(r.Resource, r.Remaining)},
			{Label: "Received", Key: p + "incoming", Value: r.Incoming, Text: units(r.Resource, r.Incoming)},
		}})
	}
	return v
}

func agentTrustFrom(t *TrustAnswer) *agentTrust {
	v := &agentTrust{Standing: agentStandingFrom(t), Band: t.Standing.Band, AsOf: t.Standing.AsOf, Stale: t.Standing.Stale,
		Numbers: []cell{
			{Label: "Own costs", Note: "what it paid here, priced", Key: "world_cents", Value: t.WorldCents, Text: cents(t.WorldCents)},
			{Label: "Kept", Note: "of its own costs, up to the cap", Key: "own_cents", Value: t.OwnCents, Text: cents(t.OwnCents)},
			{Label: "Received", Note: "passed to it by its backers", Key: "received_cents", Value: t.ReceivedCents, Text: cents(t.ReceivedCents)},
			{Label: "Passed on", Note: "to the agents it judged well", Key: "passed_cents", Value: t.PassedCents, Text: cents(t.PassedCents)},
		},
		BackersTotal: cell{Key: "backers_total", Value: t.BackersTotal, Text: count(t.BackersTotal)},
		Penalty:      t.Penalty,
		Penalties:    len(t.Penalties) > 0,
		Caveats:      t.Caveats,
	}
	for i, b := range t.Backers {
		if i == agentBackersShown {
			break
		}
		v.Backers = append(v.Backers, agentBacker{Root: b.Root, Cents: cell{Key: "backers." + strconv.Itoa(i) + ".cents", Value: b.Cents, Text: cents(b.Cents)}})
	}
	return v
}
