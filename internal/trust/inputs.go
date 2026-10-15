package trust

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Inputs are the paged, read-only sources of one run. The board implements
// it (trustwire.go): Read calls emit once per record, reading pages of at
// most PageRows rows, each in its own short read transaction, so nothing
// holds the store's one connection while the run computes.
type Inputs interface {
	Read(ctx context.Context, asOf int64, p Params, emit func(Record) error) error
}

// PageRows is the largest page an input reader may read in one transaction.
const PageRows = 2000

// SnapshotSchema is the snapshot format of RFC 0021 runs (parameter
// version 8 on). Snapshots of retired versions are schema 1.
const SnapshotSchema = 2

// Snapshot is everything one run reads, as typed records. Its JSONL form (one
// Record per line, any order) is the golden fixture's input format and what a
// run publishes at /api/trust/runs/ID/snapshot, so a third party can
// recompute it (scripts/trust/recompute.py run).
type Snapshot struct {
	Meta         Meta
	Params       Params
	Accounts     []Record // type "account"
	Posts        []Record // type "post"
	Endorsements []Record // type "endorsement"
	Proofs       []Record // type "proof"
	Breakers     []Record // type "breaker"
	Transfers    []Record // type "transfer"
	Claims       []Record // type "claim"
	Payments     []Record // type "payment"
	Acts         []Record // type "edge"
	Reviews      []Record // type "review"
	Owners       []Record // type "owner"
	// The public penalty log (RFC 0021 §8): its entries' members, burned
	// roots, backers' liabilities and lifts.
	Penalties   []Record // type "penalty"
	Burns       []Record // type "burn"
	Liabilities []Record // type "liability"
	Lifts       []Record // type "lift"
	// Carry is the previous run's carry (Carry.Records): what the world
	// edges held, published in full in that run's canonical output.
	Carry []Record // type "carry"
}

// Meta is the run's frame: its as-of time and the input positions it read.
type Meta struct {
	Schema            int64 `json:"schema"`
	AsOf              int64 `json:"as_of"`
	PauseNewKeysSince int64 `json:"pause_new_keys_since"`
	EventsSeq         int64 `json:"events_seq"`
	EndorsementsSeq   int64 `json:"endorsements_seq"`
	LedgerSeq         int64 `json:"ledger_seq"`
}

// Record is one input line. Type selects the fields that apply:
//
//	meta         schema as_of pause_new_keys_since events_seq endorsements_seq ledger_seq
//	params       version body
//	account      account first_seen
//	post         id account created_at reply_to reply_to_account
//	endorsement  seq kind voter target message_id value created_at
//	             (kind: vote, vouch, unsigned; target is the account the edge
//	             points at: the author of the voted post's edit-chain root,
//	             or the vouched account)
//	proof        account kind link_value state created_at checked_at
//	             link_account registered_at root assessed rank (registered_at:
//	             a domain's registry creation time; rank: its list rank; root
//	             and assessed: an assessed root's key and assessment)
//	breaker      account kind started_at trust_until (kind: the reason,
//	             agent.rotate, dormant, identity.link, identity.unlink or
//	             spike)
//	transfer     from to amount created_at
//	claim        account day claimed spent
//	payment      id kind account to amount created_at link_value (money in
//	             US cents: to "" is a payment to SwarmMemo, else to another
//	             account; link_value the payee's payout address, if any)
//	edge         kind id from to created_at value (work_accept: the
//	             accepting account → the worker; witness: the witness → the
//	             witnessed agent, value 1 verified or −1 failed, id the claim)
//	review       id account target value kind link_value state created_at
//	             (a community review, current or superseded: target the
//	             judged subject, value its x, kind its evidence class,
//	             link_value its effective relationship, state its verdict)
//	owner        target account (an account that owns a reviewed subject)
//	penalty      id kind account created_at ends_at value state evidence
//	             (one per member of a penalty log entry: kind its trigger,
//	             value its level k, state "pending" until a run applies it,
//	             evidence its reason or evidence reference)
//	burn         id root (a proof root the entry burned, for good)
//	liability    id account fraction_ppm created_at ends_at value (a
//	             backer's loss under the entry, its level k)
//	lift         id created_at (an operator override reversing the entry)
//	carry        kind: run (as_of), or world (account amount time
//	             fraction_ppm last_active direct standing backed): the
//	             previous run's world accumulators (Carry)
type Record struct {
	Type string `json:"type"`
	// meta
	Schema            int64 `json:"schema,omitempty"`
	AsOf              int64 `json:"as_of,omitempty"`
	PauseNewKeysSince int64 `json:"pause_new_keys_since,omitempty"`
	EventsSeq         int64 `json:"events_seq,omitempty"`
	EndorsementsSeq   int64 `json:"endorsements_seq,omitempty"`
	LedgerSeq         int64 `json:"ledger_seq,omitempty"`
	// params
	Version int64           `json:"version,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
	// shared
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Account   string `json:"account,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	Value     int64  `json:"value,omitempty"`
	State     string `json:"state,omitempty"`
	Target    string `json:"target,omitempty"`
	// account
	FirstSeen int64 `json:"first_seen,omitempty"`
	// post
	ReplyTo        string `json:"reply_to,omitempty"`
	ReplyToAccount string `json:"reply_to_account,omitempty"`
	// endorsement
	Seq       int64  `json:"seq,omitempty"`
	Voter     string `json:"voter,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	// proof
	LinkValue    string `json:"link_value,omitempty"`
	CheckedAt    int64  `json:"checked_at,omitempty"`
	LinkAccount  string `json:"link_account,omitempty"`
	RegisteredAt int64  `json:"registered_at,omitempty"`
	Root         string `json:"root,omitempty"`
	Assessed     int64  `json:"assessed,omitempty"`
	Rank         int64  `json:"rank,omitempty"`
	// breaker
	StartedAt  int64 `json:"started_at,omitempty"`
	TrustUntil int64 `json:"trust_until,omitempty"`
	// transfer, payment
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Amount int64  `json:"amount,omitempty"`
	// claim
	Day     int64 `json:"day,omitempty"`
	Claimed int64 `json:"claimed,omitempty"`
	Spent   int64 `json:"spent,omitempty"`
	// penalty, liability
	Evidence    string `json:"evidence,omitempty"`
	FractionPPM int64  `json:"fraction_ppm,omitempty"`
	EndsAt      int64  `json:"ends_at,omitempty"`
	// carry (world)
	Time       int64 `json:"time,omitempty"`
	LastActive int64 `json:"last_active,omitempty"`
	Direct     int64 `json:"direct,omitempty"`
	Standing   int64 `json:"standing,omitempty"`
	Backed     int64 `json:"backed,omitempty"`
}

// Add files one record under its type.
func (s *Snapshot) Add(r Record) error {
	switch r.Type {
	case "meta":
		if r.Schema != SnapshotSchema {
			return fmt.Errorf("trust input: snapshot schema %d is not %d (a retired run's snapshot: scripts/trust/recompute.py verifies it)", r.Schema, SnapshotSchema)
		}
		s.Meta = Meta{Schema: r.Schema, AsOf: r.AsOf, PauseNewKeysSince: r.PauseNewKeysSince, EventsSeq: r.EventsSeq, EndorsementsSeq: r.EndorsementsSeq, LedgerSeq: r.LedgerSeq}
	case "params":
		p, err := ParseParams(r.Version, r.Body)
		if err != nil {
			return err
		}
		s.Params = p
	case "account":
		s.Accounts = append(s.Accounts, r)
	case "post":
		s.Posts = append(s.Posts, r)
	case "endorsement":
		s.Endorsements = append(s.Endorsements, r)
	case "proof":
		s.Proofs = append(s.Proofs, r)
	case "breaker":
		s.Breakers = append(s.Breakers, r)
	case "transfer":
		s.Transfers = append(s.Transfers, r)
	case "claim":
		s.Claims = append(s.Claims, r)
	case "payment":
		s.Payments = append(s.Payments, r)
	case "edge":
		s.Acts = append(s.Acts, r)
	case "review":
		s.Reviews = append(s.Reviews, r)
	case "owner":
		s.Owners = append(s.Owners, r)
	case "penalty":
		s.Penalties = append(s.Penalties, r)
	case "burn":
		s.Burns = append(s.Burns, r)
	case "liability":
		s.Liabilities = append(s.Liabilities, r)
	case "lift":
		s.Lifts = append(s.Lifts, r)
	case "carry":
		s.Carry = append(s.Carry, r)
	default:
		return fmt.Errorf("trust input: unknown record type %q", r.Type)
	}
	return nil
}

// Records lists the snapshot as records: meta, params, then each type in
// canonical order, which is also the order WriteJSONL uses.
func (s *Snapshot) Records() []Record {
	m := s.Meta
	out := []Record{
		{Type: "meta", Schema: SnapshotSchema, AsOf: m.AsOf, PauseNewKeysSince: m.PauseNewKeysSince, EventsSeq: m.EventsSeq, EndorsementsSeq: m.EndorsementsSeq, LedgerSeq: m.LedgerSeq},
		{Type: "params", Version: s.Params.Version, Body: s.Params.Body()},
	}
	for _, list := range [][]Record{s.Accounts, s.Posts, s.Endorsements, s.Proofs, s.Breakers, s.Transfers, s.Claims, s.Payments, s.Acts, s.Reviews, s.Owners,
		s.Penalties, s.Burns, s.Liabilities, s.Lifts, s.Carry} {
		out = append(out, sortRecords(list)...)
	}
	return out
}

// recordKey orders records of one type canonically.
func recordKey(r Record) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// sortRecords returns a copy of list in canonical order (by recordKey).
func sortRecords(list []Record) []Record {
	type keyed struct {
		key string
		rec Record
	}
	tmp := make([]keyed, len(list))
	for i, r := range list {
		tmp[i] = keyed{recordKey(r), r}
	}
	sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].key < tmp[j].key })
	out := make([]Record, len(list))
	for i, k := range tmp {
		out[i] = k.rec
	}
	return out
}

// ReadJSONL parses a snapshot, strictly: unknown fields and types are errors.
func ReadJSONL(r io.Reader) (Snapshot, error) {
	var s Snapshot
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	line := 0
	for sc.Scan() {
		line++
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		var rec Record
		dec := json.NewDecoder(bytes.NewReader(text))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&rec); err != nil {
			return Snapshot{}, fmt.Errorf("trust input line %d: %w", line, err)
		}
		if err := s.Add(rec); err != nil {
			return Snapshot{}, fmt.Errorf("trust input line %d: %w", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// WriteJSONL writes the snapshot, one canonical JSON record per line.
func (s *Snapshot) WriteJSONL(w io.Writer) error {
	bw := bufio.NewWriter(w)
	for _, r := range s.Records() {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = bw.Write(canonicalize(raw)); err != nil {
			return err
		}
		if err = bw.WriteByte('\n'); err != nil {
			return err
		}
	}
	return bw.Flush()
}
