// Package gate is the SwarmMemo Gate's core (C325): a bot check a site
// embeds in place of a CAPTCHA. A visitor asks for a challenge bound to the
// site's origin, proves something (proof of work, a timed machine task, a
// wallet signature, or its SwarmMemo identity and standing), and gets a
// short-lived token signed with the notary key that the site checks offline
// (services.VerifyGateToken; docs/rfcs/0022-gate-token.md).
//
// This package holds what needs no database: the pending challenges (in
// memory, single use, five minutes), the proof-of-work rule and its
// difficulty, the agent task, and the strict parsers of what a caller sends.
// The board reads standing and signs (board/gate.go). Challenges are never
// written to disk: a restart forgets the pending ones, so a challenge can be
// used at most once in any process's life, and an old one is simply unknown.
package gate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/bits"
	mrand "math/rand/v2"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"swarmmemo/internal/proofs"
)

// Bounds and defaults. Every number a surface quotes reads these.
const (
	// ChallengeTTL is how long a challenge stays usable.
	ChallengeTTL = 300 * time.Second
	// TokenTTL is how long a token is valid after it is issued.
	TokenTTL = 300 * time.Second
	// BitsMin, BitsDefault and BitsMax bound the proof of work's leading
	// zero bits: about 2^bits SHA-256 hashes. 20 bits is about a second in a
	// desktop browser and a few on a phone. The minimum is the default: the
	// caller asks for the challenge, so a lower floor would be the bot's
	// choice (C325 security review); a site's bits only raise it, and the
	// token states the bits the work met.
	BitsMin     = BitsDefault
	BitsDefault = 20
	BitsMax     = 28
	// EscalateAfter is how many challenges one network may take in
	// EscalateWindow before each doubling adds a bit to its work.
	EscalateAfter  = 8
	EscalateWindow = 10 * time.Minute
	// NetworkChallengesMax bounds one network's challenges in EscalateWindow.
	NetworkChallengesMax = 600
	// WideFactor is how many networks' worth an IPv6 /48 (a site's usual
	// allocation: 65,536 /64s) gets: its count escalates and caps at this
	// multiple of one network's, so moving to a fresh /64 resets nothing.
	WideFactor = 8
	// PendingMax bounds the challenges held at once, across every caller.
	// At the bound the oldest pending challenge is dropped, never the new
	// one refused: a flood cannot shut the gate for everyone.
	PendingMax = 100000
	// AgentDeadlineMillis is the agent task's time limit, from the
	// challenge's issue to the verify, measured by the server.
	AgentDeadlineMillis = 3000
	// AgentRecords is how many records an agent task carries.
	AgentRecords = 120
	// MinCentsMax bounds the standing a site may ask for ($10,000).
	MinCentsMax = 1000000
	// SolutionMax bounds a proof-of-work solution and AnswerMax an answer.
	SolutionMax = 64
	AnswerMax   = 32
	// DataMax bounds a request's JSON.
	DataMax = 2048
	// TokenMax bounds a token a verifier accepts.
	TokenMax = 2048
)

// Modes a challenge is issued for.
const (
	ModePow   = "pow"   // proof of work, or a wallet signature, or the identity (signed)
	ModeAgent = "agent" // the timed agent task
)

// Proofs a token can name, beside the identity's verified link kinds.
const (
	ProofPow      = "pow"
	ProofAgent    = "agent_task"
	ProofWallet   = "wallet"
	ProofIdentity = "identity"
)

// Errors. The board maps each to its refusal (board/gate.go).
var (
	ErrRequest    = errors.New("gate: malformed request")
	ErrOrigin     = errors.New("gate: origin is not a web origin")
	ErrUnknown    = errors.New("gate: unknown, used or expired challenge")
	ErrRate       = errors.New("gate: too many challenges from this network")
	ErrWork       = errors.New("gate: the solution does not meet the difficulty")
	ErrAnswer     = errors.New("gate: wrong answer")
	ErrLate       = errors.New("gate: answered after the deadline")
	ErrMode       = errors.New("gate: this challenge does not take that proof")
	ErrWallet     = errors.New("gate: the wallet signature does not match")
	ErrNoProof    = errors.New("gate: no proof given")
	ErrIdentity   = errors.New("gate: this challenge needs standing: sign it with a SwarmMemo key")
	ErrNoStanding = errors.New("gate: a key with no standing passes with the work too")
	ErrTokenForm  = errors.New("gate: malformed token")
)

var (
	idRE       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	siteNonce  = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	solutionRE = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)
	answerRE   = regexp.MustCompile(`^-?[0-9]{1,30}$`)
	walletRE   = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
)

// ChallengeRequest is gate.challenge's data.
type ChallengeRequest struct {
	Schema int    `json:"schema"`
	Origin string `json:"origin"`
	Min    int64  `json:"min,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Bits   int    `json:"bits,omitempty"`
}

// VerifyRequest is gate.verify's data: the challenge and one proof. A
// signed gate.verify proves the identity and needs no other field.
type VerifyRequest struct {
	Schema    int    `json:"schema"`
	Challenge string `json:"challenge"`
	Solution  string `json:"solution,omitempty"`
	Answer    string `json:"answer,omitempty"`
	Wallet    string `json:"wallet,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// strict decodes raw into dst: one JSON object, at most DataMax bytes, no
// unknown fields, nothing after it.
func strict(raw []byte, dst any) error {
	if len(raw) == 0 || len(raw) > DataMax {
		return ErrRequest
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrRequest
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrRequest
	}
	return nil
}

// ParseChallengeRequest reads and checks gate.challenge's data. validOrigin
// is the board's web-origin rule (https://host[:port], or http on localhost).
func ParseChallengeRequest(raw []byte, validOrigin func(string) bool) (ChallengeRequest, error) {
	var r ChallengeRequest
	if err := strict(raw, &r); err != nil {
		return r, err
	}
	switch {
	case r.Schema != 1:
		return r, ErrRequest
	case r.Origin == "" && r.Mode == ModeAgent:
		// The agent task is the service's own demo: the board names its
		// origin.
	case !validOrigin(r.Origin):
		return r, ErrOrigin
	case r.Min < 0 || r.Min > MinCentsMax:
		return r, ErrRequest
	case r.Nonce != "" && !siteNonce.MatchString(r.Nonce):
		return r, ErrRequest
	case r.Bits != 0 && (r.Bits < BitsMin || r.Bits > BitsMax):
		return r, ErrRequest
	}
	switch r.Mode {
	case "":
		r.Mode = ModePow
	case ModePow, ModeAgent:
	default:
		return r, ErrRequest
	}
	if r.Mode == ModeAgent && r.Min > 0 {
		return r, ErrRequest
	}
	return r, nil
}

// ParseVerifyRequest reads and checks gate.verify's data: a solution, an
// answer, or a wallet with its signature beside a solution; one unless
// signed, and never a wallet when signed. A wallet costs nothing to make,
// as a key does, so it names a subject but never passes without the work.
// (A signed key with no standing yet sends the work too: the board checks
// that, once it has read the standing.)
func ParseVerifyRequest(raw []byte, signed bool) (VerifyRequest, error) {
	var r VerifyRequest
	if err := strict(raw, &r); err != nil {
		return r, err
	}
	if r.Schema != 1 || !idRE.MatchString(r.Challenge) {
		return r, ErrRequest
	}
	n := 0
	if r.Solution != "" {
		n++
		if !solutionRE.MatchString(r.Solution) {
			return r, ErrRequest
		}
	}
	if r.Answer != "" {
		n++
		if !answerRE.MatchString(r.Answer) {
			return r, ErrRequest
		}
	}
	wallet := r.Wallet != "" || r.Signature != ""
	if wallet && (!walletRE.MatchString(r.Wallet) || len(r.Signature) != 132 || !strings.HasPrefix(r.Signature, "0x")) {
		return r, ErrRequest
	}
	switch {
	case n > 1, wallet && r.Solution == "":
		return r, ErrRequest
	case signed && r.Wallet != "":
		// A signed verify is the identity's proof; a wallet beside it
		// would be a second, unweighed claim.
		return r, ErrRequest
	case n == 0 && !signed:
		return r, ErrNoProof
	}
	return r, nil
}

// Task is the agent task: sum Field over the records whose tag is Tag.
type Task struct {
	Kind       string   `json:"kind"`
	Field      string   `json:"field"`
	Where      TaskTag  `json:"where"`
	Records    []Record `json:"records"`
	DeadlineMS int64    `json:"deadline_ms"`
	Rule       string   `json:"rule"`
}

// TaskTag is the task's filter.
type TaskTag struct {
	Tag string `json:"tag"`
}

// Record is one record of a task.
type Record struct {
	ID    int    `json:"id"`
	Tag   string `json:"tag"`
	Value int    `json:"value"`
}

var taskTags = []string{"amber", "basalt", "cobalt", "dune", "ember", "fjord"}

// TaskRule says what an answer is, for every surface.
const TaskRule = "answer: the sum of value over the records whose tag equals where.tag, as a decimal string, sent within deadline_ms of the challenge"

// newTask builds the task a seed names; the same seed is the same task.
func newTask(seed [32]byte) (Task, string) {
	r := mrand.New(mrand.NewChaCha8(seed))
	t := Task{Kind: "sum", Field: "value", Where: TaskTag{Tag: taskTags[r.IntN(len(taskTags))]}, DeadlineMS: AgentDeadlineMillis, Rule: TaskRule}
	sum := 0
	t.Records = make([]Record, AgentRecords)
	for i := range t.Records {
		rec := Record{ID: i + 1, Tag: taskTags[r.IntN(len(taskTags))], Value: 1 + r.IntN(999)}
		if rec.Tag == t.Where.Tag {
			sum += rec.Value
		}
		t.Records[i] = rec
	}
	return t, strconv.Itoa(sum)
}

// Challenge is one pending challenge.
type Challenge struct {
	ID       string
	Origin   string
	Nonce    string // the site's nonce, or the challenge ID
	Min      int64
	Mode     string
	Bits     int
	IssuedAt time.Time
	seed     [32]byte
}

// ExpiresAt is when the challenge stops being usable.
func (c Challenge) ExpiresAt() time.Time { return c.IssuedAt.Add(ChallengeTTL) }

// Task is the agent task of an agent challenge, with its answer.
func (c Challenge) Task() (Task, string) { return newTask(c.seed) }

// Statement is what a wallet signs (personal_sign) and what a token's
// challenge stands for: the service, the origin and the challenge.
func Statement(serviceID, origin, id string) string {
	return "swarmmemo-gate:1:" + serviceID + ":" + origin + ":" + id
}

// Gate holds the pending challenges and each network's recent count.
type Gate struct {
	mu      sync.Mutex
	pending map[string]Challenge
	// order holds the pending IDs in issue order from head on, which is
	// also expiry order (one TTL); a taken ID stays until it reaches head.
	order []string
	head  int
	nets  map[string]*window
	// Now is the clock (time.Now when nil); tests freeze it.
	Now func() time.Time
}

type window struct {
	start time.Time
	n     int
}

// New makes an empty gate.
func New() *Gate { return &Gate{pending: map[string]Challenge{}, nets: map[string]*window{}} }

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// NetworkKey is the key a caller's challenges are counted under: an IPv4
// address, or an IPv6 /64 (one subscriber's usual allocation).
func NetworkKey(source string) string {
	ip := net.ParseIP(source)
	if ip == nil {
		return source
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// WideKey is the wider network an IPv6 caller is also counted under, its
// /48; empty for IPv4 and anything else.
func WideKey(source string) string {
	ip := net.ParseIP(source)
	if ip == nil || ip.To4() != nil {
		return ""
	}
	return ip.Mask(net.CIDRMask(48, 128)).String() + "/48"
}

// Escalation is the bits a network's count adds: one for each doubling
// past EscalateAfter challenges in the window.
func Escalation(count int) int {
	if count <= EscalateAfter {
		return 0
	}
	return bits.Len(uint(count-1)) - bits.Len(uint(EscalateAfter-1))
}

// count is a network's window at now, started afresh once it has ended.
func (g *Gate) count(network string, now time.Time) *window {
	w := g.nets[network]
	if w == nil || now.Sub(w.start) >= EscalateWindow {
		w = &window{start: now}
		g.nets[network] = w
	}
	return w
}

// Issue makes a challenge for req, counted against the caller's network:
// source is its address, and an IPv6 one counts against its /64 and its
// /48.
func (g *Gate) Issue(req ChallengeRequest, source string) (Challenge, error) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(now)
	w, wide := g.count(NetworkKey(source), now), &window{}
	if key := WideKey(source); key != "" {
		wide = g.count(key, now)
	}
	if w.n >= NetworkChallengesMax || wide.n >= NetworkChallengesMax*WideFactor {
		return Challenge{}, ErrRate
	}
	w.n++
	wide.n++
	for len(g.pending) >= PendingMax {
		g.dropOldest()
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Challenge{}, err
	}
	c := Challenge{ID: hex.EncodeToString(raw[:]), Origin: req.Origin, Nonce: req.Nonce, Min: req.Min, Mode: req.Mode, IssuedAt: now}
	if c.Nonce == "" {
		c.Nonce = c.ID
	}
	base := req.Bits
	if base == 0 {
		base = BitsDefault
	}
	c.Bits = min(base+max(Escalation(w.n), Escalation(wide.n/WideFactor)), BitsMax)
	if _, err := rand.Read(c.seed[:]); err != nil {
		return Challenge{}, err
	}
	g.pending[c.ID] = c
	g.order = append(g.order, c.ID)
	return c, nil
}

// dropOldest removes the oldest pending challenge; called with mu held and
// something pending.
func (g *Gate) dropOldest() {
	for g.head < len(g.order) {
		id := g.order[g.head]
		g.order[g.head] = ""
		g.head++
		if _, ok := g.pending[id]; ok {
			delete(g.pending, id)
			return
		}
	}
}

// sweep drops expired challenges from the head of the issue order (and
// taken ones on the way), keeps the order compact, and drops idle network
// windows once there are many; called with mu held.
func (g *Gate) sweep(now time.Time) {
	for g.head < len(g.order) {
		id := g.order[g.head]
		if c, ok := g.pending[id]; ok {
			if now.Before(c.ExpiresAt()) {
				break
			}
			delete(g.pending, id)
		}
		g.order[g.head] = ""
		g.head++
	}
	if live := len(g.order) - g.head; g.head > 1024 && g.head > live || live > 2*PendingMax {
		// Rebuild from what is still pending, oldest first: a slice that
		// only grew would keep every ID ever issued.
		kept := make([]string, 0, len(g.pending))
		for _, id := range g.order[g.head:] {
			if _, ok := g.pending[id]; ok {
				kept = append(kept, id)
			}
		}
		g.order, g.head = kept, 0
	}
	if len(g.nets) >= 10000 {
		for k, w := range g.nets {
			if now.Sub(w.start) >= EscalateWindow {
				delete(g.nets, k)
			}
		}
	}
}

// Take removes a challenge and returns it: whatever the verify then finds,
// the challenge is spent. An unknown, spent or expired ID is ErrUnknown.
func (g *Gate) Take(id string) (Challenge, error) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.pending[id]
	if !ok {
		return Challenge{}, ErrUnknown
	}
	delete(g.pending, id)
	if !now.Before(c.ExpiresAt()) {
		return Challenge{}, ErrUnknown
	}
	return c, nil
}

// Pending is how many challenges are held (for /v1/gate/status).
func (g *Gate) Pending() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pending)
}

// CheckWork reports whether SHA-256(prefix + solution) has at least bits
// leading zero bits: the pow proof kind's rule (proofs.Pow). The prefix is
// the challenge ID.
func CheckWork(prefix, solution string, bits int) bool {
	return proofs.LeadingZeroBits(sha256.Sum256([]byte(prefix+solution))) >= int64(bits)
}

// Solve finds a solution by counting in base 36 (tests and the docs'
// reference solver); limit bounds the tries.
func Solve(prefix string, bits int, limit uint64) (string, bool) {
	for n := uint64(0); n < limit; n++ {
		s := strconv.FormatUint(n, 36)
		if CheckWork(prefix, s, bits) {
			return s, true
		}
	}
	return "", false
}

// Elapsed is the milliseconds from c's issue to now.
func (g *Gate) Elapsed(c Challenge) int64 { return g.now().Sub(c.IssuedAt).Milliseconds() }

// Clock is the gate's time.
func (g *Gate) Clock() time.Time { return g.now() }
