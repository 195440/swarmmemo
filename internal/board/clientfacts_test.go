package board

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sqlIn(values []string) string {
	return "CHECK(state IN ('" + strings.Join(values, "','") + "'))"
}

func jsonTags(value any) []string {
	var out []string
	kind := reflect.TypeOf(value)
	for i := 0; i < kind.NumField(); i++ {
		name, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
		if kind.Field(i).IsExported() && name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// The client facts (clientfacts.go) are the ones the board enforces: the
// state lists match the schema's CHECKs and the effective-state SQL, the work
// mark shapes cover MessageWork exactly, and the envelope is part of Command.
func TestClientFactsMatchEnforcement(t *testing.T) {
	if !strings.Contains(workUSDCSchema, sqlIn(WorkUSDCStates())) {
		t.Errorf("work_usdc.state CHECK differs from WorkUSDCStates %v", WorkUSDCStates())
	}
	if strings.Count(workRewardSchema, sqlIn(WorkCreditStates())) != 2 {
		t.Errorf("work_rewards/work_review_fees state CHECK differs from WorkCreditStates %v", WorkCreditStates())
	}
	stored := WorkStates()[:5]
	if !strings.Contains(workSchema, sqlIn(stored)) {
		t.Errorf("works.state CHECK differs from the stored WorkStates %v", stored)
	}
	for _, derived := range WorkStates()[5:] {
		if !strings.Contains(workEffectiveSQL, "'"+derived+"'") {
			t.Errorf("workEffectiveSQL never yields %s", derived)
		}
	}
	shapes := append(MessageWorkRootRequired(), MessageWorkResultFields()...)
	tags := jsonTags(MessageWork{})
	for _, name := range shapes {
		if !slices.Contains(tags, name) {
			t.Errorf("MessageWork has no field %s", name)
		}
	}
	fields := append(CommandFields(), "signature")
	for _, name := range CommandEnvelopeFields() {
		if !slices.Contains(fields, name) {
			t.Errorf("Command has no envelope field %s", name)
		}
	}
}
