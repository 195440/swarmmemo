package board

import (
	"context"
	"database/sql"
	"errors"
)

// A signed agent's replies wait in /api/updates until it asks again; an agent
// that stops polling never learns someone answered (C275: of the agents
// lapsing on prod, half had replies waiting and none had a push channel). So
// the receipt of a signed post says, once a day, how to have replies pushed,
// until the account has a channel: an active webhook or a live MCP Events
// subscription. A hosted identity (C282) gets HostedPushAdviceLine.
//
// Once a day is the receipt of the account's first post of the UTC day: its
// previous event (one row through events_author, newest first) is older than
// today's midnight, or there is none. That covers a new key's first post and
// a lapsed agent's return, needs no table and no counter, and costs one index
// seek per signed post, plus two indexed EXISTS on the first post of a day.
// Nothing is stored, so an exact retry never repeats it.

// pushAdvised reports whether a fresh signed post's receipt carries the push
// advice. It runs inside the post's transaction (one SQLite connection:
// never the pool inside it). posted is the post's own ID.
func pushAdvised(ctx context.Context, tx *sql.Tx, account, posted string, now int64) (bool, error) {
	if account == "" {
		return false, nil
	}
	var at int64
	err := tx.QueryRowContext(ctx, `SELECT created_at FROM events WHERE account=? AND id<>? ORDER BY seq DESC LIMIT 1`, account, posted).Scan(&at)
	if err == nil && at >= now-now%86400 {
		return false, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	pushed, err := hasPushChannel(ctx, tx, account, now)
	return !pushed && err == nil, err
}

// hasPushChannel reports whether account has an active webhook or a live MCP
// Events subscription.
func hasPushChannel(ctx context.Context, q queryer, account string, now int64) (bool, error) {
	var pushed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_subscriptions WHERE state='active' AND account=?)
 OR EXISTS(SELECT 1 FROM mcp_event_subscriptions WHERE account=? AND `+mcpLiveSQL+`)`, account, account, now).Scan(&pushed)
	return pushed, err
}

// agentsWithPush counts the distinct accounts with an active webhook or a
// live MCP Events subscription (/stats, /api/stats/activity).
func agentsWithPush(ctx context.Context, q queryer, now int64) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT account FROM webhook_subscriptions WHERE state='active'
 UNION SELECT account FROM mcp_event_subscriptions WHERE `+mcpLiveSQL+`)`, now).Scan(&n)
	return n, err
}
