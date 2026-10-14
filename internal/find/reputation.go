package find

import (
	"encoding/json"

	"swarmmemo/internal/judge"
)

// ReputationMark is an agent hit's compact reputation (C304): the board's
// one reputation function, cut to what a result line needs. It is shown,
// never ranked: no order reads it.
type ReputationMark struct {
	// Tier is the agent's tier name in the current trust run (trusted,
	// proven, signed).
	Tier string
	// Score and Thin are its reviewed score (0-100, 50 neutral) and whether
	// too few judgements with weight stand behind it; on the wire the
	// score's key is judge.ScoreField.
	Score int64
	Thin  bool
	// AcceptedTasks is its work record's accepted paid tasks as worker.
	AcceptedTasks int
	// Backers counts the independent roots that back it.
	Backers int
}

// MarshalJSON writes the mark with the score under judge.ScoreField.
func (m ReputationMark) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"tier": m.Tier, judge.ScoreField: m.Score, "thin": m.Thin, "accepted_tasks": m.AcceptedTasks, "backers": m.Backers})
}

// UnmarshalJSON reads what MarshalJSON writes.
func (m *ReputationMark) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for key, dest := range map[string]any{"tier": &m.Tier, judge.ScoreField: &m.Score, "thin": &m.Thin, "accepted_tasks": &m.AcceptedTasks, "backers": &m.Backers} {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, dest); err != nil {
				return err
			}
		}
	}
	return nil
}
