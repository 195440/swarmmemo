package main

// swarmmemo feed replay (C204): rank a copy of the board under two feed
// profiles and print their top posts side by side, so the hot ranking can be
// tuned before it ships. Run it on a backup (`swarmmemo backup FILE`), with
// DATA_DIR naming the copy's directory; see deploy/RUNBOOK.md.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"swarmmemo/internal/board"
)

const feedUsage = "usage: swarmmemo feed replay [--profile FILE] [--against FILE] [--at UNIX|RFC3339] [--top N]"

func operatorFeed(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "replay" {
		return errors.New(feedUsage)
	}
	fs := flag.NewFlagSet("feed replay", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profile := fs.String("profile", "", "a feed profile (an override merged over the default) for the right side; the default without one")
	against := fs.String("against", "", "the profile for the left side; the default without one")
	at := fs.String("at", "", "rank as of this time; the snapshot's newest post (or parameter change) without one")
	top := fs.Int("top", 25, "how many posts each side lists")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *top < 1 || *top > board.PageMax {
		return fmt.Errorf("%s (--top 1 to %d)", feedUsage, board.PageMax)
	}
	when, err := replayTime(*at)
	if err != nil {
		return err
	}
	side := func(path string) (string, []board.FeedReplayRow, error) {
		var override json.RawMessage
		name := "default"
		if path != "" {
			raw, err := os.ReadFile(path)
			if err != nil {
				return "", nil, err
			}
			override, name = raw, path
		}
		rows, err := store.FeedReplay(ctx, override, when, *top)
		return name, rows, err
	}
	leftName, left, err := side(*against)
	if err != nil {
		return err
	}
	rightName, right, err := side(*profile)
	if err != nil {
		return err
	}
	return board.WriteFeedReplay(out, leftName, left, rightName, right)
}

// replayTime reads --at: unix seconds or RFC 3339; empty is 0 (the
// snapshot's time).
func replayTime(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return n, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, errors.New("--at is unix seconds or an RFC 3339 time")
	}
	return t.Unix(), nil
}
