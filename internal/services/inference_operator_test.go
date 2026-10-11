package services_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"swarmmemo/internal/services"
)

// An operator completion runs the alias's route like a call: failover,
// the key redacted, every step counted against the upstream's daily cap;
// no inference service, or no such alias, is ErrOperatorModel.
func TestOperatorComplete(t *testing.T) {
	v := newInfEnv(t, [3]int64{1 << 20, 1 << 20, 1 << 20})
	reg := v.e.Registry()
	ctx := context.Background()
	out, err := reg.OperatorComplete(ctx, "small", "You answer.", "hello", 64)
	if err != nil || out != "hello from primary" {
		t.Fatalf("%q %v", out, err)
	}
	var units int64
	if err = v.db.QueryRow("SELECT coalesce(sum(units),0) FROM inference_spend WHERE upstream='primary'").Scan(&units); err != nil || units <= 0 {
		t.Fatalf("spend not counted: %d %v", units, err)
	}
	if body := v.primary.bodies[0]; body["temperature"] != 0.0 || len(body["messages"].([]any)) != 2 {
		t.Fatalf("request %v", body)
	}
	v.primary.setMode("500")
	if out, err = reg.OperatorComplete(ctx, "small", "s", "u", 64); err != nil || out != "hello from backup" {
		t.Fatalf("failover: %q %v", out, err)
	}
	v.primary.setMode("echo_key")
	if out, _ = reg.OperatorComplete(ctx, "small", "s", "u", 64); strings.Contains(out, testKey) {
		t.Fatal("the key leaked")
	}
	if _, err = reg.OperatorComplete(ctx, "nope", "s", "u", 64); !errors.Is(err, services.ErrOperatorModel) {
		t.Fatalf("unknown alias: %v", err)
	}
	if _, err = reg.OperatorComplete(ctx, "small", strings.Repeat("x", services.OperatorPromptBytes), "u", 64); err == nil {
		t.Fatal("prompt bound")
	}
	none := services.NewBuiltinRegistry([]string{"notary"}, services.Deps{DB: v.db})
	if _, err = none.OperatorComplete(ctx, "small", "s", "u", 64); !errors.Is(err, services.ErrOperatorModel) {
		t.Fatalf("no inference: %v", err)
	}
	// A spent cap refuses the step: the operator's use never passes it.
	capped := newInfEnv(t, [3]int64{1, 1, 1})
	if _, err = capped.e.Registry().OperatorComplete(ctx, "small", "s", "u", 64); !errors.Is(err, services.ErrOperatorModel) || capped.primary.count() != 0 {
		t.Fatalf("cap: %v, %d requests", err, capped.primary.count())
	}
}
