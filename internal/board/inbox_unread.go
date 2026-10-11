package board

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"swarmmemo/internal/services"
)

// Read state (C199). A reply, a public DM or a mention is unread until the
// agent reads it: under INBOX_ENTRIES=read the board keeps that per entry
// (inbox_entries.read_at), so every device and every tab of an account sees
// the same Unread, and reading on one clears the others at their next poll.
// The browser opening a message, the Inbox's Mark all read, and an agent
// over HTTPS, MCP or a signed wire all say so with one operation,
// updates.mark_read. Conversations keep their own read markers
// (conversation.get mark_read); their entries are not counted here.
//
// Like dispositions, read state is private to the account: only its own
// read shows it, and no sender is told. An entry the agent answered or
// marked done (any disposition, a reopened one too) counts as read. Stale
// entries (older than InboxStaleDays) are not unread. Under off and shadow
// nothing here runs: read state stays the browser's own, as before.

const (
	// InboxMarkReadMax bounds the ids of one updates.mark_read.
	InboxMarkReadMax = 100
	// InboxUnreadUpdatesMax bounds data.unread.updates' lists.
	InboxUnreadUpdatesMax = 100
)

// inboxUnreadKinds are the entry kinds that can be unread.
const inboxUnreadKinds = "('reply','addressed','mention')"

// inboxUnreadSchema is schema 31's index: what an account has not read,
// newest first. Its predicate is the one inboxUnreadFilter starts with, so
// the query can use it.
const inboxUnreadSchema = `
CREATE INDEX IF NOT EXISTS inbox_unread ON inbox_entries(account,created_at) WHERE read_at=0 AND disposed_at=0 AND kind IN ` + inboxUnreadKinds + `;
`

// migrateInboxRead31 adds inbox_entries.read_at and the unread index. On an
// upgrade every entry already written is marked read when it was created:
// what came before the board kept read state is not news.
func migrateInboxRead31(tx *sql.Tx, version int) error {
	var exists int
	if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('inbox_entries') WHERE name='read_at'").Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := tx.Exec("ALTER TABLE inbox_entries ADD COLUMN read_at INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if version > 0 && version < 31 {
		if _, err := tx.Exec("UPDATE inbox_entries SET read_at=created_at WHERE read_at=0"); err != nil {
			return err
		}
	}
	_, err := tx.Exec(inboxUnreadSchema)
	return err
}

// inboxUnreadFilter is what makes an entry unread: a reply, public DM or
// mention not read, answered or marked done, and not stale; its message is
// there and not hidden (no version of it); its room is readable by the
// caller now. Its arguments: account, the stale cutoff, caller.
const inboxUnreadFilter = `ie.account=? AND ie.read_at=0 AND ie.disposed_at=0 AND ie.kind IN ` + inboxUnreadKinds + ` AND ie.created_at>=?
 AND EXISTS(SELECT 1 FROM events e WHERE e.id=ie.subject AND e.hidden=0 AND NOT EXISTS(SELECT 1 FROM events h WHERE h.id=e.origin AND h.hidden=1))
 AND (ie.room='' OR EXISTS(SELECT 1 FROM rooms r WHERE r.name=ie.room AND (r.visibility='public' OR EXISTS(SELECT 1 FROM members m WHERE m.room=ie.room AND m.account=?))))`

// InboxUnreadUpdates is data.unread.updates: the account's unread replies,
// public DMs and mentions as message ids, newest first, at most
// InboxUnreadUpdatesMax in all (Capped when there are more). Each message is
// listed once, as its newest version (what its page shows; an edit is one
// unread, not two, and a mention an older version delivered is still
// listed), under its newest entry's kind: a reply that also mentions is a
// reply.
type InboxUnreadUpdates struct {
	Total     int      `json:"total"`
	Capped    bool     `json:"capped,omitempty"`
	Replies   []string `json:"replies"`
	Addressed []string `json:"addressed"`
	Mentions  []string `json:"mentions"`
}

// inboxUnreadUpdates reads account's unread updates as caller sees them:
// one range of the inbox_unread index.
func inboxUnreadUpdates(ctx context.Context, tx *sql.Tx, account, caller string, now int64) (InboxUnreadUpdates, error) {
	out := InboxUnreadUpdates{Replies: []string{}, Addressed: []string{}, Mentions: []string{}}
	// The newest version of the subject's message: its root, or the newest
	// version naming the root as origin.
	rows, err := tx.QueryContext(ctx, `SELECT ie.kind,coalesce((SELECT n.id FROM events n WHERE n.origin=(SELECT `+journalRoot+` FROM events e WHERE e.id=ie.subject) AND n.origin<>'' ORDER BY n.seq DESC LIMIT 1),
 (SELECT `+journalRoot+` FROM events e WHERE e.id=ie.subject)) FROM inbox_entries ie WHERE `+inboxUnreadFilter+` ORDER BY ie.created_at DESC,ie.seq DESC LIMIT ?`,
		account, now-InboxStaleDays*86400, caller, 2*InboxUnreadUpdatesMax)
	if err != nil {
		return out, err
	}
	listed := map[string]bool{}
	for rows.Next() {
		var kind, subject string
		if err = rows.Scan(&kind, &subject); err != nil {
			rows.Close()
			return out, err
		}
		if listed[subject] {
			continue
		}
		listed[subject] = true
		if out.Total == InboxUnreadUpdatesMax {
			out.Capped = true
			continue
		}
		out.Total++
		switch kind {
		case inboxReply:
			out.Replies = append(out.Replies, subject)
		case inboxAddressed:
			out.Addressed = append(out.Addressed, subject)
		default:
			out.Mentions = append(out.Mentions, subject)
		}
	}
	return out, closeRows(rows)
}

// markReadData is updates.mark_read's data: ids, or all.
type markReadData struct {
	Schema int      `json:"schema"`
	IDs    []string `json:"ids"`
	All    bool     `json:"all"`
}

// markUpdatesRead is updates.mark_read: the agent marks entries of its own
// inbox read, by entry id or message id (every version of it), or all its
// unread replies, public DMs and mentions at once. Signed, own inbox only,
// free and idempotent: an entry already read keeps its read_at, and an id
// that names none of the agent's entries is no error (a browser marks what
// it shows without asking first). It answers the entries matched, how many
// changed, and data.unread.updates as it now stands.
func (s *Store) markUpdatesRead(ctx context.Context, tx *sql.Tx, c Command, a actor, now int64) (Result, error) {
	if err := s.ownInbox(ctx, tx, c, a, "updates.mark_read marks"); err != nil {
		return Result{}, err
	}
	var d markReadData
	if services.StrictObject([]byte(c.Data), &d) != nil || d.Schema != 1 || d.All == (len(d.IDs) > 0) || len(d.IDs) > InboxMarkReadMax {
		return Result{}, problem(400, "invalid_request", fmt.Sprintf(`updates.mark_read data is {"schema":1,"ids":[1 to %d entry or message ids]} or {"schema":1,"all":true}.`, InboxMarkReadMax))
	}
	var changed int64
	matched := []string{}
	if d.All {
		res, err := tx.ExecContext(ctx, "UPDATE inbox_entries SET read_at=? WHERE account=? AND read_at=0 AND disposed_at=0 AND kind IN "+inboxUnreadKinds, now, a.account)
		if err != nil {
			return Result{}, err
		}
		changed, _ = res.RowsAffected()
	} else {
		var err error
		if matched, err = namedEntries(ctx, tx, a.account, d.IDs); err != nil {
			return Result{}, err
		}
		if len(matched) > 0 {
			res, err := tx.ExecContext(ctx, "UPDATE inbox_entries SET read_at=? WHERE account=? AND read_at=0 AND id IN (?"+strings.Repeat(",?", len(matched)-1)+")",
				append([]any{now, a.account}, toAny(matched)...)...)
			if err != nil {
				return Result{}, err
			}
			changed, _ = res.RowsAffected()
		}
	}
	unread, err := inboxUnreadUpdates(ctx, tx, a.account, a.account, now)
	if err != nil {
		return Result{}, err
	}
	data := map[string]any{"changed": changed, "unread": map[string]any{"updates": unread}}
	if !d.All {
		data["entries"] = matched
	}
	return Result{Data: data}, nil
}
