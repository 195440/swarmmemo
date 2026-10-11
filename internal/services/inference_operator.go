package services

// Operator completions: a short completion SwarmMemo itself asks for (Find
// reads a query's intent with one), on the inference service's routes and
// upstreams. No caller is charged and nothing is recorded as a call; every
// step is still reserved against the upstream's daily spend cap, so the
// operator's own use can never pass it, and the upstream's suspension and
// key rules apply as for any call. Without the inference service, or
// without the alias, it answers ErrOperatorModel.

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrOperatorModel is OperatorComplete's answer when no upstream can run it.
var ErrOperatorModel = errors.New("services: no operator model")

// Operator completion bounds.
const (
	OperatorPromptBytes = 8 << 10
	OperatorMaxTokens   = 512
)

// OperatorComplete runs one completion of alias (system and user
// messages, temperature 0, at most maxTokens) and returns its text.
func (r *Registry) OperatorComplete(ctx context.Context, alias, system, user string, maxTokens int64) (string, error) {
	p, err := r.Lookup("inference")
	if err != nil {
		return "", ErrOperatorModel
	}
	in, ok := p.(*inference)
	if !ok || in.cfg == nil || in.db == nil {
		return "", ErrOperatorModel
	}
	route, ok := in.cfg.Models[alias]
	if !ok || len(route) == 0 {
		return "", ErrOperatorModel
	}
	if len(system)+len(user) > OperatorPromptBytes || maxTokens < 1 || maxTokens > OperatorMaxTokens {
		return "", errors.New("services: operator completion out of bounds")
	}
	zero := 0.0
	messages := []inferenceMessage{{Role: "system", Content: system}, {Role: "user", Content: user}}
	plan := inferencePlan{alias: alias, route: route, messages: messages, prompt: canonicalJSON(messages), promptBytes: len(system) + len(user), maxTokens: maxTokens, temperature: &zero}
	plan.maxIn = int64(plan.promptBytes) + inferenceMessageTokens*int64(len(messages)) + inferenceRequestTokens
	day := time.Now().Unix() / 86400
	for _, step := range plan.route {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		up := in.cfg.byName[step.Upstream]
		if up == nil || in.isSuspended(up.Name, time.Now()) {
			continue
		}
		key, err := readKey(up.KeyFile)
		if err != nil {
			continue
		}
		model, maxOut, reserve := in.stepMax(plan, step)
		if ok, err := in.reserveSpend(ctx, up, day, reserve); err != nil || !ok {
			continue
		}
		reply, outcome, billed := in.send(ctx, up, key, step.Model, plan, maxOut)
		if outcome != "" {
			if !billed {
				in.releaseSpend(up, day, reserve)
			}
			if outcome == "stop" {
				return "", errors.New("services: operator completion refused upstream")
			}
			continue
		}
		used := reserve
		if reply.Reported {
			used = model.Price.For(min(reply.InputTokens, plan.maxIn), min(reply.OutputTokens, maxOut))
		}
		in.releaseSpend(up, day, reserve-used)
		return strings.ReplaceAll(reply.Output, key, "[redacted]"), nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", ErrOperatorModel
}
