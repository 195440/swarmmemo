package main

// Owned by builder D (RFC0012 §12.1): the trust module's operator commands.

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

const trustUsage = "usage: swarmmemo trust run | trust show AGENT | trust inputs | trust lift EVIDENCE_ID --reason TEXT | trust replay [--days N] [--json|--md] [--snapshot FILE] | trust simulate [--scenario NAME|all] [--seed N] [--seeds K] [--cadence DAYS] [--md|--json] | trust params VERSION"

// trustParams is "swarmmemo trust params VERSION": the compiled-in body of
// trust parameter version 6 (the default) or 7 (RFC 0020 is the rule,
// standing.rule 4), for `swarmmemo params set trust FILE --reason TEXT`.
// It reads no database.
func trustParams(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New(trustUsage)
	}
	var p trust.Params
	switch args[0] {
	case strconv.FormatInt(trust.DefaultVersion, 10):
		p = trust.DefaultParams()
	case strconv.FormatInt(trust.JudgementsVersion, 10):
		p = trust.JudgementsParams()
	default:
		return fmt.Errorf("trust params prints version %d (the default) or %d (RFC 0020)", trust.DefaultVersion, trust.JudgementsVersion)
	}
	if _, err := out.Write(append(p.Body(), '\n')); err != nil {
		return err
	}
	return nil
}

// operatorTrust is "swarmmemo trust run", "trust show AGENT", "trust inputs"
// (the JSONL snapshot a run as of today would read, for verifiers) and
// "trust lift EVIDENCE_ID --reason TEXT" (the only human action on
// evidence: lifting it, with a public reason). Run and show need TRUST set.
// "trust replay" (RFC 0020 §6, C303a) computes rule 3 and RFC 0020 on the
// snapshot a run as of today would read (or on a snapshot file) and reports
// tier moves, the top and bottom marks, the most judged subjects, ring and
// funnel members' marks and conservation. It only reads: run it against a
// copy of the database (DATA_DIR=copy), never the live one.
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
		fmt.Fprintf(out, "trust run %d as_of=%d params_version=%d state=%s nodes=%d edges=%d work=%d output_sha256=%s\n",
			sum.ID, sum.AsOf, sum.ParamsVersion, sum.State, sum.Nodes, sum.Edges, sum.Work, sum.OutputSHA256)
		return nil
	case "show":
		if len(args) != 2 {
			return errors.New(trustUsage)
		}
		res, err := store.Execute(ctx, board.Command{Operation: "trust.get", Target: args[1]}, "operator")
		if err != nil {
			return trustError(err)
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(res.Data)
	case "inputs":
		if len(args) != 1 {
			return errors.New(trustUsage)
		}
		return store.WriteTrustInputs(ctx, out)
	case "replay":
		return trustReplay(ctx, store, args[1:], out)
	case "lift":
		if len(args) != 4 || args[2] != "--reason" {
			return errors.New(trustUsage)
		}
		if err := store.LiftTrustEvidence(ctx, args[1], strings.TrimSpace(args[3])); err != nil {
			return err
		}
		fmt.Fprintf(out, "lifted %s\n", args[1])
		return nil
	}
	return errors.New(trustUsage)
}

// trustReplay is "trust replay [--days N] [--json|--md] [--snapshot FILE]".
func trustReplay(ctx context.Context, store *board.Store, args []string, out io.Writer) error {
	var days int64
	format, file := "md", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			format = "json"
		case "--md":
			format = "md"
		case "--days", "--snapshot":
			if i+1 >= len(args) {
				return errors.New(trustUsage)
			}
			if args[i] == "--snapshot" {
				file = args[i+1]
			} else if n, err := strconv.ParseInt(args[i+1], 10, 64); err != nil || n < 1 || n > 365 {
				return errors.New("--days is 1-365")
			} else {
				days = n
			}
			i++
		default:
			return errors.New(trustUsage)
		}
	}
	var snap trust.Snapshot
	var err error
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		if snap, err = trust.ReadJSONL(f); err != nil {
			return err
		}
	} else if snap, err = store.TrustInputs(ctx, days); err != nil {
		return err
	}
	report, err := trust.Replay(ctx, snap, days)
	if err != nil {
		return err
	}
	if format == "md" {
		_, err = io.WriteString(out, report.Markdown())
		return err
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(report)
}

func trustError(err error) error {
	var be *board.Error
	if errors.As(err, &be) && be.Code == "service_unavailable" {
		return errors.New("the trust module is off: set TRUST=shadow (or allocation) for this command")
	}
	return err
}
