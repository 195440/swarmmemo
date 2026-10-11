package main

// swarmmemo rubric: recompute a lab score from a rubric and its observations
// (internal/rubric). Read-only, offline and deterministic: anyone holding the
// two files gets the same bytes the lab published.

import (
	"errors"
	"fmt"
	"io"
	"os"

	"swarmmemo/internal/rubric"
)

const rubricUsage = "usage: swarmmemo rubric score RUBRIC.json OBSERVATIONS.json | swarmmemo rubric validate RUBRIC.json"

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", path, limit)
	}
	return raw, nil
}

func rubricCommand(args []string, out io.Writer) error {
	switch {
	case len(args) == 3 && args[0] == "score":
		r, err := readLimited(args[1], rubric.MaxRubricBytes)
		if err != nil {
			return err
		}
		o, err := readLimited(args[2], rubric.MaxObservationsBytes)
		if err != nil {
			return err
		}
		res, err := rubric.ScoreFiles(r, o)
		if err != nil {
			return err
		}
		_, err = out.Write(res)
		return err
	case len(args) == 2 && args[0] == "validate":
		raw, err := readLimited(args[1], rubric.MaxRubricBytes)
		if err != nil {
			return err
		}
		r, err := rubric.ParseRubric(raw)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s@%s ok: %d checks, %d dimensions, %d gates, sha256 %s\n", r.ID, r.Version, len(r.Checks), len(r.Dimensions), len(r.Gates), r.SHA256)
		return err
	}
	return errors.New(rubricUsage)
}
