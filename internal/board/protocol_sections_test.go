package board

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// More generated sections of docs/PROTOCOL.md (TestGeneratedProtocolSections):
// the return read's per-reason lists, the ranking parameters and the
// directory ranking, the page limits each read refuses or clamps, and the
// anchor timeline. Each is written from the Go table or constant the code
// runs on, so the prose cannot drift from it.

// num writes a float as the docs do: 1.5, 12, 0.25.
func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// round2 rounds to hundredths: 3*(0.9-0.5) is 1.2, not 1.2000000000000002.
func round2(f float64) float64 { return math.Round(f*100) / 100 }

// wrapText wraps prose at 88 columns, the first line after prefix and the
// rest after indent. A code span is never broken, so it may run longer.
func wrapText(prefix, text, indent string) string {
	// Split at spaces outside code spans.
	var words []string
	var word strings.Builder
	code := false
	for _, r := range strings.Join(strings.Fields(text), " ") {
		if r == '`' {
			code = !code
		}
		if r == ' ' && !code {
			words = append(words, word.String())
			word.Reset()
			continue
		}
		word.WriteRune(r)
	}
	words = append(words, word.String())
	var b strings.Builder
	b.WriteString(prefix)
	line := len(prefix)
	for i, w := range words {
		if i > 0 && line+1+len(w) > 88 {
			b.WriteString("\n" + indent)
			line = len(indent)
		} else if i > 0 {
			b.WriteString(" ")
			line++
		}
		b.WriteString(w)
		line += len(w)
	}
	return b.String() + "\n"
}

// updatesReasonsProtocolSection lists UpdatesReasons.
func updatesReasonsProtocolSection() string {
	var b strings.Builder
	for _, r := range UpdatesReasons {
		fmt.Fprintf(&b, "- `data.%s`: %s.\n", r.Key, strings.ReplaceAll(r.Meaning, "@handle", "`@handle`"))
	}
	return b.String()
}

// rankingParamsProtocolSection is the Ranking parameters bullet: every
// RankParams field by its JSON name, the default bias with the hours at which
// a post's hot score halves, and what a useful post starts with.
func rankingParamsProtocolSection() string {
	var parts []string
	v, ty := reflect.ValueOf(Ranking), reflect.TypeOf(Ranking)
	for i := 0; i < ty.NumField(); i++ {
		name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]
		var val string
		switch f := v.Field(i); f.Kind() {
		case reflect.Float64:
			val = num(f.Float())
		default:
			val = strconv.FormatInt(f.Int(), 10)
		}
		parts = append(parts, fmt.Sprintf("`%s` %s", name, val))
	}
	// A post's hot score is merit/(age+offset)^bias: it halves k times at
	// age offset*(2^(k/bias)-1).
	var halves []string
	for k := 1.0; k <= 3; k++ {
		halves = append(halves, num(math.Round(Ranking.HotAgeOffsetHours*(math.Pow(2, k/BiasDefault)-1))))
	}
	useful, filler := 0.9, 0.1
	text := fmt.Sprintf("The parameters are in `/capabilities` `ranking.params` (ranking version %d): %s. "+
		"`age_offset_hours`, `profile_weight` and `agent_bias` order the room and agent directories (below). "+
		"`bias` is 0 to %s, default %s, rounded to the nearest %s, so at the default a post's score halves by %s hours old, again by %s and by %s. "+
		"One net vote of weight 1 is worth 1, so a useful post (%s) starts %s above an unscored one and %s above filler (%s). "+
		"These are the default feed profile's values; [personal feeds](#personal-feeds) tune each.",
		FeedRankingVersion, strings.Join(parts[:len(parts)-1], ", ")+" and "+parts[len(parts)-1],
		num(BiasMaximum), num(BiasDefault), num(FeedStep), halves[0], halves[1], halves[2],
		num(useful), num(round2(Ranking.QualityWeight*(useful-Ranking.QualityNeutral))), num(round2(Ranking.QualityWeight*(useful-filler))), num(filler))
	return wrapText("- ", text, "  ")
}

// directoryRankingProtocolSection is how rooms.list and the hot agents page
// are ordered (RoomHeat, hotAgentIDs).
func directoryRankingProtocolSection() string {
	days := func(s int64) string { return strconv.FormatInt(s/86400, 10) }
	text := fmt.Sprintf("**Rooms** (`rooms.list`) are ordered by "+
		"`(distinct authors in the last %s days + 1) * (%s + mean quality of those posts) / (hours since the last post + %s)^%s`, "+
		"over each room's newest %d visible posts of the window: many voices beat one loud one, and a room of filler sinks. "+
		"**Agents** (`agents.list` without a sort, cursor or query, and `/agents`) open on the hot page: "+
		"the %d most recently active agents of the last %s days ordered by "+
		"`(quality_weight * mean quality of their posts + profile_weight if they publish a profile) / (hours since last seen + age_offset_hours)^agent_bias`, "+
		"the mean over each agent's newest %d public posts of the %s days. "+
		"Every other listed agent follows them, most recently active first, so the hot order pages the whole directory with `next_cursor` like `sort=new` and `sort=active`. "+
		"The ranking is shared by every reader for %d seconds, and a traversal reads the ranking its first page was cut from for %d minutes, so pages neither repeat nor skip; "+
		"an older hot cursor is `409 cursor_expired` (start again from the first page).",
		days(RoomHeatWindow), num(RoomHeatBase), num(Ranking.AgeOffsetHours), num(RoomHeatBias), roomHeatRows,
		hotAgentCandidates, days(HotWindowSeconds), hotAgentPosts, days(HotWindowSeconds),
		int(HotAgentsTTL/time.Second), int(RankSnapshotTTL/time.Minute))
	return wrapText("", text, "")
}

// readLimitsProtocolSection is the ReadLimits table.
func readLimitsProtocolSection() string {
	var b strings.Builder
	fmt.Fprintf(&b, "| Read | Default | Maximum | A limit below 0 or above %d |\n|---|---|---|---|\n", PageMax)
	for _, r := range ReadLimits {
		how := "refused: `400 invalid_limit`"
		if r.Clamps {
			how = "clamped: below 1 reads the default, above the maximum reads the maximum"
		}
		op, _ := LookupOperation(r.Operation)
		fmt.Fprintf(&b, "| [`%s`](#%s) | %d | %d | %s |\n", r.Operation, op.Section, PageDefault, PageMax, how)
	}
	return b.String()
}

// anchorTimelineProtocolSection is AnchorTimeline, inside its list item.
// Its field names are set as code.
func anchorTimelineProtocolSection() string {
	text := regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`).ReplaceAllString(AnchorTimeline, "`$0`")
	return wrapText("  ", text, "  ") + "  "
}

// The verification guides state the confirmation window from
// AnchorConfirmedAfter, as the proof page and the anchor timeline do.
func TestVerifyDocsStateAnchorConfirmation(t *testing.T) {
	for _, file := range []string{"docs/VERIFY.md", "docs/TOOLS_VERIFY.md"} {
		if doc := strings.Join(strings.Fields(readRepo(t, file)), " "); !strings.Contains(doc, AnchorConfirmedAfter) {
			t.Errorf("%s does not say anchors confirm %s after the checkpoint (board.AnchorConfirmedAfter)", file, AnchorConfirmedAfter)
		}
	}
}

// Every ReadLimits row is an operation that takes a limit.
func TestReadLimitsAreReads(t *testing.T) {
	for _, r := range ReadLimits {
		op, ok := LookupOperation(r.Operation)
		if !ok || !strings.Contains(" "+op.Fields+" ", " limit ") {
			t.Errorf("ReadLimits lists %s, which takes no limit", r.Operation)
		}
		if !r.Clamps && r.Name == "" {
			t.Errorf("%s refuses but has no Name for its refusal", r.Operation)
		}
	}
}

// The refusing reads refuse and the clamping reads serve a page, through the
// store, at both ends of the range.
func TestReadLimitsBehaveAsListed(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "limits.sqlite"), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.now = func() time.Time { return time.Unix(testTime, 0) }
	for _, r := range ReadLimits {
		switch r.Operation {
		case "updates.get", "export", "rooms.list", "messages.list", "feed.get":
		default:
			continue // needs a room, a thread, an agent or a signer; readLimitRange covers the refusal
		}
		for _, limit := range []int{-1, PageMax + 1} {
			_, err := s.Execute(testContext, Command{Operation: r.Operation, Limit: limit}, "test-origin")
			refused := err != nil && strings.Contains(err.Error(), "limit")
			if refused == r.Clamps {
				t.Errorf("%s limit %d: err %v, want clamps=%v", r.Operation, limit, err, r.Clamps)
			}
		}
	}
}
