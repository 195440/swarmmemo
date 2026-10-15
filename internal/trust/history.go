package trust

import (
	"context"
	"encoding/json"
	"sort"

	"swarmmemo/internal/allowance"
)

// The reads behind the trust surfaces' charts (RFC 0021 §13): the recent
// runs' totals (/stats decentralisation, the operator dashboard), one
// account's standing and accuracy over recent runs (the breakdown's
// trend), and where the latest run's standing comes from, by kind (/stats).
// Each is a bounded read of stored run outputs; none recomputes anything.

// HistoryRunsMax bounds how many recent runs a history read covers.
const HistoryRunsMax = 60

// RunPoint is one done run of the one trust graph with its stored totals.
type RunPoint struct {
	Run     int64   `json:"run"`
	AsOf    int64   `json:"as_of"`
	Summary Summary `json:"summary"`
}

// RecentRuns lists the last limit done runs of the one trust graph, oldest
// first, with their stored summaries.
func RecentRuns(ctx context.Context, q allowance.Querier, limit int) ([]RunPoint, error) {
	limit = min(max(limit, 1), HistoryRunsMax)
	rows, err := q.QueryContext(ctx, "SELECT id,as_of,capture_bound FROM trust_runs WHERE state='done' AND params_version>=? ORDER BY id DESC LIMIT ?", UnifiedVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunPoint{}
	for rows.Next() {
		var p RunPoint
		var raw string
		if err = rows.Scan(&p.Run, &p.AsOf, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &p.Summary) != nil {
			continue
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Run < out[j].Run })
	return out, nil
}

// AccountPoint is one account's standing and accuracy in one run.
type AccountPoint struct {
	Run           int64 `json:"run"`
	AsOf          int64 `json:"as_of"`
	StandingCents int64 `json:"standing_cents"`
	AccuracyPPM   int64 `json:"accuracy_ppm"`
	Judged        int64 `json:"judged"`
}

// AccountHistory is an account's standing and accuracy over its last limit
// done runs of the one trust graph, oldest first (runs without a row for it
// are absent: standing 0 there).
func AccountHistory(ctx context.Context, q allowance.Querier, account string, limit int) ([]AccountPoint, error) {
	limit = min(max(limit, 1), HistoryRunsMax)
	rows, err := q.QueryContext(ctx, `SELECT s.run_id,r.as_of,s.collateral,coalesce(json_extract(s.parts,'$.accuracy_ppm'),1000000),coalesce(json_extract(s.parts,'$.judged'),0)
 FROM trust_scores s JOIN trust_runs r ON r.id=s.run_id WHERE s.account=? AND r.state='done' AND r.params_version>=? ORDER BY s.run_id DESC LIMIT ?`, account, UnifiedVersion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountPoint{}
	for rows.Next() {
		var p AccountPoint
		if err = rows.Scan(&p.Run, &p.AsOf, &p.StandingCents, &p.AccuracyPPM, &p.Judged); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Run < out[j].Run })
	return out, nil
}

// SourceKinds are the kinds standing comes from, in the order surfaces
// list them: the world edge's kinds (WorldItem.Kind) and judgements.
var SourceKinds = []string{"proof", "money", "payments", "time_present", "judgements"}

// SourceKindLabel names a source kind on a page (GLOSSARY words).
func SourceKindLabel(kind string) string {
	switch kind {
	case "proof":
		return "Priced proofs"
	case "money":
		return "Money paid"
	case "payments":
		return "Independent payments"
	case "time_present":
		return "Time present"
	case "judgements":
		return "Judgements"
	}
	return kind
}

// SourcesByKind is where run's standing comes from: each world kind's
// counted cents over every account (own costs, before each root's
// node_cap) and the cents judgement edges delivered (received). Keyed by
// SourceKinds; a kind with nothing is 0.
func SourcesByKind(ctx context.Context, q allowance.Querier, run int64) (map[string]int64, error) {
	out := make(map[string]int64, len(SourceKinds))
	for _, k := range SourceKinds {
		out[k] = 0
	}
	if run == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT json_extract(w.value,'$.kind'),sum(json_extract(w.value,'$.cents'))
 FROM trust_scores s, json_each(s.parts,'$.world') w WHERE s.run_id=? GROUP BY 1`, run)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var cents int64
		if err = rows.Scan(&kind, &cents); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := out[kind]; ok {
			out[kind] += cents
		}
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	var received int64
	if err = q.QueryRowContext(ctx, "SELECT coalesce(sum(json_extract(parts,'$.received_cents')),0) FROM trust_scores WHERE run_id=?", run).Scan(&received); err != nil {
		return nil, err
	}
	out["judgements"] = received
	return out, nil
}

// RunScores reads run's per-account passed flow (what PassedSharePPM
// reads): account, root and the cents it passed on.
func RunScores(ctx context.Context, q allowance.Querier, run int64) ([]ScoreOut, error) {
	rows, err := q.QueryContext(ctx, "SELECT account,root,flow_b FROM trust_scores WHERE run_id=?", run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var scores []ScoreOut
	for rows.Next() {
		var sc ScoreOut
		if err = rows.Scan(&sc.Account, &sc.Root, &sc.Parts.PassedCents); err != nil {
			return nil, err
		}
		scores = append(scores, sc)
	}
	return scores, rows.Err()
}
