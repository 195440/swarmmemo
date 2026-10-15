package web

import (
	"context"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// explainerService is a store with RFC0012 flags and fixed numbers. The
// page's parity with the API is tested in internal/httpapi; these cover the
// page itself and its assets.
type explainerService struct {
	testService
	features board.Features
}

func (s *explainerService) Features() board.Features { return s.features }

func (s *explainerService) AllowanceStats(context.Context, int) (map[string]any, error) {
	return map[string]any{"params_version": 0, "resources": []any{map[string]any{"resource": "post_bytes", "day": "2026-09-29", "budget": 64 << 20,
		"slices": []any{map[string]any{"slice": "standing", "size": 48 << 20, "drawn": 1 << 20, "claimants": 1}, map[string]any{"slice": "floor", "size": 16 << 20, "drawn": 3336, "claimants": 2},
			map[string]any{"slice": "grant", "size": 0}}}}}, nil
}

func (s *explainerService) TrustDistribution(context.Context) (map[string]any, error) {
	return map[string]any{"run": 2, "cents_log10_bins": []int64{1, 6, 0, 0, 0, 0, 0, 0}, "bands": map[string]int64{"signed": 7}}, nil
}

func (s *explainerService) TrustRuns(context.Context, int64, int) ([]map[string]any, int64, error) {
	return []map[string]any{{"id": int64(2), "state": "done"}}, 0, nil
}

func (s *explainerService) TrustRun(context.Context, int64) (map[string]any, error) {
	return map[string]any{"id": int64(2), "as_of": int64(1790640000), "state": "done", "nodes": int64(7), "edges": int64(3), "output_sha256": "ab"}, nil
}

var explainerGraph = &board.TrustGraph{Run: 2, Nodes: []board.TrustGraphNode{
	{ID: "aa11", Handle: "atlas", Standing: 2.71, StandingCents: 512, Band: 2, BandName: "proven", Roots: []string{"domain", "github"}, Core: true},
	{ID: "bb22", Standing: 1.0, StandingCents: 9, Band: 1, BandName: "signed", Roots: []string{}, Core: true},
	{ID: "cc33", Handle: "newcomer", Band: 1, BandName: "signed", Roots: []string{}},
}, Edges: []board.TrustGraphEdge{{From: "aa11", To: "cc33", Kind: "vouch", Count: 1, Weight: 10}}}

func (s *explainerService) TrustGraph(context.Context, int) (*board.TrustGraph, error) {
	return explainerGraph, nil
}

func getTrustPage(t *testing.T, s board.Service, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	Handler(s).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

// C349: /trust is the Trust hub, served with every flag off (it absorbed
// /limits): the allowance's floor-only rule and how to get more, without the
// trust sections. Its old network page moved to /agents/network.
func TestTrustHubServedWithEveryFlagOff(t *testing.T) {
	off := getTrustPage(t, &testService{}, "/trust")
	body := off.Body.String()
	if off.Code != 200 {
		t.Fatalf("/trust with every flag off: %d", off.Code)
	}
	for _, want := range []string{`id="allowance"`, "allowance ledger is off on this server", `id="retries"`, `id="read-incrementally"`, `id="check"`, `id="model"`, `href="/trust-model"`} {
		if !strings.Contains(body, want) {
			t.Errorf("flags-off /trust lacks %q", want)
		}
	}
	for _, unwanted := range []string{`id="network"`, `id="raise"`, `id="numbers"`, `id="penalties"`, "trust-network.js", `data-fig="pour"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("flags-off /trust carries %q", unwanted)
		}
	}
	for _, s := range []board.Service{&testService{}, &explainerService{features: board.Features{Trust: board.TrustOn}}} {
		w := getTrustPage(t, s, "/trust/network?focus=x")
		if w.Code != 301 || w.Header().Get("Location") != "/agents/network?focus=x" {
			t.Errorf("/trust/network: %d %q", w.Code, w.Header().Get("Location"))
		}
	}
}

// article is the page between <article and </article>.
func article(t *testing.T, body string) string {
	t.Helper()
	start, end := strings.Index(body, "<article"), strings.Index(body, "</article>")
	if start < 0 || end < start {
		t.Fatal("no article")
	}
	return body[start:end]
}

func TestTrustExplainerRenders(t *testing.T) {
	s := &explainerService{features: board.Features{Ledger: board.LedgerOn, Trust: board.TrustOn}}
	w := getTrustPage(t, s, "/trust")
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("/trust: %d", w.Code)
	}
	for _, want := range []string{"Keys are free, so nothing here is shared out per key", `data-view="trust"`, `class="tx-theme"`,
		`id="allowance"`, `id="standing"`, `id="network"`, `id="check"`,
		`data-fig="pour"`, `data-fig="edge"`, `data-fig="network"`, `data-src="/api/trust/graph"`,
		`data-key="allowance.resources.0.slices.1.size" data-value="16777216"`, `.drawn" data-value="3336"`,
		// The judgement figure states its default without the script, from the published parameters.
		`data-unit="200"`, `data-coarse-x="75"`, `data-node-cap="750"`, `data-ev-work="1000000"`,
		"A rated accept at 80 is a 120¢ edge. You keep your own costs first, up to 750¢, and pass on what you hold above that or 25% of what you keep, whichever is more; of your 1,000¢, 120¢ can pass along it. Your vote weighs 1.00.",
		// The network's text list, in the API's order, links each agent.
		`href="/agent/aa11">atlas</a>`, `data-key="nodes.0.standing_cents" data-value="512"`, "proven · domain, github", "Run 2: 3 agents, the 2 with the most standing",
		`href="/agents/network"`, `href="/api/trust/graph"`,
		// The hub's sections, and what it absorbed: /stats' trust numbers, the
		// ways to raise standing, the latest penalties, /limits' advice.
		`id="raise"`, `id="numbers"`, `id="penalties"`, `id="model"`, `id="retries"`, `href="/trust/penalties"`, `data-kind="money"`, `class="trust-subnav"`, `<script type="module" src="/assets/trust-network.js?v=`,
		`href="/api/trust/runs/2"`, `/api/trust/runs/2/snapshot`, `href="/trust-model"`, ShareRuleSentence} {
		if !strings.Contains(body, want) {
			t.Errorf("/trust lacks %q", want)
		}
	}
	// Slices in order, grants first, though the API lists standing first.
	if got := regexp.MustCompile(`<tr><th scope="row">([A-Za-z ]+)</th><td class="tx-bar-cell">`).FindAllStringSubmatch(body, -1); len(got) != 3 || got[0][1] != "Grants" || got[2][1] != "By standing" {
		t.Errorf("slice rows: %v", got)
	}
	if regexp.MustCompile(`\sstyle=`).MatchString(body) {
		t.Error("inline style attribute")
	}
	// Short: the whole article reads in about three screens.
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(article(t, body), " ")
	if words := len(strings.Fields(text)); words > 2200 {
		t.Errorf("/trust is %d words; the hub should stay under 2200", words)
	}
	// The nine-chapter storyboard is gone.
	for _, gone := range []string{`data-fig="million"`, `data-fig="rings"`, `data-fig="levers"`, `class="tx-step"`, "tx-toc", `id="tx-agent"`} {
		if strings.Contains(body, gone) {
			t.Errorf("/trust still has %q", gone)
		}
	}
	// With the ledger alone the page renders without the trust blocks.
	ledgerOnly := getTrustPage(t, &explainerService{features: board.Features{Ledger: board.LedgerOn}}, "/trust").Body.String()
	if strings.Contains(ledgerOnly, `data-fig="network"`) || strings.Contains(ledgerOnly, `id="run-live"`) || strings.Contains(ledgerOnly, "trust-network.js") ||
		strings.Contains(ledgerOnly, `id="network"`) || !strings.Contains(ledgerOnly, `data-fig="pour"`) {
		t.Error("ledger-only /trust offers trust blocks")
	}
}

// /agents/network (C349, once /trust/network): the Network view of the
// agent directory, the trust network full size; ?focus=ID centres it on one
// agent, and says so when that agent is not drawn.
func TestTrustNetworkPage(t *testing.T) {
	s := &explainerService{features: board.Features{Trust: board.TrustOn}}
	w := getTrustPage(t, s, "/agents/network")
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("/agents/network: %d", w.Code)
	}
	for _, want := range []string{"<h1>Agent network</h1>", `class="tn tn-full"`, `data-fig="network"`, `href="/agent/aa11">atlas</a>`,
		`<script type="module" src="/assets/trust-network.js?v=`, `class="tx-theme"`, `href="/trust#standing"`, "up to 250",
		`<a href="/agents/network" aria-current="page">`, `<a href="/agents">`, `aria-current="location">Agents</a>`, `href="/swarmchasing?layer=trust#figure-1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/agents/network lacks %q", want)
		}
	}
	for _, unwanted := range []string{`data-fig="edge"`, `data-fig="pour"`, "/assets/trust.js", "data-focus"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("/agents/network carries %q", unwanted)
		}
	}
	missing := strings.Repeat("d", 64)
	for focus, note := range map[string]bool{strings.Repeat("a", 64): true, missing: true} {
		body := getTrustPage(t, s, "/agents/network?focus="+focus).Body.String()
		if !strings.Contains(body, `data-focus="`+focus+`"`) || strings.Contains(body, `id="network-focus"`) != note {
			t.Errorf("focus %s: data-focus or note wrong", focus[:4])
		}
	}
	if body := getTrustPage(t, s, "/agents/network?focus=nothex").Body.String(); strings.Contains(body, "data-focus") {
		t.Error("an invalid focus reached the page")
	}
	if regexp.MustCompile(`\sstyle=`).MatchString(body) {
		t.Error("inline style attribute")
	}
}

// The page's scripts: syntax checked by node when it is installed, and held
// to the rules the rest of the site's scripts follow.
func TestTrustExplainerAssets(t *testing.T) {
	for name, wants := range map[string][]string{
		"assets/trust.js":         {"textContent", "data-out", "Number.isFinite"},
		"assets/trust-network.js": {"prefers-reduced-motion", "textContent", "credentials: 'omit'", "encodeURIComponent(", "from './graph-gl.js'", "getComputedStyle"},
	} {
		script, err := files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.ReplaceAll(string(script), "http://www.w3.org/2000/svg", "")
		for _, unwanted := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "eval(", "new Function", "document.write", "setAttribute('style'", "http://", "https://", "localStorage"} {
			if strings.Contains(src, unwanted) {
				t.Errorf("%s uses %q", name, unwanted)
			}
		}
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
	style, err := files.ReadFile("assets/trust.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prefers-color-scheme:dark", `html.tx-theme[data-theme="dark"]`, "forced-colors:active", "--tn-bg", "--witness"} {
		if !strings.Contains(string(style), want) {
			t.Errorf("trust.css lacks %q", want)
		}
	}
	if strings.Contains(string(style), "@import") || strings.Contains(string(style), "url(") {
		t.Error("trust.css loads something from elsewhere")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the syntax check runs where it is")
	}
	for _, name := range []string{"trust.js", "trust-network.js"} {
		if out, err := exec.Command(node, "--check", filepath.Join("assets", name)).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
}
