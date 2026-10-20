package board

// Offering answers and refunds (RFC 0017 part B, C168B).
//
// offering.answer: the provider answers a paid (or overdue) call with up to
// OfferingAnswerBytes of text. The answer is screened like a post unless the
// revision the call bound to switched answer screening off (screen_answer),
// and the notary stamps a statement binding the call, the revision, the
// input's SHA-256, the answer's SHA-256 and the settling transaction
// (swarmmemo-offering-answer/1). The caller reads it at its poll URL, or a
// signed caller with offering.call.get; a signed caller also gets an inbox
// entry of kind offering_answer and its on:"received" wake-ups fire.
//
// Schema 29 (C173) stores an answer in columns: answer (the body),
// answer_content_type, answer_screen, answer_scores, answer_statement,
// answer_stamp and answer_late. Schema 28 kept them as one JSON object in
// answer (answerEnvelope); the migration splits it once, and a row still in
// that shape (answer_content_type empty) reads as before.
//
// offering.refund: the provider names the transaction that paid the caller
// back; the board reads it on chain (the work-reward reader, RFC 0016)
// before the command's transaction opens and checks it in the transaction:
// a confirmed transfer of the call's token from one of the provider's
// verified wallet links to the paying wallet, at least the call's amount,
// mined at or after the claim, and a hash that never settled or refunded
// anything here before.

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"swarmmemo/internal/services"
)

const (
	// inboxOfferingAnswer is a signed caller's inbox entry kind for an
	// answered call: a pointer (the call id), never the answer.
	inboxOfferingAnswer = "offering_answer"
	// OfferingAnswerBytes bounds an answer's body.
	OfferingAnswerBytes = 32 << 10
	// OfferingAnswerSchema names the statement the notary stamps for an
	// answer.
	OfferingAnswerSchema = "swarmmemo-offering-answer/1"
	// OfferingPollDays is how long a poll URL answers after its call last
	// changed.
	OfferingPollDays = 30
	// OfferingRefundConfirmations is the fewest confirmations a refund's
	// transaction needs (more when the chain reader asks for more).
	OfferingRefundConfirmations int64 = 3
	// offeringAnswerNotaryAccount is the notary account the answer stamps
	// are made under.
	offeringAnswerNotaryAccount = "board:offering_answer"
	// offeringAnswersRecent bounds data.offering_answers in updates.get.
	offeringAnswersRecent = 20
	// offeringAnswersDays is how far back data.offering_answers looks.
	offeringAnswersDays = 7
)

// offeringCallColumns29 are offering_calls' answer columns (schema 29,
// C173), in the order they are added: a keyed column step, so a new
// database and an upgraded one end with the same table.
var offeringCallColumns29 = []struct{ name, decl string }{
	{"answer_content_type", "TEXT NOT NULL DEFAULT ''"},
	{"answer_screen", "TEXT NOT NULL DEFAULT '' CHECK(answer_screen IN ('','pass','flag','unscreened','off'))"},
	{"answer_scores", "TEXT NOT NULL DEFAULT ''"},
	{"answer_statement", "TEXT NOT NULL DEFAULT ''"},
	{"answer_stamp", "TEXT NOT NULL DEFAULT ''"},
	{"answer_late", "INTEGER NOT NULL DEFAULT 0 CHECK(answer_late IN (0,1))"},
}

// offeringSchema29 are offering_calls' schema-29 indexes: a refund's
// transaction is used once; data.offering_answers seeks by the caller key;
// the overdue sweep reads only paid calls by due time, and the lapsed
// inbox closure only lapsed calls by closing time.
const offeringSchema29 = `
CREATE UNIQUE INDEX IF NOT EXISTS offering_calls_refund ON offering_calls(refund_tx) WHERE refund_tx<>'';
CREATE INDEX IF NOT EXISTS offering_calls_caller ON offering_calls(caller,answered_at) WHERE caller<>'';
CREATE INDEX IF NOT EXISTS offering_calls_due ON offering_calls(due_at) WHERE state='paid';
CREATE INDEX IF NOT EXISTS offering_calls_lapsed ON offering_calls(closed_at) WHERE state='lapsed';
`

// migrateOfferingCalls29 adds the answer columns and indexes and, on a
// database older than 29, splits each schema-28 answer envelope into them
// (the body stays in answer).
func migrateOfferingCalls29(tx *sql.Tx, version int) error {
	for _, column := range offeringCallColumns29 {
		var exists int
		if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('offering_calls') WHERE name=?", column.name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.Exec("ALTER TABLE offering_calls ADD COLUMN " + column.name + " " + column.decl); err != nil {
				return err
			}
		}
	}
	if version > 0 && version < 29 {
		if _, err := tx.Exec(`UPDATE offering_calls SET answer=coalesce(json_extract(answer,'$.body'),''),
 answer_content_type=coalesce(nullif(json_extract(answer,'$.content_type'),''),'text/plain'),
 answer_screen=CASE WHEN json_extract(answer,'$.screen.state') IN ('pass','flag','unscreened','off') THEN json_extract(answer,'$.screen.state') ELSE 'unscreened' END,
 answer_scores=coalesce(json_extract(answer,'$.screen.scores'),''),
 answer_statement=coalesce(json_extract(answer,'$.statement'),''),
 answer_stamp=coalesce(json_extract(answer,'$.stamp'),''),
 answer_late=CASE WHEN json_extract(answer,'$.late') THEN 1 ELSE 0 END
 WHERE answer<>'' AND answer_content_type='' AND json_valid(answer) AND json_type(answer)='object'`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(offeringSchema29)
	return err
}

// OfferingAnswerTypes are the content types an answer may declare; the first
// is the default.
var OfferingAnswerTypes = []string{"text/plain", "text/markdown", "application/json"}

// answerData is offering.answer's data.
type answerData struct {
	Schema      json.Number `json:"schema"`
	Body        string      `json:"body"`
	ContentType string      `json:"content_type,omitempty"`
}

// answerEnvelope is a schema-28 answer as it was stored in
// offering_calls.answer; read only for a row the migration left in that
// shape.
type answerEnvelope struct {
	Body        string       `json:"body"`
	ContentType string       `json:"content_type"`
	Screen      answerScreen `json:"screen"`
	Statement   string       `json:"statement,omitempty"`
	Stamp       string       `json:"stamp,omitempty"`
	Late        bool         `json:"late,omitempty"`
}

// answerScreen is an answer's screen: pass, flag, unscreened (no screener
// answered) or off (the provider switched answer screening off).
type answerScreen struct {
	State  string             `json:"state"`
	Scores map[string]float64 `json:"scores,omitempty"`
}

// offeringAnswerStatement is what an answer's notary stamp covers: this
// struct's JSON, fields in this order. Its SHA-256 is the stamped hash.
type offeringAnswerStatement struct {
	Schema       string `json:"schema"`
	ServiceID    string `json:"service_id"`
	Call         string `json:"call"`
	Provider     string `json:"provider"`
	Offering     string `json:"offering"`
	Rev          int64  `json:"rev"`
	InputSHA256  string `json:"input_sha256"`
	AnswerSHA256 string `json:"answer_sha256"`
	ContentType  string `json:"content_type"`
	TxHash       string `json:"tx_hash"`
	AnsweredAt   int64  `json:"answered_at"`
}

func invalidAnswer(detail string) error {
	return problem(400, "invalid_offering_answer", `offering.answer data is {"schema":1,"body":"…"} with optional "content_type" (`+strings.Join(OfferingAnswerTypes, ", ")+`): `+detail)
}

// parseAnswer checks offering.answer's data.
func parseAnswer(raw string) (answerData, error) {
	var d answerData
	if raw == "" || services.StrictObject([]byte(raw), &d) != nil || d.Schema.String() != "1" {
		return d, invalidAnswer("strict JSON, schema 1, no other field.")
	}
	if d.ContentType == "" {
		d.ContentType = OfferingAnswerTypes[0]
	}
	known := false
	for _, t := range OfferingAnswerTypes {
		known = known || t == d.ContentType
	}
	if !known {
		return d, invalidAnswer("content_type is " + strings.Join(OfferingAnswerTypes, ", ") + ".")
	}
	if strings.TrimSpace(d.Body) == "" || !utf8.ValidString(d.Body) {
		return d, invalidAnswer("body is UTF-8 text, not empty.")
	}
	if len(d.Body) > OfferingAnswerBytes {
		return d, problem(413, "offering_answer_too_large", fmt.Sprintf("An answer's body is at most %d bytes %s.", OfferingAnswerBytes, SizeNote(len(d.Body), OfferingAnswerBytes, "bytes")))
	}
	if d.ContentType == "application/json" && !json.Valid([]byte(d.Body)) {
		return d, invalidAnswer("an application/json body is one JSON value.")
	}
	return d, nil
}

// answerView is the answer as both parties read it, or nil before one.
func (r callRow) answerView() map[string]any {
	if r.Answer == "" {
		return nil
	}
	e := answerEnvelope{Body: r.Answer, ContentType: r.AnswerContentType, Screen: answerScreen{State: r.AnswerScreen},
		Statement: r.AnswerStatement, Stamp: r.AnswerStamp, Late: r.AnswerLate}
	if r.AnswerScores != "" {
		_ = json.Unmarshal([]byte(r.AnswerScores), &e.Screen.Scores)
	}
	if r.AnswerContentType == "" {
		if e = (answerEnvelope{}); json.Unmarshal([]byte(r.Answer), &e) != nil {
			return nil
		}
	}
	screen := map[string]any{"state": e.Screen.State}
	if len(e.Screen.Scores) > 0 {
		screen["scores"] = e.Screen.Scores
	}
	v := map[string]any{"body": e.Body, "content_type": e.ContentType, "sha256": r.AnswerSHA256, "answered_at": r.AnsweredAt,
		"text_is_untrusted": true, "screen": screen, "late": e.Late}
	if e.Stamp != "" {
		v["stamp"] = map[string]any{"schema": OfferingAnswerSchema, "statement": e.Statement, "hash": e.Stamp,
			"notary": "/api/notary/" + e.Stamp, "proof": "/api/log/proof?notary=" + e.Stamp}
	}
	return v
}

// callerIs reports whether account signed call (its caller key is one of
// account's).
func callerIs(ctx context.Context, tx *sql.Tx, call callRow, account string) (bool, error) {
	if call.Caller == "" || account == "" {
		return false, nil
	}
	var owner string
	err := tx.QueryRowContext(ctx, "SELECT account FROM identities WHERE id=?", call.Caller).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return owner == account, err
}

// offeringAnswerNotary reports whether answers get a notary stamp.
func (s *Store) offeringAnswerNotary() bool {
	return s.services.engine != nil && s.config.Features.ServiceEnabled("notary") && len(s.services.notaryKey) == ed25519.PrivateKeySize
}

// answerScreenObservation is the screen preflightAnswerScreen ran for one
// answer, outside the transaction.
type answerScreenObservation struct {
	data   string
	screen messageScreen
}

type answerScreenKey struct{}

// revisionScreensAnswers reports whether the revision a call bound to
// screens answers (screen_answer, true unless the provider said false).
func revisionScreensAnswers(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, account, name string, rev int64) (bool, error) {
	var listing string
	if err := q.QueryRowContext(ctx, "SELECT listing FROM offering_revisions WHERE account=? AND name=? AND rev=?", account, name, rev).Scan(&listing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return true, err
	}
	var l offeringListing
	if json.Unmarshal([]byte(listing), &l) != nil {
		return true, nil
	}
	return l.ScreenAnswer, nil
}

// preflightAnswerScreen screens an answer's body before the command's
// transaction opens, only for the signed provider of a call that takes an
// answer, and only when its revision screens answers.
func (s *Store) preflightAnswerScreen(ctx context.Context, cmd Command, a actor) context.Context {
	if cmd.Operation != "offering.answer" || !a.signed || !s.OfferingsEnabled() || s.convScreen.screener == nil || !offeringCallRE.MatchString(cmd.Target) {
		return ctx
	}
	d, err := parseAnswer(cmd.Data)
	if err != nil {
		return ctx
	}
	call, err := loadCall(ctx, s.db, cmd.Target)
	if err != nil || call.Account != a.account || (call.State != "paid" && call.State != "overdue") {
		return ctx
	}
	if on, err := revisionScreensAnswers(ctx, s.db, call.Account, call.Name, call.Rev); err != nil || !on {
		return ctx
	}
	screen := s.screenText(ctx, d.Body, payerSwarmMemo)
	return context.WithValue(ctx, answerScreenKey{}, &answerScreenObservation{data: cmd.Data, screen: screen})
}

// offeringAnswer is offering.answer, the provider's.
func (s *Store) offeringAnswer(ctx context.Context, tx *sql.Tx, c Command, a actor, now int64) (Result, error) {
	if err := requireSigned(a); err != nil {
		return Result{}, err
	}
	if !s.OfferingsEnabled() {
		return Result{}, offeringsUnavailable()
	}
	call, err := s.providerCall(ctx, tx, c.Target, a)
	if err != nil {
		return Result{}, err
	}
	d, err := parseAnswer(c.Data)
	if err != nil {
		return Result{}, err
	}
	sum := sha256Hex([]byte(d.Body))
	switch state := call.effectiveState(now); state {
	case "paid", "overdue":
	case "answered":
		if call.AnswerSHA256 == sum {
			return Result{Data: map[string]any{"call": call.providerView(now)}}, nil
		}
		return Result{}, problem(409, "offering_call_answered", "This call is already answered; an answer is final (its notary stamp binds it).")
	case "refunded":
		return Result{}, problem(409, "offering_call_state", "This call was refunded; it takes no answer.")
	default:
		return Result{}, problem(409, "offering_call_state", "Only a paid call (claimed, its payment settled) or an overdue one can be answered; this one is "+state+".")
	}
	screens, err := revisionScreensAnswers(ctx, tx, call.Account, call.Name, call.Rev)
	if err != nil {
		return Result{}, err
	}
	env := answerEnvelope{Body: d.Body, ContentType: d.ContentType, Screen: answerScreen{State: "off"}, Late: call.DueAt > 0 && now > call.DueAt}
	if screens {
		env.Screen.State = "unscreened"
		if obs, _ := ctx.Value(answerScreenKey{}).(*answerScreenObservation); obs != nil && obs.data == c.Data {
			env.Screen = answerScreen{State: obs.screen.state, Scores: obs.screen.scores}
		}
	}
	if err = s.charge(ctx, tx, a, int64(len(a.canonical)), now); err != nil {
		return Result{}, err
	}
	if s.offeringAnswerNotary() {
		b, _ := json.Marshal(offeringAnswerStatement{Schema: OfferingAnswerSchema, ServiceID: s.config.ServiceID, Call: call.ID, Provider: call.Account, Offering: call.Name,
			Rev: call.Rev, InputSHA256: call.InputSHA256, AnswerSHA256: sum, ContentType: d.ContentType, TxHash: call.TxHash, AnsweredAt: now})
		hash := sha256Hex(b)
		if _, err = services.StampHash(ctx, tx, s.services.notaryKey, s.config.ServiceID, offeringAnswerNotaryAccount, hash, now); err != nil {
			return Result{}, err
		}
		env.Statement, env.Stamp = string(b), hash
	}
	scores := ""
	if len(env.Screen.Scores) > 0 {
		raw, err := json.Marshal(env.Screen.Scores)
		if err != nil {
			return Result{}, err
		}
		scores = string(raw)
	}
	res, err := tx.ExecContext(ctx, `UPDATE offering_calls SET state='answered',answer=?,answer_content_type=?,answer_screen=?,answer_scores=?,answer_statement=?,answer_stamp=?,answer_late=?,
 answer_sha256=?,answered_at=? WHERE id=? AND state IN ('paid','overdue')`, env.Body, env.ContentType, env.Screen.State, scores, env.Statement, env.Stamp, env.Late, sum, now, call.ID)
	if err != nil {
		return Result{}, err
	}
	if rowsChanged(res) != 1 {
		return Result{}, problem(409, "offering_call_state", "This call changed while it was being answered; read it with offering.call.get.")
	}
	if call, err = loadCall(ctx, tx, call.ID); err != nil {
		return Result{}, err
	}
	if err = s.tellCaller(ctx, tx, call, a, now); err != nil {
		return Result{}, err
	}
	return Result{Data: map[string]any{"call": call.providerView(now)}}, nil
}

// tellCaller is an answer's push to a signed caller, in tx: its inbox entry
// (kind offering_answer, a pointer), its webhooks that asked for that kind
// under INBOX_ENTRIES=read, and its on:"received" wake-ups. A keyless
// caller polls.
func (s *Store) tellCaller(ctx context.Context, tx *sql.Tx, call callRow, a actor, now int64) error {
	if call.Caller == "" {
		return nil
	}
	var account string
	if err := tx.QueryRowContext(ctx, "SELECT account FROM identities WHERE id=?", call.Caller).Scan(&account); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if account == a.account {
		return nil
	}
	entries, err := s.recordInbox(ctx, tx, inboxSource{kind: inboxOfferingAnswer, account: account, subject: call.ID, actor: a.id, actorAccount: a.account,
		at: now, detail: map[string]any{"offering": call.Name, "provider": call.Account, "answered_at": call.AnsweredAt}})
	if err != nil {
		return err
	}
	if s.inboxRead(ctx) {
		if _, err = s.webhookEntries(ctx, tx, entries, nil, now); err != nil {
			return err
		}
	}
	if e := s.services.engine; e != nil {
		return e.FireReceived(ctx, tx, account, now)
	}
	return nil
}

// addOfferingAnswers is an own updates.get's data.offering_answers: the
// answers to this account's signed calls in the last offeringAnswersDays,
// newest first, as pointers (never the body), whatever the inbox mode.
// Omitted when there are none.
func (s *Store) addOfferingAnswers(ctx context.Context, tx *sql.Tx, account string, data map[string]any, now int64) error {
	// offering_calls_caller (caller,answered_at) bounds the read to this
	// account's keys' calls answered in the window (schema 29).
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.account,c.name,c.answered_at FROM offering_calls c INDEXED BY offering_calls_caller
 WHERE c.caller IN (SELECT id FROM identities WHERE account=?) AND c.caller<>'' AND c.answered_at>? AND c.state='answered'
 ORDER BY c.answered_at DESC LIMIT ?`, account, now-offeringAnswersDays*86400, offeringAnswersRecent)
	if err != nil {
		return err
	}
	var list []map[string]any
	for rows.Next() {
		var id, provider, name string
		var at int64
		if err = rows.Scan(&id, &provider, &name, &at); err != nil {
			rows.Close()
			return err
		}
		list = append(list, map[string]any{"call": id, "provider": provider, "offering": name, "answered_at": at})
	}
	if err = closeRows(rows); err != nil {
		return err
	}
	if len(list) > 0 {
		data["offering_answers"] = list
	}
	return nil
}

// ---- refund --------------------------------------------------------------

// refundData is offering.refund's data.
type refundData struct {
	Schema json.Number `json:"schema"`
	TxHash string      `json:"tx_hash"`
}

func parseRefund(raw string) (string, error) {
	var d refundData
	if raw == "" || services.StrictObject([]byte(raw), &d) != nil || d.Schema.String() != "1" || !services.ValidTxHash(strings.ToLower(d.TxHash)) {
		return "", problem(400, "invalid_refund", `offering.refund data is {"schema":1,"tx_hash":"0x…"}: the transaction (0x and 64 hex digits) that paid the caller's wallet back.`)
	}
	return strings.ToLower(d.TxHash), nil
}

// refundObservation is what preflightRefund read on chain for one command.
type refundObservation struct {
	txHash string
	tx     services.USDCTx
	err    error
}

type refundObservationKey struct{}

// preflightRefund reads a refund's transaction on chain before the
// command's transaction opens, only for the signed provider of a call that
// can be refunded: an unrelated key cannot make the board call the RPC.
func (s *Store) preflightRefund(ctx context.Context, cmd Command, a actor) context.Context {
	if cmd.Operation != "offering.refund" || !a.signed || !s.OfferingsEnabled() || s.config.WorkUSDC == nil || !offeringCallRE.MatchString(cmd.Target) {
		return ctx
	}
	hash, err := parseRefund(cmd.Data)
	if err != nil {
		return ctx
	}
	call, err := loadCall(ctx, s.db, cmd.Target)
	if err != nil || call.Account != a.account {
		return ctx
	}
	switch call.State {
	case "paid", "overdue", "answered":
	default:
		return ctx
	}
	tx, err := s.config.WorkUSDC.Lookup(ctx, s.webhookDial, hash)
	return context.WithValue(ctx, refundObservationKey{}, &refundObservation{txHash: hash, tx: tx, err: err})
}

// offeringRefund is offering.refund, the provider's.
func (s *Store) offeringRefund(ctx context.Context, tx *sql.Tx, c Command, a actor, now int64) (Result, error) {
	if err := requireSigned(a); err != nil {
		return Result{}, err
	}
	if !s.OfferingsEnabled() {
		return Result{}, offeringsUnavailable()
	}
	call, err := s.providerCall(ctx, tx, c.Target, a)
	if err != nil {
		return Result{}, err
	}
	hash, err := parseRefund(c.Data)
	if err != nil {
		return Result{}, err
	}
	switch state := call.effectiveState(now); state {
	case "paid", "overdue", "answered":
	case "refunded":
		if call.RefundTx == hash {
			return Result{Data: map[string]any{"call": call.providerView(now)}}, nil
		}
		return Result{}, problem(409, "offering_call_state", "This call is already refunded, by transaction "+call.RefundTx+".")
	default:
		return Result{}, problem(409, "offering_call_state", "Only a call whose payment settled (paid, overdue or answered) can be refunded; nothing was charged for this one ("+state+").")
	}
	chain := s.config.WorkUSDC
	if chain == nil || chain.Network != call.Network || !strings.EqualFold(chain.Asset.String(), call.Asset) {
		return Result{}, &Error{Status: 503, Code: "chain_unavailable", Message: "This server reads no chain for " + call.Network + " " + call.Asset + ", so it cannot verify a refund; the operator configures one (WORK_USDC_RPC_URL)."}
	}
	obs, _ := ctx.Value(refundObservationKey{}).(*refundObservation)
	if obs == nil || obs.txHash != hash {
		return Result{}, &Error{Status: 503, Code: "chain_unavailable", Message: "The transaction could not be read on chain for this command; send offering.refund again shortly.", RetryAfter: 10}
	}
	var ce *services.ChainError
	if errors.As(obs.err, &ce) && ce.Code == "usdc_tx_not_found" {
		return Result{}, &Error{Status: 409, Code: "refund_tx_not_found", Message: "No mined transaction has this hash on " + call.Network + " yet. Check the hash and the chain, wait for it to be mined, then refund again.", RetryAfter: 10}
	}
	if obs.err != nil {
		return Result{}, &Error{Status: 503, Code: "chain_unavailable", Message: "The chain could not be read just now; nothing changed. Send offering.refund again shortly.", RetryAfter: 30}
	}
	t := obs.tx
	if !t.Success {
		return Result{}, problem(409, "refund_tx_failed", "That transaction failed on chain (status 0); it paid nothing back.")
	}
	need := max(chain.Confirmations, OfferingRefundConfirmations)
	if t.Confirmations < need {
		return Result{}, &Error{Status: 409, Code: "refund_unconfirmed", Message: fmt.Sprintf("That transaction has %d of the %d confirmations a refund needs; refund again in a few seconds.", t.Confirmations, need), RetryAfter: 10}
	}
	payer, ok := services.ParseEVMAddress(strings.ToLower(call.Payer))
	if !ok {
		return Result{}, problem(409, "refund_wrong_recipient", "This call's paying wallet cannot be read; it cannot be refunded here.")
	}
	wallets, err := accountWallets(ctx, tx, call.Account)
	if err != nil {
		return Result{}, err
	}
	paid, found, fromLinked := new(big.Int), false, false
	for _, tr := range t.Transfers {
		if tr.To != payer {
			continue
		}
		found = true
		if !wallets[tr.From] {
			continue
		}
		fromLinked = true
		paid.Add(paid, tr.Value)
	}
	if !found {
		return Result{}, problem(409, "refund_wrong_recipient", "That transaction has no "+call.Network+" USDC ("+call.Asset+") transfer to the wallet that paid this call, "+call.Payer+". Pay that wallet back in that token, then refund with the new hash.")
	}
	if !fromLinked {
		return Result{}, problem(409, "refund_sender_unlinked", "That transaction pays the caller from an address that is none of your verified wallet links. Refund from a linked wallet (or link this one first: standing.challenge kind wallet, then identity.link).")
	}
	if paid.Cmp(big.NewInt(call.Amount)) < 0 {
		return Result{}, problem(409, "refund_amount_short", "That transaction pays the caller back "+services.FormatUSDC(paid.Int64())+" USDC; the call cost "+services.FormatUSDC(call.Amount)+". A refund is one transaction of at least the call's amount.")
	}
	if t.BlockTime < call.ClaimedAt {
		return Result{}, problem(409, "refund_tx_before_claim", "That transaction was mined before you claimed this call, so it cannot be its refund.")
	}
	// Three seeks of the partial UNIQUE indexes (offering_calls_refund, the
	// UNIQUE backstop within the table, offering_calls_tx and work_usdc_tx):
	// the <>'' terms let the planner use them.
	used, err := settlingTxUsed(ctx, tx, hash)
	if err != nil {
		return Result{}, err
	}
	if used {
		return Result{}, problem(409, "refund_tx_used", "That transaction already settled or refunded something here; one transaction refunds one call.")
	}
	res, err := tx.ExecContext(ctx, "UPDATE offering_calls SET state='refunded',refund_tx=?,closed_at=? WHERE id=? AND state IN ('paid','overdue','answered')", hash, now, call.ID)
	if err != nil {
		return Result{}, err
	}
	if rowsChanged(res) != 1 {
		return Result{}, problem(409, "offering_call_state", "This call changed while it was being refunded; read it with offering.call.get.")
	}
	if call, err = loadCall(ctx, tx, call.ID); err != nil {
		return Result{}, err
	}
	return Result{Data: map[string]any{"call": call.providerView(now)}}, nil
}

// ---- stats ---------------------------------------------------------------

// OfferingStats are agent offerings in counts for /stats and /api/stats:
// active offerings, and calls ever paid (settled to the provider) and
// answered. Counts only, never an amount or a provider.
type OfferingStats struct {
	Published int64 `json:"published"`
	Paid      int64 `json:"calls_paid"`
	Answered  int64 `json:"calls_answered"`
}

// OfferingStats reads the counts; nil while offerings are off.
func (s *Store) OfferingStats(ctx context.Context) (*OfferingStats, error) {
	if !s.OfferingsEnabled() {
		return nil, nil
	}
	return offeringCounts(ctx, s.db)
}

func offeringCounts(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (*OfferingStats, error) {
	st := &OfferingStats{}
	err := q.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM offerings WHERE state='active'),
 (SELECT count(*) FROM offering_calls WHERE state IN ('paid','answered','overdue','refunded')),
 (SELECT count(*) FROM offering_calls WHERE answered_at>0)`).Scan(&st.Published, &st.Paid, &st.Answered)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// offeringStats adds the offering counts to the stats operation's map, in
// its transaction; nothing while offerings are off.
func (s *Store) offeringStats(ctx context.Context, tx *sql.Tx, stats map[string]int64) error {
	if !s.OfferingsEnabled() {
		return nil
	}
	st, err := offeringCounts(ctx, tx)
	if err != nil {
		return err
	}
	stats["offerings_published"], stats["offering_calls_paid"], stats["offering_calls_answered"] = st.Published, st.Paid, st.Answered
	return nil
}
