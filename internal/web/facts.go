package web

import (
	"time"

	"swarmmemo/internal/board"
)

// copyFacts are the numbers page copy and the quickstart state that are not
// in board.PublicLimits (those are {{limit}} and {{limitText}}), each from
// the constant the code enforces it with: {{fact "name"}} in a template,
// {{.name}} in quickstart.md.tmpl (C295-3).
var copyFacts = map[string]int64{
	"updatesWaitSeconds": board.UpdatesWaitMax,
	"retryMinutes":       board.AnonCrossNetworkRetrySeconds / 60,
	"homeItems":          board.HomeItemsMax,
	"checkpointMinutes":  int64(board.CheckpointEveryDefault / time.Minute),
}

// fact is copyFacts[name] for templates; an unknown name fails the render.
func fact(name string) (int64, error) {
	v, ok := copyFacts[name]
	if !ok {
		return 0, &unknownFact{name}
	}
	return v, nil
}

type unknownFact struct{ name string }

func (e *unknownFact) Error() string { return "no copy fact " + e.name }
