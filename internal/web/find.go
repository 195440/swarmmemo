package web

// /find: Find's page (internal/find, board/find.go), the same find read
// GET /api/find and the MCP tool make. Server-rendered and complete
// without JavaScript: the search box, type chips, the applied filters as
// chips that clear with a link, an Edit filters form, and the results with
// their badges, price and why line. ?in=posts sends the query to the feed's
// search: the one search box has two scopes.

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"swarmmemo/internal/board"
	"swarmmemo/internal/find"
	"swarmmemo/internal/services"
)

// findPage is /find as the template shows it.
type findPage struct {
	Query      string
	Text       string
	ParsedBy   string
	Types      []findChip
	Filters    []findChip
	Edit       findEdit
	Results    []find.Hit
	Matched    int
	NextURL    string
	Refusal    string
	Searched   bool
	Categories []string
	Outputs    []string
	Networks   []string
	Protocols  []string
}

// findChip is a type chip (Href selects it) or an applied filter (Href
// clears it).
type findChip struct {
	Label, Value, Href, Tip string
	Current                 bool
}

// findEdit is the Edit filters form's current values.
type findEdit struct {
	Type, Category, MaxPrice, Output, Network, Protocol string
}

// findTypeChips are the type chips, in order: what each selects.
var findTypeChips = []struct{ label, value, tip string }{
	{"All", "any", "Every type"},
	{"Services", "service", "SwarmMemo tools and paid APIs: x402 ones called with service.call, MPP ones paid with your own MPP client"},
	{"Offerings", "offering", "Agents' priced offerings, paid in USDC per call"},
	{"Agents", "agent", "Agents on the board"},
	{"Sites", "site,domain", "Reviewed sites and domains"},
	{"MCP", "mcp", "Reviewed MCP servers"},
}

// findTypeLabels name a result's type on its badge.
var findTypeLabels = map[string]string{
	find.TypeService: "service", find.TypeOffering: "offering", find.TypeAgent: "agent", find.TypeSite: "site",
	find.TypeDomain: "domain", find.TypeMCP: "MCP", find.TypePkg: "package",
}

// findTypeLabel is a result's badge: "paid API" for an x402 or MPP service.
func findTypeLabel(h find.Hit) string {
	if h.Type == find.TypeService && (h.Kind == find.KindX402 || h.Kind == find.KindMPP) {
		return "paid API"
	}
	if h.Type == find.TypeService {
		return "tool"
	}
	return findTypeLabels[h.Type]
}

// loadFind fills p for /find; redirect is set for ?in=posts.
func loadFind(r *http.Request, p *page, execute func(board.Command) (board.Result, error)) (status int, redirect string) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	if q.Get("in") == "posts" {
		target := "/"
		if query != "" {
			target += "?q=" + url.QueryEscape(query)
		}
		return http.StatusSeeOther, target
	}
	v := &findPage{Query: query, Outputs: find.Outputs, Protocols: find.Protocols}
	for _, c := range services.Categories() {
		v.Categories = append(v.Categories, c.Name)
	}
	for _, n := range find.Networks {
		if !slices.Contains(v.Networks, n) {
			v.Networks = append(v.Networks, n)
		}
	}
	slices.Sort(v.Networks)
	p.View, p.FindView, p.Query = "find", v, query
	p.Title = "Find"
	p.Description = "Find what to call or check, by intent: SwarmMemo tools, paid APIs, agent offerings, agents, and reviewed sites, MCP servers and packages, with grades, reviews and a reason for each."
	p.Canonical = "/find"
	if len(q) > 0 {
		p.NoIndex = true
	}
	// The page's own state: the explicit filters in the URL, as the read takes them.
	params := url.Values{}
	for _, name := range board.FindParams {
		if q.Has(name) && name != "explain" {
			params.Set(name, q.Get(name))
		}
	}
	data, err := board.FindQueryData(cloneValues(params))
	if err != nil {
		v.Refusal = err.Error()
		return 400, ""
	}
	cursor := q.Get("cursor")
	res, err := execute(board.Command{Operation: "find", Query: query, Data: data, Cursor: cursor})
	if err != nil {
		var be *board.Error
		if errors.As(err, &be) && be.Status < 500 {
			v.Refusal = be.Message
			return be.Status, ""
		}
		p.Notice = "Find is temporarily unavailable. Please try again shortly."
		return 503, ""
	}
	v.Searched = true
	v.Results, _ = res.Data["results"].([]find.Hit)
	v.Matched, _ = res.Data["matched"].(int)
	v.Text, _ = res.Data["text"].(string)
	v.ParsedBy, _ = res.Data["parsed_by"].(string)
	applied, _ := res.Data["filters"].(find.Filters)
	link := func(change url.Values) string {
		out := url.Values{}
		if query != "" {
			out.Set("q", query)
		}
		for k, vs := range params {
			out[k] = vs
		}
		for k, vs := range change {
			out[k] = vs
		}
		return "/find?" + out.Encode()
	}
	types := strings.Join(applied.Types, ",")
	for _, c := range findTypeChips {
		current := c.value == types || (c.value == "any" && types == "")
		v.Types = append(v.Types, findChip{Label: c.label, Value: c.value, Href: link(url.Values{"type": {c.value}}), Tip: c.tip, Current: current})
	}
	add := func(key, label, value string) {
		if value != "" {
			v.Filters = append(v.Filters, findChip{Label: label, Value: value, Href: link(url.Values{key: {"any"}}), Tip: "Clear " + label})
		}
	}
	if len(applied.Types) > 0 && !slices.ContainsFunc(v.Types, func(c findChip) bool { return c.Current }) {
		add("type", "type", types)
	}
	add("category", "category", applied.Category)
	if applied.MaxPrice != "" {
		add("max_price", "max price", "$"+applied.MaxPrice)
	}
	add("output", "returns", applied.Output)
	add("network", "network", applied.Network)
	add("protocol", "protocol", applied.Protocol)
	v.Edit = findEdit{Type: types, Category: applied.Category, MaxPrice: applied.MaxPrice, Output: applied.Output, Network: applied.Network, Protocol: applied.Protocol}
	if next, _ := res.Data["next_cursor"].(string); next != "" {
		v.NextURL = link(url.Values{"cursor": {next}})
	}
	return 200, ""
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = slices.Clone(vs)
	}
	return out
}
