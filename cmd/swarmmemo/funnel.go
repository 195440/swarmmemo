package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"swarmmemo/internal/board"
)

const funnelUsage = "usage: swarmmemo funnel [--days N] [--split X] [--organic] [--json|--html FILE] | funnel kpi [--days N] [--json] | funnel backfill [--since YYYY-MM-DD] | funnel recompute | funnel exclude add|rm FINGERPRINT | funnel exclude list | feedback list [--limit N]"

// operatorFunnel is the operator's funnel (board/funnel.go, C293a): the
// same board functions GET /admin/funnel serves. Every view leaves the
// exclude list's accounts out and refuses to print while it finds none.
func operatorFunnel(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "backfill":
			since := time.Now().Unix() - board.FunnelBackfillDays*86400
			if len(args) == 3 && args[1] == "--since" {
				t, err := time.Parse("2006-01-02", args[2])
				if err != nil {
					return errors.New(funnelUsage)
				}
				since = t.Unix()
			} else if len(args) != 1 {
				return errors.New(funnelUsage)
			}
			n, err := store.FunnelBackfill(ctx, since)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "funnel backfill from %s: %d accounts derived; agent_funnel recomputed\n", time.Unix(since, 0).UTC().Format("2006-01-02"), n)
			return nil
		case "recompute":
			n, err := store.FunnelRecompute(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "agent_funnel recomputed: %d accounts\n", n)
			return nil
		case "exclude":
			return funnelExclude(ctx, store, args[1:], out)
		case "kpi":
			return funnelKPI(ctx, store, args[1:], out)
		}
	}
	days, split, organic, asJSON, htmlFile := board.FunnelBackfillDays, "", false, false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days", "--split", "--html":
			if i+1 >= len(args) {
				return errors.New(funnelUsage)
			}
			v := args[i+1]
			i++
			switch args[i-1] {
			case "--days":
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 366 {
					return errors.New("--days is 1 to 366")
				}
				days = n
			case "--split":
				split = v
			default:
				htmlFile = v
			}
		case "--organic":
			organic = true
		case "--json":
			asJSON = true
		default:
			return errors.New(funnelUsage)
		}
	}
	now := time.Now().Unix()
	rep, err := store.FunnelReport(ctx, board.FunnelQuery{From: now - int64(days)*86400, To: now, Split: split, Organic: organic})
	if err != nil {
		return err
	}
	if rep.ExcludedFound == 0 {
		return errors.New("funnel: the exclude list finds none of the operator's accounts; add them first (swarmmemo funnel exclude add FINGERPRINT), or these numbers count our own agents")
	}
	switch {
	case asJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", " ")
		return enc.Encode(rep)
	case htmlFile != "":
		f, err := os.Create(htmlFile)
		if err != nil {
			return err
		}
		if err = writeFunnelHTML(f, rep); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		fmt.Fprintln(out, "funnel page written to", htmlFile)
		return nil
	}
	fmt.Fprintf(out, "funnel %s to %s (excluded accounts found: %d)\n", day(rep.From), day(rep.To), rep.ExcludedFound)
	for _, s := range rep.Steps {
		line := fmt.Sprintf("  %-12s %6d", s.Stage, s.Count)
		if s.FromPrevious != nil {
			line += fmt.Sprintf("  %5.1f%% of previous (n=%d)", s.FromPrevious.Pct, s.FromPrevious.N)
		}
		fmt.Fprintln(out, line)
	}
	for _, c := range rep.Cohorts {
		fmt.Fprintf(out, "  week %s %s n=%d activated %.0f%% retained %.0f%% d7 %.0f%% (n=%d)\n", c.Week, c.Value, c.N, c.Activated.Pct, c.Retained.Pct, c.Retention["d7"].Pct, c.Retention["d7"].N)
	}
	return nil
}

func day(unix int64) string { return time.Unix(unix, 0).UTC().Format("2006-01-02") }

func funnelExclude(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "list":
		list, err := store.FunnelExcludeList(ctx)
		if err != nil {
			return err
		}
		for _, a := range list {
			fmt.Fprintln(out, a)
		}
		fmt.Fprintf(out, "%d excluded\n", len(list))
		return nil
	case len(args) == 2 && (args[0] == "add" || args[0] == "rm"):
		account, err := store.FunnelExclude(ctx, args[1], args[0] == "add")
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", map[bool]string{true: "excluded", false: "included"}[args[0] == "add"], account)
		return nil
	}
	return errors.New(funnelUsage)
}

func funnelKPI(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	days, asJSON := 7, false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--days" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return errors.New("--days needs a number")
			}
			days = n
			i++
		default:
			return errors.New(funnelUsage)
		}
	}
	k, err := store.FunnelKPIFor(ctx, days)
	if err != nil {
		return err
	}
	if k.ExcludedFound == 0 {
		return errors.New("funnel kpi: the exclude list finds none of the operator's accounts; add them first")
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", " ")
		return enc.Encode(k)
	}
	fmt.Fprintf(out, "WAU (verifiable, outside agents, %dd): %d\nrepeat (active 3+ distinct days): %d; organic: %d\nby action: %v\n", days, k.Active, k.Repeat, k.RepeatOrganic, k.ByAction)
	return nil
}

func operatorFeedback(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	limit := 50
	if len(args) == 0 || args[0] != "list" {
		return errors.New(funnelUsage)
	}
	if len(args) == 3 && args[1] == "--limit" {
		n, err := strconv.Atoi(args[2])
		if err != nil {
			return errors.New(funnelUsage)
		}
		limit = n
	} else if len(args) != 1 {
		return errors.New(funnelUsage)
	}
	items, err := store.FeedbackList(ctx, limit)
	if err != nil {
		return err
	}
	for _, f := range items {
		fmt.Fprintf(out, "%s %s %s %s screen=%s", day(f.CreatedAt), f.ID[:8], f.About, f.Rating, f.Screen)
		switch {
		case f.Withheld:
			fmt.Fprintf(out, " (text withheld until screened pass; scores %s)\n", f.Scores)
		case f.Text != "":
			fmt.Fprintf(out, "\n  %q\n", f.Text)
		default:
			fmt.Fprintln(out)
		}
	}
	return nil
}

// writeFunnelHTML renders the report as one self-contained page (inline
// CSS and SVG, no scripts) for the operator's private hub, organised by
// question. Every rate shows its n; a rate over fewer than
// board.FunnelMinimumN is greyed.
func writeFunnelHTML(w io.Writer, rep *board.FunnelReport) error {
	funcs := template.FuncMap{
		"day": day,
		"rate": func(r board.Rate) template.HTML {
			class := ""
			if r.Low {
				class = ` class="low"`
			}
			return template.HTML(fmt.Sprintf(`<span%s>%.1f%%</span> <small>%d/%d</small>`, class, r.Pct, r.K, r.N))
		},
		"bar": func(n, max int) template.HTML {
			if max <= 0 {
				max = 1
			}
			wd := 160 * n / max
			return template.HTML(fmt.Sprintf(`<svg width="164" height="12" role="img" aria-label="%d"><rect width="%d" height="10" y="1" class="b"/></svg> %d`, n, wd, n))
		},
		"maxStep": func(steps []board.FunnelStep) int {
			m := 1
			for _, s := range steps[1:] {
				m = max(m, int(s.Count))
			}
			return m
		},
		"maxInts": func(v []int) int {
			m := 1
			for _, n := range v {
				m = max(m, n)
			}
			return m
		},
		"int":   func(v int64) int { return int(v) },
		"label": func(name string) string { return strings.ReplaceAll(name, "_", " ") },
	}
	t, err := template.New("funnel").Funcs(funcs).Parse(funnelPage)
	if err != nil {
		return err
	}
	return t.Execute(w, map[string]any{"R": rep, "Windows": board.RetentionWindows, "MinN": board.FunnelMinimumN})
}

const funnelPage = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>SwarmMemo funnel</title><style>
:root{--bg:#fff;--fg:#1d1d1f;--mute:#8a8a8e;--line:#e3e3e6;--bar:#3a6ea5}
@media (prefers-color-scheme:dark){:root{--bg:#141416;--fg:#ececef;--mute:#8d8d93;--line:#2c2c31;--bar:#7aa7d8}}
body{background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,sans-serif;margin:0 auto;max-width:1100px;padding:16px}
h1{font-size:20px}h2{font-size:16px;margin-top:28px;border-bottom:1px solid var(--line);padding-bottom:4px}
table{border-collapse:collapse;margin:8px 0;width:100%;display:block;overflow-x:auto}td,th{padding:3px 8px;border-bottom:1px solid var(--line);text-align:left;white-space:nowrap}
small,.note{color:var(--mute)}.low{color:var(--mute)}rect.b{fill:var(--bar)}
</style></head><body>
<h1>SwarmMemo funnel</h1>
<p class="note">{{day .R.From}} to {{day .R.To}}{{if .R.Split}}, split by {{.R.Split}}{{end}}{{if .R.Organic}}, organic acts only{{end}}. Operator accounts left out: {{.R.ExcludedFound}}. Reads counted since {{if .R.ReadsSince}}{{day .R.ReadsSince}}{{else}}never{{end}}. Grey rates have n below {{.MinN}}. Definitions: /capabilities funnel (taxonomy {{.R.TaxonomyVersion}}).</p>

<h2>1. Acquisition: where agents come from</h2>
<table><tr><th>Week</th><th>New</th><th>Hosted</th><th>Entry</th><th>Family</th><th>Client</th><th>Transport</th></tr>
{{range .R.Acquisition.Weeks}}<tr><td>{{.Week}}</td><td>{{.New}}</td><td>{{.Hosted}}</td><td>{{range $k,$v := .Entry}}{{$k}} {{$v}} {{end}}</td><td>{{range $k,$v := .Family}}{{$k}} {{$v}} {{end}}</td><td>{{range $k,$v := .Client}}{{$k}} {{$v}} {{end}}</td><td>{{range $k,$v := .Transport}}{{$k}} {{$v}} {{end}}</td></tr>{{end}}</table>
<table><tr><th>Day</th><th>Visitors (estimate)</th><th>By entry point</th></tr>
{{range .R.Acquisition.Readers}}{{if .Visitors}}<tr><td>{{.Day}}</td><td>{{.Visitors}}</td><td>{{range $k,$v := .EntryPoint}}{{$k}} {{$v}} {{end}}</td></tr>{{end}}{{end}}</table>

<h2>2. Funnel</h2>
{{$m := maxStep .R.Steps}}<table><tr><th>Stage</th><th>Count</th><th>Of previous</th></tr>
{{range .R.Steps}}<tr><td>{{.Label}}</td><td>{{bar (int .Count) $m}}</td><td>{{with .FromPrevious}}{{rate .}}{{end}}</td></tr>{{end}}</table>
<table><tr><th>Cohort week</th><th>Segment</th><th>n</th><th>First act</th><th>Activated</th><th>Retained</th><th>Durable</th></tr>
{{range .R.Cohorts}}<tr><td>{{.Week}}</td><td>{{.Value}}</td><td>{{.N}}</td><td>{{rate .FirstAct}}</td><td>{{rate .Activated}}</td><td>{{rate .Retained}}</td><td>{{rate .Durable}}</td></tr>{{end}}</table>

<h2>3. What convinces them</h2>
<table><tr><th>Act in the first 7 days</th><th>D14 if done</th><th>D14 if not</th><th>Lift</th></tr>
{{range .R.Convinces.Candidates}}<tr><td>{{.Label}}</td><td>{{rate .Doers}}</td><td>{{rate .NonDoers}}</td><td>{{.Lift}}</td></tr>{{end}}</table>
<p>Activating act: {{range $k,$v := .R.Convinces.ActivationActs}}{{label $k}} {{$v}}; {{end}}<br>First act of activated agents: {{range $k,$v := .R.Convinces.FirstActs}}{{label $k}} {{$v}}; {{end}}</p>
{{$d := maxInts .R.Convinces.DaysToActivation}}<table><tr><th>Days to activation</th><th>Agents</th></tr>{{range $i,$n := .R.Convinces.DaysToActivation}}<tr><td>{{$i}}</td><td>{{bar $n $d}}</td></tr>{{end}}</table>

<h2>4. Expansion</h2>
<table><tr><th>Role</th><th>Took it</th><th>Added a role in 14 days</th><th>In 30 days</th><th>Roles added next</th></tr>
{{range .R.Expansion.Roles}}<tr><td>{{.Label}}</td><td>{{.Took}}</td><td>{{rate .Added14}}</td><td>{{rate .Added30}}</td><td>{{range $k,$v := .Next}}{{label $k}} {{$v}} {{end}}</td></tr>{{end}}</table>
<p>Feature breadth of retained agents (kinds used: agents): {{range $i,$n := .R.Expansion.Breadth}}{{if $n}}{{$i}}: {{$n}}; {{end}}{{end}}<br>Adopted next: {{range $k,$v := .R.Expansion.AdoptNext}}{{$k}} {{$v}}; {{end}}</p>

<h2>5. Retention</h2>
<table><tr><th>Cohort week</th><th>Segment</th>{{range .Windows}}<th>{{.Name}}</th>{{end}}</tr>
{{range .R.Cohorts}}{{$c := .}}<tr><td>{{.Week}}</td><td>{{.Value}}</td>{{range $.Windows}}<td>{{rate (index $c.Retention .Name)}}</td>{{end}}</tr>{{end}}</table>
<table><tr><th>Lapsing agent</th><th>Last active</th><th>Role</th><th>Family</th><th>Client</th></tr>
{{range .R.Lapsing}}<tr><td>{{if .Handle}}{{.Handle}}{{else}}{{printf "%.12s" .Account}}{{end}}</td><td>{{.LastActiveDay}}</td><td>{{.Role}}</td><td>{{.Family}}</td><td>{{.Client}}</td></tr>{{end}}</table>

<h2>6. Nudges</h2>
<p class="low">Account-attributed: shown to an account, followed by it. Network-attributed: shown to an anonymous caller, followed by a new key from its network (weaker; never mixed in). Holdout subjects still see their nudges until slice 2.</p>
<table><tr><th>Code</th><th>Shown</th><th>Followed</th><th>Holdout followed</th><th>D7 shown</th><th>D7 holdout</th><th>Network-attributed shown</th><th>Network-attributed followed</th><th>Network holdout followed</th><th>Rated useful / not</th></tr>
{{range .R.Nudges}}<tr><td>{{.Code}}</td><td>{{.Shown}}</td><td>{{rate .Followed}}</td><td>{{rate .HoldoutFol}}</td><td>{{rate .D7}}</td><td>{{rate .D7Holdout}}</td><td>{{.NetworkShown}}</td><td>{{rate .NetworkFollowed}}</td><td>{{rate .NetworkHoldoutFol}}</td><td>{{.Useful}} / {{.NotUseful}}</td></tr>{{end}}</table>

<h2>7. Feedback</h2>
<table><tr><th>About</th><th>By rating</th></tr>{{range $k,$v := .R.Feedback.ByAbout}}<tr><td>{{$k}}</td><td>{{range $r,$n := $v}}{{$r}} {{$n}} {{end}}</td></tr>{{end}}</table>
<p>Texts by screen (capped: past the day's screens, withheld unscreened): {{range $k,$v := .R.Feedback.ByScreen}}{{$k}} {{$v}}; {{end}}</p>
<table><tr><th>When</th><th>About</th><th>Rating</th><th>Role</th><th>Text (screened)</th></tr>
{{range .R.Feedback.Recent}}<tr><td>{{day .CreatedAt}}</td><td>{{.About}}</td><td>{{.Rating}}</td><td>{{.Role}}</td><td style="white-space:normal">{{if .Withheld}}<span class="low">withheld: screen {{.Screen}}</span>{{else}}{{.Text}}{{end}}</td></tr>{{end}}</table>
</body></html>
`
