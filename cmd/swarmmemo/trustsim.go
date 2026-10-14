package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"swarmmemo/internal/judge/sim"
)

const trustSimulateUsage = "usage: swarmmemo trust simulate [--scenario NAME|all] [--seed N] [--seeds K] [--cadence DAYS] [--md|--json]"

// trustSimulate is "swarmmemo trust simulate" (C303s): the agent-based
// simulation of RFC 0020 (internal/judge/sim), which drives the real trust
// run on a synthetic snapshot. It reads no database: seeds N..N+K−1 of
// one scenario, or all of them, as a page (--md, the default) or JSON.
func trustSimulate(args []string, out io.Writer) error {
	scenario, format := "all", "md"
	var seed uint64 = 1
	seeds, cadence := 5, int64(7)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--md":
			format = "md"
		case "--json":
			format = "json"
		case "--scenario", "--seed", "--seeds", "--cadence":
			if i+1 >= len(args) {
				return errors.New(trustSimulateUsage)
			}
			v := args[i+1]
			switch args[i] {
			case "--scenario":
				scenario = v
			case "--seed":
				n, err := strconv.ParseUint(v, 10, 64)
				if err != nil {
					return errors.New("--seed is a non-negative integer")
				}
				seed = n
			case "--seeds":
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > 100 {
					return errors.New("--seeds is 1-100")
				}
				seeds = n
			case "--cadence":
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 1 || n > 30 {
					return errors.New("--cadence is 1-30 days")
				}
				cadence = n
			}
			i++
		default:
			return errors.New(trustSimulateUsage)
		}
	}
	var list []sim.Scenario
	if scenario == "all" {
		list = sim.Scenarios()
	} else if sc, ok := sim.ScenarioByName(scenario); ok {
		list = []sim.Scenario{sc}
	} else {
		var names []string
		for _, s := range sim.Scenarios() {
			names = append(names, s.Name)
		}
		return fmt.Errorf("unknown scenario %q (one of %s, or all)", scenario, strings.Join(names, ", "))
	}
	var reports []sim.Report
	for _, sc := range list {
		r, err := sim.Simulate(context.Background(), sc, seed, seeds, sim.Config{Cadence: cadence})
		if err != nil {
			return err
		}
		reports = append(reports, r)
	}
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(reports)
	}
	fmt.Fprintf(out, "# RFC 0020 simulation (C303s)\n\nThe real trust run (internal/trust, internal/judge) on synthetic worlds: runs every %d days from day 6 to 181, window 90, judging on days 1-89. Cents per agent unless stated.\n\n", cadence)
	for _, r := range reports {
		io.WriteString(out, r.Markdown()+"\n")
	}
	return nil
}
