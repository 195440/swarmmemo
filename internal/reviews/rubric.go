package reviews

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strconv"
	"strings"

	"swarmmemo/internal/rubric"
)

// Rubric is one served rubric file (RFC 0019 §5): the format is
// internal/rubric's, parsed and scored there, never here. This adds what
// the board serves: the exact bytes, and the documentation fields the
// method page shows.
type Rubric struct {
	*rubric.Rubric
	// Raw is the file exactly as served; Rubric.SHA256 is its SHA-256.
	Raw []byte
	Doc RubricDoc
}

// RubricDoc is a rubric's documentation, shown on its page and never
// scored (internal/rubric ignores these fields).
type RubricDoc struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Notes       string            `json:"notes"`
	Changelog   []string          `json:"changelog"`
	Checks      []CheckDoc        `json:"checks"`
	Value       map[string]any    `json:"value"`
	byID        map[string]CheckDoc
}

// CheckDoc is one check's documentation.
type CheckDoc struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Measure  string `json:"measure"`
	Validity string `json:"validity"`
}

// CheckDoc is the documentation of check id.
func (r *Rubric) CheckDoc(id string) CheckDoc { return r.Doc.byID[id] }

// Check is the rubric's check id, or nil.
func (r *Rubric) Check(id string) *rubric.Check {
	for i := range r.Checks {
		if r.Checks[i].ID == id {
			return &r.Checks[i]
		}
	}
	return nil
}

// HasFlag reports whether one of the rubric's gates raises flag.
func (r *Rubric) HasFlag(flag string) bool {
	return slices.ContainsFunc(r.Gates, func(g rubric.Gate) bool { return g.Flag == flag })
}

// Path is where the board serves this version's bytes.
func (r *Rubric) Path() string { return "/rubrics/" + r.ID + "/" + r.Version + ".json" }

//go:embed rubrics/*.json
var rubricFiles embed.FS

// rubrics are every embedded rubric file, parsed once by internal/rubric: a
// bad file stops the binary at start, never a review at post time. A file
// is named ID-VERSION.json.
var rubrics = func() []*Rubric {
	entries, err := fs.Glob(rubricFiles, "rubrics/*.json")
	if err != nil {
		panic(err)
	}
	var out []*Rubric
	for _, name := range entries {
		raw, err := rubricFiles.ReadFile(name)
		if err != nil {
			panic(err)
		}
		r, err := LoadRubric(raw)
		if err != nil {
			panic(fmt.Sprintf("%s: %v", name, err))
		}
		if want := "rubrics/" + r.ID + "-" + r.Version + ".json"; want != name {
			panic(fmt.Sprintf("rubric %s must be named %s", name, want))
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return versionLess(out[i].Version, out[j].Version)
	})
	return out
}()

// LoadRubric parses a rubric file with internal/rubric and reads its
// documentation fields; the board serves only rubrics whose dimensions are
// the four universal ones and whose subject types it knows.
func LoadRubric(raw []byte) (*Rubric, error) {
	p, err := rubric.ParseRubric(raw)
	if err != nil {
		return nil, err
	}
	for _, d := range p.Dimensions {
		if !slices.Contains(Dimensions, d.ID) {
			return nil, fmt.Errorf("dimension %q is not one of %s", d.ID, strings.Join(Dimensions, ", "))
		}
	}
	for _, t := range p.SubjectTypes {
		if !slices.Contains(Types, t) {
			return nil, fmt.Errorf("unknown subject type %q", t)
		}
	}
	r := &Rubric{Rubric: p, Raw: raw}
	var doc struct {
		Title       json.RawMessage `json:"title"`
		Description json.RawMessage `json:"description"`
		Notes       json.RawMessage `json:"notes"`
		Changelog   json.RawMessage `json:"changelog"`
		Value       json.RawMessage `json:"value"`
		Checks      []CheckDoc      `json:"checks"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	// Documentation is text or absent; anything else is left out of the page.
	_ = json.Unmarshal(doc.Title, &r.Doc.Title)
	_ = json.Unmarshal(doc.Description, &r.Doc.Description)
	_ = json.Unmarshal(doc.Notes, &r.Doc.Notes)
	_ = json.Unmarshal(doc.Changelog, &r.Doc.Changelog)
	_ = json.Unmarshal(doc.Value, &r.Doc.Value)
	if r.Doc.Title == "" {
		r.Doc.Title = r.ID
	}
	r.Doc.Checks = doc.Checks
	r.Doc.byID = map[string]CheckDoc{}
	for _, c := range doc.Checks {
		r.Doc.byID[c.ID] = c
	}
	return r, nil
}

// Rubrics lists every rubric version the board serves, by id then version.
func Rubrics() []*Rubric { return slices.Clone(rubrics) }

// LookupRubric is one rubric version; version "" is the newest. nil when unknown.
func LookupRubric(id, version string) *Rubric {
	var found *Rubric
	for _, r := range rubrics {
		if r.ID == id && (version == "" || r.Version == version) {
			found = r
		}
	}
	return found
}

// versionLess orders semver strings: numbers, then a release above its
// pre-releases.
func versionLess(a, b string) bool {
	core := func(v string) ([3]int, string) {
		c, pre, _ := strings.Cut(v, "-")
		var n [3]int
		for i, p := range strings.SplitN(c, ".", 3) {
			n[i], _ = strconv.Atoi(p)
		}
		return n, pre
	}
	na, pa := core(a)
	nb, pb := core(b)
	if na != nb {
		for i := range na {
			if na[i] != nb[i] {
				return na[i] < nb[i]
			}
		}
	}
	switch {
	case pa == pb:
		return false
	case pa == "":
		return false
	case pb == "":
		return true
	}
	return pa < pb
}
