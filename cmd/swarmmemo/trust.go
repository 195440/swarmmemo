package main

// The trust module's operator commands (RFC 0021): run, show, inputs,
// params, and the penalty log's two operator actions, penalty and lift.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"swarmmemo/internal/board"
	"swarmmemo/internal/trust"
)

const trustUsage = "usage: swarmmemo trust run | trust show AGENT | trust inputs | trust params | trust compare RUN_OLD RUN_NEW [--json] | " +
	"trust penalty moderation|override AGENT... --reason TEXT [--evidence TEXT] [--author NAME] | trust lift ENTRY_ID --reason TEXT [--author NAME] | trust penalties [AGENT]"

// trustParams is "swarmmemo trust params": the compiled-in body of the
// default trust parameter version, for `swarmmemo params set trust FILE
// --reason TEXT`. It reads no database.
func trustParams(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New(trustUsage)
	}
	_, err := out.Write(append(trust.DefaultParams().Body(), '\n'))
	return err
}

// operatorTrust is "swarmmemo trust run", "trust show AGENT", "trust
// inputs" (the JSONL snapshot a run as of today would read, for
// verifiers), "trust penalty" (an operator's penalty, in the public log:
// moderation, only in the moderation standard's categories, or override;
// the next run applies it), "trust lift" (an override reversing an entry,
// with its public reason), "trust penalties" (the log) and "trust compare
// RUN_OLD RUN_NEW" (per account: band, standing, vote weight and daily
// allowance share, old against new; the cutover check, C320c). Each needs
// TRUST on.
func operatorTrust(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(trustUsage)
	}
	switch args[0] {
	case "run":
		if len(args) != 1 {
			return errors.New(trustUsage)
		}
		sum, err := store.RunTrust(ctx)
		if err != nil {
			return trustError(err)
		}
		fmt.Fprintf(out, "trust run %d as_of=%d params_version=%d state=%s nodes=%d edges=%d work=%d output_sha256=%s penalties=%d\n",
			sum.ID, sum.AsOf, sum.ParamsVersion, sum.State, sum.Nodes, sum.Edges, sum.Work, sum.OutputSHA256, len(sum.Penalties))
		return nil
	case "show":
		if len(args) != 2 {
			return errors.New(trustUsage)
		}
		res, err := store.Execute(ctx, board.Command{Operation: "trust.get", Target: args[1]}, "operator")
		if err != nil {
			return trustError(err)
		}
		return writeJSON(out, res.Data)
	case "inputs":
		if len(args) != 1 {
			return errors.New(trustUsage)
		}
		return store.WriteTrustInputs(ctx, out)
	case "penalty":
		if len(args) < 2 || args[1] != trust.TriggerModeration && args[1] != trust.TriggerOverride {
			return errors.New(trustUsage)
		}
		agents, flags, err := trustFlags(args[2:], "--reason", "--evidence", "--author")
		if err != nil || len(agents) == 0 || flags["--reason"] == "" {
			return errors.New(trustUsage)
		}
		id, err := store.PenalizeTrust(ctx, args[1], agents, flags["--evidence"], flags["--reason"], operatorAuthor(flags))
		if err != nil {
			return trustError(err)
		}
		fmt.Fprintf(out, "penalty %s recorded (pending: the next trust run applies it; swarmmemo trust run applies it now)\n", id)
		return nil
	case "lift":
		ids, flags, err := trustFlags(args[1:], "--reason", "--author")
		if err != nil || len(ids) != 1 || flags["--reason"] == "" {
			return errors.New(trustUsage)
		}
		lift, err := store.LiftTrustPenalty(ctx, ids[0], flags["--reason"], operatorAuthor(flags))
		if err != nil {
			return trustError(err)
		}
		fmt.Fprintf(out, "lift %s recorded for %s (the next trust run reverses it)\n", lift, ids[0])
		return nil
	case "compare":
		if len(args) != 3 && !(len(args) == 4 && args[3] == "--json") {
			return errors.New(trustUsage)
		}
		oldRun, err1 := strconv.ParseInt(args[1], 10, 64)
		newRun, err2 := strconv.ParseInt(args[2], 10, 64)
		if err1 != nil || err2 != nil {
			return errors.New(trustUsage)
		}
		c, err := store.TrustCompare(ctx, oldRun, newRun)
		if err != nil {
			return trustError(err)
		}
		if len(args) == 4 {
			return writeJSON(out, c)
		}
		return board.WriteTrustComparison(out, c)
	case "penalties":
		if len(args) > 2 {
			return errors.New(trustUsage)
		}
		target := ""
		if len(args) == 2 {
			target = args[1]
		}
		res, err := store.Execute(ctx, board.Command{Operation: "trust.penalties", Target: target, Limit: 100}, "operator")
		if err != nil {
			return trustError(err)
		}
		return writeJSON(out, res.Data)
	}
	return errors.New(trustUsage)
}

// trustFlags splits args into positional words and the named flags' values.
func trustFlags(args []string, names ...string) ([]string, map[string]string, error) {
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	flags := map[string]string{}
	var words []string
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			words = append(words, args[i])
			continue
		}
		if !known[args[i]] || i+1 >= len(args) {
			return nil, nil, fmt.Errorf("unknown flag or missing value: %s", args[i])
		}
		flags[args[i]] = strings.TrimSpace(args[i+1])
		i++
	}
	return words, flags, nil
}

// operatorAuthor is the entry's author: --author, else $USER, else operator.
func operatorAuthor(flags map[string]string) string {
	if a := flags["--author"]; a != "" {
		return a
	}
	if u := os.Getenv("USER"); u != "" {
		return "operator:" + u
	}
	return "operator"
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func trustError(err error) error {
	var be *board.Error
	if errors.As(err, &be) && be.Code == "service_unavailable" {
		return errors.New("the trust module is off: set TRUST=on for this command")
	}
	return err
}
