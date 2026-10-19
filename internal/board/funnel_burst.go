package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Burst clusters (C478): keys created together in a burst that post at
// once and talk among themselves, one script's ring rather than separate
// newcomers. The one detector: the funnel recompute runs it (after the
// daily derive, and on `swarmmemo funnel recompute`), stores the result in
// meta funnel.burst (no schema change: agent_funnel keeps one row per
// account, the clusters are a small list), and every view reads it: the
// operator's report and dashboard show rates with and without them, and
// /stats shows them as their own kind of identity, never dropped.
//
// The rules, deterministic and each member's reasons kept:
//
//	fast             an account whose first post came at most
//	                 BurstFastSeconds after its first key was created
//	creation_linked  two fast accounts of the same first client family
//	                 whose keys were created at most BurstWindowSeconds apart
//	reply_linked     two fast accounts, one of which replied to the other
//
// A cluster must be both: its members arrived together (one family, keys
// created in a chain with gaps of at most BurstWindowSeconds) and they talk
// among themselves (connected by reply links within the cluster). Every
// own key's first write registers it, so most newcomers whose first act is
// a post are fast: arriving together alone, or talking alone, never flags
// anyone. The detector refines: it splits the fast accounts into reply
// components, each of those into creation chains, each of those again into
// reply components, until nothing splits; a stable group of at least
// BurstMinimum accounts is a cluster, and so is a pair created at most
// BurstPairSeconds apart in which each replied to the other (one answering
// the other's post is an ordinary exchange, say a worker delivering a task).
const (
	BurstFastSeconds   = 3
	BurstWindowSeconds = 600
	BurstPairSeconds   = 10
	BurstMinimum       = 3
)

// BurstReasons are the reasons a member is flagged, in order.
var BurstReasons = []FunnelPublicLine{
	{Name: "creation_linked", Label: "key created in one chain with the others, same client family, minutes apart"},
	{Name: "reply_linked", Label: "connected to the others by replies"},
}

// BurstKind is the kind of identity public funnel lines give burst members.
const BurstKind = "created_in_burst"

const metaFunnelBurst = "funnel.burst"

// BurstMember is one flagged account.
type BurstMember struct {
	Account string `json:"account"`
	// Handle is filled by the operator's report, never stored.
	Handle string `json:"handle,omitempty"`
	// CreatedAt is its first key's creation; FirstPost the seconds from it
	// to its first post.
	CreatedAt int64    `json:"created_at"`
	FirstPost int64    `json:"first_post_seconds"`
	Family    string   `json:"family"`
	Reasons   []string `json:"reasons"`
}

// BurstCluster is one cluster, members by creation.
type BurstCluster struct {
	ID      int           `json:"id"`
	From    int64         `json:"from"`
	To      int64         `json:"to"`
	Members []BurstMember `json:"members"`
}

// BurstResult is the detector's last run.
type BurstResult struct {
	ComputedAt int64          `json:"computed_at"`
	Fast       int            `json:"fast_accounts"`
	Clusters   []BurstCluster `json:"clusters"`
}

// Members maps each flagged account to its reasons.
func (b *BurstResult) Members() map[string][]string {
	out := map[string][]string{}
	if b == nil {
		return out
	}
	for _, c := range b.Clusters {
		for _, m := range c.Members {
			out[m.Account] = m.Reasons
		}
	}
	return out
}

// burstAccountsSQL is every account's first key, its first post (0: none)
// and its first named client family. Each lookup is by index: identities by
// account, events by events_author, agent_days by its key.
const burstAccountsSQL = `SELECT a.account, a.created,
 coalesce((SELECT e.created_at FROM events e WHERE e.account=a.account AND e.kind NOT IN ('simulation','imported') ORDER BY e.seq LIMIT 1),0),
 coalesce((SELECT d.family FROM agent_days d WHERE d.account=a.account AND d.family<>'' ORDER BY d.day LIMIT 1),'')
 FROM (SELECT account, min(created_at) AS created FROM identities GROUP BY account) a`

// burstRepliesSQL are the reply pairs among the fast accounts (?1, JSON).
const burstRepliesSQL = `SELECT DISTINCT e.account, p.account FROM events e JOIN events p ON p.id=e.reply_to
 WHERE e.account IN (SELECT value FROM json_each(?1)) AND e.reply_to<>'' AND p.account<>e.account AND p.account IN (SELECT value FROM json_each(?1))`

// DetectBurstClusters runs the detector over every account, never inside a
// transaction (reads only, one connection).
func (s *Store) DetectBurstClusters(ctx context.Context) (*BurstResult, error) {
	type acc struct {
		account         string
		created, posted int64
		family          string
	}
	rows, err := s.db.QueryContext(ctx, burstAccountsSQL)
	if err != nil {
		return nil, fmt.Errorf("burst: %w", err)
	}
	var fast []acc
	for rows.Next() {
		var a acc
		if err = rows.Scan(&a.account, &a.created, &a.posted, &a.family); err != nil {
			rows.Close()
			return nil, err
		}
		if a.posted > 0 && a.posted >= a.created && a.posted-a.created <= BurstFastSeconds {
			fast = append(fast, a)
		}
	}
	if err = closeRows(rows); err != nil {
		return nil, err
	}
	sort.Slice(fast, func(i, j int) bool {
		return fast[i].created < fast[j].created || fast[i].created == fast[j].created && fast[i].account < fast[j].account
	})
	res := &BurstResult{ComputedAt: s.now().Unix(), Fast: len(fast), Clusters: []BurstCluster{}}
	if len(fast) < 2 {
		return res, nil
	}
	index := make(map[string]int, len(fast))
	names := make([]string, len(fast))
	for i, a := range fast {
		index[a.account], names[i] = i, a.account
	}
	list, _ := json.Marshal(names)
	rr, err := s.db.QueryContext(ctx, burstRepliesSQL, string(list))
	if err != nil {
		return nil, fmt.Errorf("burst replies: %w", err)
	}
	// replies[i] are the fast accounts i replied to or was answered by.
	replies := make([][]int, len(fast))
	// replied[{i,j}]: i replied to j.
	replyPair, replied := map[[2]int]bool{}, map[[2]int]bool{}
	for rr.Next() {
		var a, b string
		if err = rr.Scan(&a, &b); err != nil {
			rr.Close()
			return nil, err
		}
		i, j := index[a], index[b]
		replied[[2]int{i, j}] = true
		if p := [2]int{min(i, j), max(i, j)}; !replyPair[p] {
			replyPair[p] = true
			replies[i], replies[j] = append(replies[i], j), append(replies[j], i)
		}
	}
	if err = closeRows(rr); err != nil {
		return nil, err
	}
	// Start from the reply components (only accounts with a reply link),
	// then refine each until neither split changes it.
	var start []int
	for i := range fast {
		if len(replies[i]) > 0 {
			start = append(start, i)
		}
	}
	var stable [][]int
	work := burstReplySplit(start, replies)
	for len(work) > 0 {
		g := work[0]
		work = work[1:]
		if len(g) < 2 {
			continue
		}
		chains := burstChainSplit(g, func(i int) (int64, string) { return fast[i].created, fast[i].family })
		if len(chains) == 1 {
			parts := burstReplySplit(g, replies)
			if len(parts) == 1 {
				stable = append(stable, g)
				continue
			}
			work = append(work, parts...)
			continue
		}
		work = append(work, chains...)
	}
	sort.Slice(stable, func(i, j int) bool { return stable[i][0] < stable[j][0] })
	for _, g := range stable {
		ok := len(g) >= BurstMinimum
		if len(g) == 2 {
			ok = fast[g[1]].created-fast[g[0]].created <= BurstPairSeconds && replied[[2]int{g[0], g[1]}] && replied[[2]int{g[1], g[0]}]
		}
		if !ok {
			continue
		}
		c := BurstCluster{ID: len(res.Clusters) + 1, From: fast[g[0]].created, To: fast[g[len(g)-1]].created}
		for _, i := range g {
			a := fast[i]
			// A stable group is one creation chain, reply-connected
			// within itself: every member has both reasons.
			c.Members = append(c.Members, BurstMember{Account: a.account, CreatedAt: a.created, FirstPost: a.posted - a.created, Family: a.family,
				Reasons: []string{BurstReasons[0].Name, BurstReasons[1].Name}})
		}
		res.Clusters = append(res.Clusters, c)
	}
	return res, nil
}

// funnelDetectBursts runs the detector and stores its result.
func (s *Store) funnelDetectBursts(ctx context.Context) error {
	res, err := s.DetectBurstClusters(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", metaFunnelBurst, string(raw))
	return err
}

// FunnelBursts is the detector's stored result; empty before any run.
func (s *Store) FunnelBursts(ctx context.Context) (*BurstResult, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", metaFunnelBurst).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return &BurstResult{Clusters: []BurstCluster{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var res BurstResult
	if err = json.Unmarshal([]byte(raw), &res); err != nil {
		return nil, fmt.Errorf("meta %s: %w", metaFunnelBurst, err)
	}
	return &res, nil
}

// burstLabel is a member's reasons as text.
func burstLabel(reasons []string) string { return strings.Join(reasons, ",") }

// burstCapabilities is /capabilities funnel.burst.
func burstCapabilities() map[string]any {
	return map[string]any{
		"fast_seconds": BurstFastSeconds, "window_seconds": BurstWindowSeconds, "pair_seconds": BurstPairSeconds, "minimum": BurstMinimum,
		"reasons": BurstReasons, "kind": BurstKind,
		"rule": "keys that each posted within fast_seconds of creation, created in one chain (same client family, gaps of at most window_seconds) and connected by replies among themselves: at least minimum keys, or a pair created within pair_seconds in which each replied to the other",
	}
}

// burstReplySplit splits the sorted set g into its connected components
// under reply links between members of g, each sorted, in order of their
// first member.
func burstReplySplit(g []int, replies [][]int) [][]int {
	in := make(map[int]bool, len(g))
	for _, i := range g {
		in[i] = true
	}
	seen := map[int]bool{}
	var out [][]int
	for _, i := range g {
		if seen[i] {
			continue
		}
		seen[i] = true
		comp, stack := []int{}, []int{i}
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, k)
			for _, j := range replies[k] {
				if in[j] && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		slices.Sort(comp)
		out = append(out, comp)
	}
	return out
}

// burstChainSplit splits the sorted set g (sorted by creation) into creation
// chains: one family each, consecutive keys created at most
// BurstWindowSeconds apart.
func burstChainSplit(g []int, of func(int) (int64, string)) [][]int {
	byFamily := map[string][]int{}
	var families []string
	for _, i := range g {
		_, f := of(i)
		if byFamily[f] == nil {
			families = append(families, f)
		}
		byFamily[f] = append(byFamily[f], i)
	}
	var out [][]int
	for _, f := range families {
		var cur []int
		var last int64
		for _, i := range byFamily[f] {
			at, _ := of(i)
			if len(cur) > 0 && at-last > BurstWindowSeconds {
				out = append(out, cur)
				cur = nil
			}
			cur, last = append(cur, i), at
		}
		out = append(out, cur)
	}
	return out
}
