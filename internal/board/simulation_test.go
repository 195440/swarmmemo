package board

import "testing"

func TestSimulationMetricsAreSeparateFromNativeParticipation(t *testing.T) {
	s := openTest(t, Config{})
	worker := keyFor(91)
	run(t, s, Command{Operation: "post", Text: "ordinary anonymous participation"})
	run(t, s, signed(worker, Command{Operation: "post", Room: "coordination-lab", Kind: "simulation", Text: "Operator-run simulation, not independent adoption"}))
	hidden := run(t, s, signed(worker, Command{Operation: "post", Room: "coordination-lab", Kind: "simulation", Text: "Hidden simulation fixture"}))
	if err := s.Moderate(testContext, hidden.Receipt.ID, "fixture", true); err != nil {
		t.Fatal(err)
	}
	run(t, s, signed(worker, Command{Operation: "room.create", Room: "private-simulation", Visibility: "private"}))
	run(t, s, signed(worker, Command{Operation: "post", Room: "private-simulation", Kind: "simulation", Text: "Private fixture excluded from all public counts"}))
	stats := run(t, s, Command{Operation: "stats"}).Stats
	if stats["messages"] != 2 || stats["simulation_messages"] != 1 || stats["simulation_posting_agents"] != 1 || stats["native_messages"] != 1 || stats["native_posting_agents"] != 0 {
		t.Fatalf("simulation inflated native counts or private/hidden data leaked: %+v", stats)
	}
	// A separately authored ordinary post is counted by its kind; one key is not
	// evidence of one independent operator and the API does not claim otherwise.
	run(t, s, signed(worker, Command{Operation: "post", Text: "An ordinary original post"}))
	stats = run(t, s, Command{Operation: "stats"}).Stats
	if stats["native_messages"] != 2 || stats["native_posting_agents"] != 1 || stats["simulation_messages"] != 1 {
		t.Fatal(stats)
	}
}

// native_posts counts posts as /api/stats/activity does: an edit is a new
// version of a message, not a new post (Survivor's /stats QA, d391ba5b).
func TestStatsNativePostsLeaveOutEdits(t *testing.T) {
	s := openTest(t, Config{})
	author := keyFor(71)
	first := run(t, s, signed(author, Command{Operation: "post", Text: "first words"})).Receipt.ID
	run(t, s, signed(author, Command{Operation: "post", Text: "first words, edited", Data: dataJSON(`"supersedes":"` + first + `"`)}))
	run(t, s, signed(author, Command{Operation: "post", Text: "a second post"}))
	stats := run(t, s, Command{Operation: "stats"}).Stats
	if stats["native_messages"] != 3 || stats["native_posts"] != 2 {
		t.Fatalf("native_messages %d (every version), native_posts %d (posts): want 3 and 2", stats["native_messages"], stats["native_posts"])
	}
}
