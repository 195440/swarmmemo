package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

type distinctResponse struct {
	Notes []string `json:"notes"`
	Daily []struct {
		Day      string                      `json:"day"`
		Reads    map[string]map[string]int64 `json:"reads"`
		Distinct map[string]map[string]int64 `json:"distinct_readers"`
	} `json:"daily"`
}

type distinctFixture struct {
	t      *testing.T
	path   string
	store  *board.Store
	server *Server
	now    time.Time
}

func newDistinctFixture(t *testing.T, own string) *distinctFixture {
	t.Helper()
	f := &distinctFixture{t: t, path: filepath.Join(t.TempDir(), "distinct.db"), now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	var err error
	if f.store, err = board.Open(f.path, board.Config{ServiceID: "swarmmemo.com"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	f.restart(own)
	return f
}

// restart is a new process over the same database: new counters, new salt.
func (f *distinctFixture) restart(own string) {
	networks, err := ParseOwnNetworks(own)
	if err != nil {
		f.t.Fatal(err)
	}
	f.server = New(f.store, nil, Config{ServiceID: "swarmmemo.com", OwnNetworks: networks})
	f.server.readers.interval = time.Hour // flush explicitly
	f.server.readers.now = func() time.Time { return f.now }
}

func (f *distinctFixture) get(path, addr, ua string) {
	f.t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = addr
	r.Header.Set("User-Agent", ua)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code >= 500 {
		f.t.Fatalf("%s: %d", path, w.Code)
	}
}

// today is the daily entry for the fixture's day.
func (f *distinctFixture) day(day string) (map[string]map[string]int64, map[string]map[string]int64) {
	f.t.Helper()
	w := makeRequest(f.server, "GET", "/api/stats/daily?days=3", "", "")
	var body distinctResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		f.t.Fatalf("daily: %d %s", w.Code, w.Body.String())
	}
	for _, d := range body.Daily {
		if d.Day == day {
			return d.Reads, d.Distinct
		}
	}
	f.t.Fatalf("no day %s", day)
	return nil, nil
}

const distinctDay = "2026-10-13"

func TestDistinctReadersCountReadersNotRequests(t *testing.T) {
	f := newDistinctFixture(t, "")
	for i := 0; i < 100; i++ {
		f.get("/llms.txt", "203.0.113.5:1000", "PollingAgent/1.0")
	}
	reads, distinct := f.day(distinctDay)
	if reads["llms_txt"]["other"] != 100 || distinct["llms_txt"]["other"] != 1 || distinct["entry_points"]["other"] != 1 {
		t.Fatalf("100 polls by one reader: reads %v distinct %v", reads["llms_txt"], distinct)
	}
	// Another port and another address in the same /24 with the same
	// User-Agent is the same reader; a second User-Agent there is another.
	f.get("/llms.txt", "203.0.113.9:2000", "PollingAgent/1.0")
	f.get("/llms.txt", "203.0.113.9:2000", "OtherAgent/2.0")
	f.get("/llms.txt", "203.0.113.9:2000", "GPTBot/1.0")
	if _, distinct = f.day(distinctDay); distinct["llms_txt"]["other"] != 2 || distinct["llms_txt"]["crawler"] != 1 {
		t.Fatalf("two User-Agents on one /24: %v", distinct["llms_txt"])
	}
	// IPv6 is grouped by /48.
	f.get("/skill.md", "[2001:db8:1:aaaa::1]:1", "PollingAgent/1.0")
	f.get("/skill.md", "[2001:db8:1:bbbb::2]:1", "PollingAgent/1.0")
	f.get("/skill.md", "[2001:db8:2::1]:1", "PollingAgent/1.0")
	// The union over entry points counts a reader of several once.
	f.server.readers.flush(f.store)
	if _, distinct = f.day(distinctDay); distinct["skill_md"]["other"] != 2 || distinct["entry_points"]["other"] != 4 {
		t.Fatalf("IPv6 /48s and the union: %v", distinct)
	}
}

func TestDistinctReadersNamedAgentAcrossNetworksAndRestarts(t *testing.T) {
	f := newDistinctFixture(t, "")
	agent := strings.Repeat("c", 64)
	f.get("/api/updates?agent="+agent, "203.0.113.5:1", "AgentA/1")
	f.get("/api/updates?agent="+agent, "198.51.100.7:1", "AgentA/2")
	f.get("/api/updates?agent="+agent, "[2001:db8::1]:1", "AgentA/3")
	f.get("/api/updates", "203.0.113.5:1", "AgentB/1")
	reads, distinct := f.day(distinctDay)
	if reads["updates_with_agent"]["other"] != 3 || distinct["updates_with_agent"]["other"] != 1 {
		t.Fatalf("one agent from three networks: %v %v", reads, distinct)
	}
	// FlushReaderCounts at shutdown, then a new process with a new salt:
	// the named agent is still one reader; an unnamed reader is told apart
	// by a new salt and counts once more, as documented.
	f.server.FlushReaderCounts()
	f.restart("")
	f.get("/api/updates?agent="+agent, "192.0.2.1:1", "AgentA/4")
	f.get("/api/updates", "203.0.113.5:1", "AgentB/1")
	if _, distinct = f.day(distinctDay); distinct["updates_with_agent"]["other"] != 1 || distinct["updates_without_agent"]["other"] != 2 {
		t.Fatalf("after a restart (unflushed): %v", distinct)
	}
	f.server.readers.flush(f.store)
	if _, distinct = f.day(distinctDay); distinct["updates_with_agent"]["other"] != 1 || distinct["updates_without_agent"]["other"] != 2 {
		t.Fatalf("after a restart (flushed): %v", distinct)
	}
}

func TestDistinctReadersLeaveOutOwnNetworks(t *testing.T) {
	if _, err := ParseOwnNetworks("10.0.0.0/8, not-a-network"); err == nil {
		t.Fatal("a malformed OWN_NETWORKS entry must be refused")
	}
	f := newDistinctFixture(t, "198.51.100.0/24, 2001:db8:ff::/48, 192.0.2.44")
	f.get("/llms.txt", "198.51.100.7:1", "Operator/1")
	f.get("/llms.txt", "[2001:db8:ff::9]:1", "Operator/1")
	f.get("/llms.txt", "192.0.2.44:1", "Operator/1")
	f.get("/api/updates?agent="+strings.Repeat("d", 64), "198.51.100.8:1", "Operator/1")
	f.get("/llms.txt", "192.0.2.45:1", "Visitor/1")
	f.server.readers.flush(f.store)
	reads, distinct := f.day(distinctDay)
	if reads["llms_txt"]["other"] != 4 || reads["updates_with_agent"]["other"] != 1 {
		t.Fatalf("own networks must still count as reads: %v", reads)
	}
	if distinct["llms_txt"]["other"] != 1 || distinct["updates_with_agent"]["other"] != 0 || distinct["entry_points"]["other"] != 1 {
		t.Fatalf("own networks counted as readers: %v", distinct)
	}
}

func TestDistinctReadersDayRolloverResetsAndForgetsTheSalt(t *testing.T) {
	f := newDistinctFixture(t, "")
	f.now = time.Date(2026, 10, 13, 23, 59, 50, 0, time.UTC)
	f.get("/llms.txt", "203.0.113.5:1", "Reader/1")
	f.get("/llms.txt", "203.0.113.5:1", "Reader/1")
	c := f.server.readers
	firstSalt := append([]byte{}, c.salt.key...)
	idBefore := c.readerID("203.0.113.0", "", "Reader/1", distinctDay, f.now)
	f.now = time.Date(2026, 10, 14, 0, 0, 10, 0, time.UTC)
	f.get("/llms.txt", "203.0.113.5:1", "Reader/1")
	if string(c.salt.key) == string(firstSalt) || string(c.salt.prevKey) != string(firstSalt) {
		t.Fatal("a new day must make a new salt and keep the old one only for the grace minute")
	}
	if idAfter := c.readerID("203.0.113.0", "", "Reader/1", "2026-10-14", f.now); idAfter == idBefore || len(idAfter) != 18 || !strings.HasPrefix(idAfter, "h:") {
		t.Fatalf("reader ids must differ across days: %q %q", idBefore, idAfter)
	}
	// A request stamped before midnight and counted in the grace minute
	// still lands in its own day.
	f.now = time.Date(2026, 10, 14, 0, 0, 20, 0, time.UTC)
	c.mu.Lock()
	if id := c.readerID("203.0.113.0", "", "Reader/1", distinctDay, f.now); id != idBefore {
		t.Error("the previous day's salt must survive the grace minute")
	}
	c.mu.Unlock()
	f.now = time.Date(2026, 10, 14, 0, 1, 1, 0, time.UTC)
	f.get("/llms.txt", "203.0.113.5:1", "Reader/1")
	if c.salt.prevKey != nil || c.salt.prevDay != "" {
		t.Fatal("the previous day's salt outlived the grace minute")
	}
	c.mu.Lock()
	if id := c.readerID("203.0.113.0", "", "Reader/1", distinctDay, f.now); id != "" {
		t.Error("a day whose salt is gone must not be counted")
	}
	c.mu.Unlock()
	f.server.readers.flush(f.store)
	reads, distinct := f.day(distinctDay)
	if reads["llms_txt"]["other"] != 2 || distinct["llms_txt"]["other"] != 1 {
		t.Fatalf("first day: %v %v", reads, distinct)
	}
	reads, distinct = f.day("2026-10-14")
	if reads["llms_txt"]["other"] != 2 || distinct["llms_txt"]["other"] != 1 {
		t.Fatalf("second day: %v %v", reads, distinct)
	}
}

func TestDistinctReadersStoreNoAddressPrefixAgentOrHash(t *testing.T) {
	f := newDistinctFixture(t, "")
	const ua = "UniqueReaderAgent/7.7"
	agent := strings.Repeat("e", 64)
	f.get("/llms.txt", "203.0.113.77:1", ua)
	f.get("/api/updates?agent="+agent, "[2001:db8:77::1]:1", ua)
	c := f.server.readers
	c.mu.Lock()
	hashed := c.readerID("203.0.113.0", "", ua, distinctDay, f.now)
	c.mu.Unlock()
	f.server.FlushReaderCounts()
	raw, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tables, err := raw.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		_ = tables.Scan(&name)
		names = append(names, name)
	}
	tables.Close()
	sketches := 0
	for _, table := range names {
		rows, err := raw.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			_ = rows.Scan(pointers...)
			for _, v := range values {
				text := ""
				switch v := v.(type) {
				case string:
					text = v
				case []byte:
					text = string(v)
				}
				if strings.HasPrefix(text, "reader_hll:") {
					sketches++
				}
				for _, secret := range []string{"203.0.113", "2001:db8", ua, "UniqueReader", agent, hashed, hashed[2:], "a:" + agent} {
					if strings.Contains(text, secret) {
						t.Fatalf("table %s stored %q", table, secret)
					}
				}
			}
		}
		rows.Close()
	}
	if sketches == 0 {
		t.Fatal("no sketch was stored, so the scan proved nothing")
	}
}
