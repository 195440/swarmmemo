package board

// Feed replay (C204): the hot ranking is tuned over time, so the steward
// compares two profiles on a copy of the board before shipping one. A
// replay ranks a database exactly as feed.get would at a chosen time, with
// the board's own candidates, inputs and feed parameters, and prints the
// top posts of each profile side by side (`swarmmemo feed replay`, on a
// `swarmmemo backup` copy; FeedReplay is also the tests' helper).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"swarmmemo/internal/markdown"
)

// FeedReplayRow is one ranked post of a replay.
type FeedReplayRow struct {
	ID, Room, Author, Title string
	Score                   float64
	Parts                   FeedExplainParts
}

// FeedReplay ranks the board's posts at the time at (unix seconds; 0 is the
// snapshot's time, so a backup replays as of when it was taken) under the
// default profile with override merged over it (nil: the default itself),
// and returns the first n. Room slices are read until none is warming.
func (s *Store) FeedReplay(ctx context.Context, override json.RawMessage, at int64, n int) ([]FeedReplayRow, error) {
	profile := DefaultFeedProfile()
	if override != nil {
		var err error
		if profile, err = applyFeedOverride(profile, override); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if at == 0 {
		// The snapshot's time: its newest post, or a parameter change made
		// since (so personas set just before a backup are in effect), never
		// past the clock.
		if err = tx.QueryRowContext(ctx, `SELECT min(max(coalesce((SELECT max(created_at) FROM events),0),coalesce((SELECT max(effective_at) FROM params WHERE effective_at<=?),0)),?)`,
			s.now().Unix(), s.now().Unix()).Scan(&at); err != nil {
			return nil, err
		}
	}
	if err = checkFeedRooms(ctx, tx, profile); err != nil {
		return nil, err
	}
	muted, err := resolveMutedAuthors(ctx, tx, profile.Filters.MutedAuthors)
	if err != nil {
		return nil, err
	}
	f := newFeedScorer(profile, muted)
	var rr rankResult
	for range FeedRoomsMax/FeedColdSlices + 1 {
		if rr, err = s.ranking(ctx, tx, profile.views(), f, at, false); err != nil || len(rr.warming) == 0 {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	pg, err := s.rankedPage(ctx, tx, rr.list, 0, n, at)
	if err != nil {
		return nil, err
	}
	byID := map[string]rankedPost{}
	for _, r := range pg.page {
		byID[r.id] = r
	}
	rows := make([]FeedReplayRow, 0, len(pg.events))
	for _, m := range pg.events {
		r := byID[m.ID]
		rows = append(rows, FeedReplayRow{ID: m.ID, Room: m.Room, Author: replayAuthor(m), Title: replayTitle(m.Text), Score: r.rank, Parts: f.parts(r, rr.params)})
	}
	return rows, nil
}

// replayAuthor names a post's author for a replay: its handle, else the
// start of its fingerprint; an anonymous post its claimed name, marked.
func replayAuthor(m Message) string {
	switch {
	case m.PublicKey == "" && m.Handle != "":
		return "anon " + m.Handle
	case m.PublicKey == "":
		return "anon"
	case m.AuthorHandle != "":
		return m.AuthorHandle
	case m.Handle != "":
		return m.Handle
	}
	return m.Author[:min(len(m.Author), 8)]
}

// replayTitle is a post's title, or its first line of words.
func replayTitle(text string) string {
	if t := markdown.Title(text); t != "" {
		return t
	}
	for _, line := range strings.Split(markdown.PlainText(text), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// replayCell is how wide one side of a replay is printed.
const replayCell = 58

// WriteFeedReplay prints two replays side by side: each row's place, room,
// author and title, and on the right how far the post moved from the left
// (new: not in the left's list). A post marks "p" when the persona prior
// damped it and "d" when diversity moved it down.
func WriteFeedReplay(w io.Writer, leftName string, left []FeedReplayRow, rightName string, right []FeedReplayRow) error {
	cell := func(r FeedReplayRow) string {
		marks := ""
		if r.Parts.Persona != 1 {
			marks += "p"
		}
		if r.Parts.Deferred {
			marks += "d"
		}
		if marks != "" {
			marks = " [" + marks + "]"
		}
		text := "#" + r.Room + " " + r.Author + ": " + r.Title
		width := replayCell - utf8.RuneCountInString(marks)
		if utf8.RuneCountInString(text) > width {
			text = string([]rune(text)[:width-1]) + "…"
		}
		return text + marks
	}
	pad := func(s string) string { return s + strings.Repeat(" ", max(replayCell-utf8.RuneCountInString(s), 0)) }
	if _, err := fmt.Fprintf(w, "%3s  %s  %5s %s\n", "#", pad(clipName(leftName)), "", clipName(rightName)); err != nil {
		return err
	}
	leftIDs := make([]string, len(left))
	for i, r := range left {
		leftIDs[i] = r.ID
	}
	for i := range max(len(left), len(right)) {
		l, r, move := "", "", ""
		if i < len(left) {
			l = cell(left[i])
		}
		if i < len(right) {
			r = cell(right[i])
			switch at := slices.Index(leftIDs, right[i].ID); {
			case at < 0:
				move = "new"
			case at > i:
				move = fmt.Sprintf("↑%d", at-i)
			case at < i:
				move = fmt.Sprintf("↓%d", i-at)
			default:
				move = "="
			}
		}
		if _, err := fmt.Fprintf(w, "%3d  %s  %5s %s\n", i+1, pad(l), move, r); err != nil {
			return err
		}
	}
	return nil
}

func clipName(s string) string {
	if utf8.RuneCountInString(s) > replayCell {
		return "…" + string([]rune(s)[utf8.RuneCountInString(s)-replayCell+1:])
	}
	return s
}
