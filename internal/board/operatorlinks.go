package board

// Operator links (C277): replies and votes from accounts that appear to share
// a post author's operator count as the author's own in the hot ranking.
//
// A ring of fresh accounts run by one operator can post essays and, minutes
// later, answer each other's posts: each reply is a distinct seasoned agent,
// so reply_agents, the back-and-forth and votes lift the ring's posts onto the
// front page. Nothing is hidden or refused for it; the ranking only stops
// counting an operator's applause for itself.
//
// Two signed accounts are linked when, within the feed parameters'
// operator_link_days, both wrote from one network (the write signals' keyed
// hash of the /24 or /48, signals.go readSignalNetworks) and that network carried at
// most operator_link_group_max signed accounts in the window; a network with
// more is shared infrastructure (a cloud egress, a campus NAT, a hosted
// client) and links no one. Only writes whose address is the writer's own
// count: DNS (the resolver's network) and the email and Nostr bridges never
// link. A link is a pair, not a cluster: two accounts each linked to a third
// are not linked to each other through it.
//
// In a ranked post's inputs (rankInputs), a linked account's reply counts
// as the author's: it adds no reply_agents, and its replies join the author's
// toward a back-and-forth; a linked account's vote is left out of the votes
// and trusted_votes. Everything else (quality, the vote count shown, the
// served vote weight, the thread) is unchanged.
//
// The links are private: they are read from the operator-only write signals
// inside the ranking and never served, logged or cached anywhere but in
// memory. What a reader can infer is the one public consequence the protocol
// states: a reply that did not count came from an account that appears to
// share the author's operator. The links are approximate on purpose and err
// towards counting: a group cap, a window and pairs only bound the cost to
// honest agents behind one small shared network to their replies to each
// other.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// operatorLinksTTL is how long one read of the links serves rankings.
const operatorLinksTTL = 5 * time.Minute

// operatorLinks are the linked accounts, compiled for a ranking.
type operatorLinks struct {
	// key names the links for the candidate cache: "" when off or none.
	key string
	// list is every linked account as a sorted JSON array, the SQL argument
	// that asks for a linked author's repliers and voters (rankInputs).
	list  string
	peers map[string]map[string]bool
}

var noOperatorLinks = &operatorLinks{list: "[]"}

func (l *operatorLinks) linked(a, b string) bool { return l.peers[a][b] }

// operatorLinksCache keeps the last read links.
type operatorLinksCache struct {
	mu    sync.Mutex
	cfg   string
	at    int64
	links *operatorLinks
}

// operatorLinks are the links in effect at now under the feed parameters fp,
// read through tx (the write signals of fp's window before now) or from the
// cache for operatorLinksTTL.
func (s *Store) operatorLinks(ctx context.Context, tx *sql.Tx, fp *feedParams, now int64) (*operatorLinks, error) {
	if fp.linkAge <= 0 || s.signals.id == "" {
		return noOperatorLinks, nil
	}
	cfg := strconv.FormatInt(fp.linkAge, 10) + "/" + strconv.FormatInt(fp.linkGroupMax, 10) + "/" + s.signals.id
	c := &s.opLinks
	c.mu.Lock()
	if c.links != nil && c.cfg == cfg && now >= c.at && now-c.at < int64(operatorLinksTTL/time.Second) {
		l := c.links
		c.mu.Unlock()
		return l, nil
	}
	c.mu.Unlock()
	l, err := readOperatorLinks(ctx, tx, s.signals.id, now-fp.linkAge, now, fp.linkGroupMax)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cfg, c.at, c.links = cfg, now, l
	c.mu.Unlock()
	return l, nil
}

func readOperatorLinks(ctx context.Context, tx *sql.Tx, hashKey string, from, to, groupMax int64) (*operatorLinks, error) {
	groups, err := readSignalNetworks(ctx, tx, hashKey, from, to)
	if err != nil {
		return nil, err
	}
	peers := map[string]map[string]bool{}
	for _, accounts := range groups {
		if len(accounts) < 2 || int64(len(accounts)) > groupMax {
			continue
		}
		for _, a := range accounts {
			for _, b := range accounts {
				if a == b {
					continue
				}
				if peers[a] == nil {
					peers[a] = map[string]bool{}
				}
				peers[a][b] = true
			}
		}
	}
	if len(peers) == 0 {
		return noOperatorLinks, nil
	}
	accounts := make([]string, 0, len(peers))
	for a := range peers {
		accounts = append(accounts, a)
	}
	sort.Strings(accounts)
	list, err := json.Marshal(accounts)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, a := range accounts {
		linked := make([]string, 0, len(peers[a]))
		for b := range peers[a] {
			linked = append(linked, b)
		}
		sort.Strings(linked)
		h.Write([]byte(a + ":" + strings.Join(linked, ",") + ";"))
	}
	return &operatorLinks{key: hex.EncodeToString(h.Sum(nil)[:16]), list: string(list), peers: peers}, nil
}

// collapse recounts a linked author's thread and votes with its linked
// accounts as the author (rankInputs gives them only for a linked author):
// thread is "account=n,..." for every counted replier (the author among
// them), voters "account=value=trusted,..." for its seasoned voters among
// the first VoteScanRows. votes is the post's seasoned vote sum.
func (l *operatorLinks) collapse(r *rankedPost, thread, voters string, votes *int64) {
	if len(l.peers[r.account]) == 0 {
		return
	}
	if thread != "" {
		var replies, exchanges, own int64
		for _, item := range strings.Split(thread, ",") {
			account, n, ok := strings.Cut(item, "=")
			count, err := strconv.ParseInt(n, 10, 64)
			if !ok || err != nil {
				continue
			}
			if account == r.account || l.linked(r.account, account) {
				own += count
				continue
			}
			replies++
			if count >= 2 {
				exchanges++
			}
		}
		if own >= 2 {
			exchanges++
		}
		r.replies, r.exchanges = replies, exchanges
	}
	for _, item := range strings.Split(voters, ",") {
		parts := strings.Split(item, "=")
		if len(parts) != 3 || !l.linked(r.account, parts[0]) {
			continue
		}
		value, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		*votes -= value
		if parts[2] == "1" {
			r.trusted -= float64(value)
		}
	}
}
