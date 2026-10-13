package ledger

// Escrow splits (C278, standing tasks): one escrow (a pool) funds many
// smaller ones. EscrowSplit carves units out of a held escrow into a new
// escrow of the same account; EscrowJoin moves a held escrow's units into
// another. Both only move hold parts between two held escrows: every lot's
// held units, the supply and the account's balance stay as they were, no
// fee is charged, and the journal records the move as a refund out of one
// hold and a reserve into the other.

import (
	"context"
	"errors"

	"swarmmemo/internal/allowance"
)

// EscrowSplit moves amount units of the held escrow holdID into a new escrow
// of the same account named requestKey, held until at most until (no later
// than the source's end, so every lot still lasts). Splitting into a key
// that already names a hold returns that hold unchanged.
func (l *Ledger) EscrowSplit(ctx context.Context, q allowance.Querier, holdID string, amount int64, requestKey string, until, now int64) (Hold, error) {
	src, _, err := loadEscrow(ctx, q, holdID)
	if err != nil {
		return Hold{}, err
	}
	if requestKey == "" || len(requestKey) > 256 || until <= now {
		return Hold{}, errors.New("ledger: an escrow split needs a request key of 1–256 bytes and an end after now")
	}
	id, _ := escrowKeys(src.Account, requestKey)
	if h, ok, err := loadHold(ctx, q, id); err != nil || ok {
		// A retry returns the same split; a key that names a finished hold is
		// refused, so its caller never counts units that did not move.
		if err == nil && h.State != "held" {
			return Hold{}, refuse("hold_not_held")
		}
		return h, err
	}
	if src.State != "held" {
		return Hold{}, refuse("hold_not_held")
	}
	if amount <= 0 || amount > src.Max {
		return Hold{}, refuse("invalid_amount")
	}
	if until > src.ExpiresAt {
		until = src.ExpiresAt
	}
	h := Hold{ID: id, Account: src.Account, Resource: src.Resource, Max: amount, ExpiresAt: until, State: "held"}
	if _, err = q.ExecContext(ctx, "INSERT INTO ledger_holds(id,account,resource,max_units,used_units,state,request_key,service,method,created_at,expires_at) VALUES(?,?,?,?,0,'held',?,?,?,?,?)",
		h.ID, h.Account, string(h.Resource), 0, "escrow:"+requestKey, ledgerService, escrowMethod, now, h.ExpiresAt); err != nil {
		return Hold{}, err
	}
	// A credential's spend follows the units, so a release gives it back.
	if _, err = q.ExecContext(ctx, "INSERT INTO spend_limit_holds(hold_id,credential,day) SELECT ?,credential,day FROM spend_limit_holds WHERE hold_id=? ON CONFLICT(hold_id) DO NOTHING", h.ID, holdID); err != nil {
		return Hold{}, err
	}
	if err = l.moveEscrow(ctx, q, src.Hold, h, amount, now); err != nil {
		return Hold{}, err
	}
	return h, nil
}

// EscrowJoin moves every unit of the held escrow holdID into the held escrow
// intoID of the same account and resource; holdID is then refunded, empty.
// Lots that would end before intoID's end are not_transferable, and nothing
// changes.
func (l *Ledger) EscrowJoin(ctx context.Context, q allowance.Querier, holdID, intoID string, now int64) error {
	src, _, err := loadEscrow(ctx, q, holdID)
	if err != nil {
		return err
	}
	dst, _, err := loadEscrow(ctx, q, intoID)
	if err != nil {
		return err
	}
	if src.State != "held" || dst.State != "held" || holdID == intoID {
		return refuse("hold_not_held")
	}
	if src.Account != dst.Account || src.Resource != dst.Resource {
		return refuse("invalid_amount")
	}
	parts, err := loadHoldParts(ctx, q, holdID)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.ExpiresAt != 0 && p.ExpiresAt <= dst.ExpiresAt {
			return refuse("not_transferable")
		}
	}
	// A hold reads, moves and releases at most liveLotsMax parts: a join
	// that would take intoID past it is refused (the caller releases holdID
	// instead), so no hold ever has parts a release cannot reach.
	var lots int64
	if err = q.QueryRowContext(ctx, "SELECT count(DISTINCT lot_id) FROM ledger_hold_parts WHERE hold_id IN (?,?)", holdID, intoID).Scan(&lots); err != nil {
		return err
	}
	if lots > liveLotsMax {
		return refuse("too_many_parts")
	}
	if err = l.moveEscrow(ctx, q, src.Hold, dst.Hold, src.Max, now); err != nil {
		return err
	}
	// The units' credential spend goes with them: it stays counted where it
	// was spent, and a release of intoID gives it back there.
	if err = credentialJoin(ctx, q, holdID, intoID, src.Max); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE ledger_holds SET state='refunded' WHERE id=?", holdID)
	return err
}

// moveEscrow moves amount units of src's parts, in their spend order, to
// dst, and journals each part's move.
func (l *Ledger) moveEscrow(ctx context.Context, q allowance.Querier, src, dst Hold, amount, now int64) error {
	_, version, err := l.params(ctx, q, now)
	if err != nil {
		return err
	}
	parts, err := loadHoldParts(ctx, q, src.ID)
	if err != nil {
		return err
	}
	left := amount
	for _, p := range parts {
		if left == 0 {
			break
		}
		take := min(p.Units, left)
		if take == p.Units {
			_, err = q.ExecContext(ctx, "DELETE FROM ledger_hold_parts WHERE hold_id=? AND lot_id=?", src.ID, p.LotID)
		} else {
			_, err = q.ExecContext(ctx, "UPDATE ledger_hold_parts SET units=units-? WHERE hold_id=? AND lot_id=?", take, src.ID, p.LotID)
		}
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, "INSERT INTO ledger_hold_parts(hold_id,lot_id,units) VALUES(?,?,?) ON CONFLICT(hold_id,lot_id) DO UPDATE SET units=units+excluded.units", dst.ID, p.LotID, take); err != nil {
			return err
		}
		for _, e := range []entry{
			{Kind: "refund", HoldID: src.ID, Ref: escrowRef("escrow_split", dst.ID)},
			{Kind: "reserve", HoldID: dst.ID, Ref: escrowRef("escrow_split", src.ID)},
		} {
			e.Day, e.Now, e.Account, e.Resource, e.Bucket, e.LotID, e.Amount, e.Version, e.Detail = now/86400, now, src.Account, src.Resource, p.Bucket, p.LotID, take, version, "escrow split"
			if err = journal(ctx, q, e); err != nil {
				return err
			}
		}
		left -= take
	}
	if left != 0 {
		return errors.New("ledger: escrow parts do not cover its units")
	}
	if _, err = q.ExecContext(ctx, "UPDATE ledger_holds SET max_units=max_units-? WHERE id=?", amount, src.ID); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "UPDATE ledger_holds SET max_units=max_units+? WHERE id=?", amount, dst.ID)
	return err
}
