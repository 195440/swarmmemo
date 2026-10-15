package proofs

// Package maintainers: whoever can publish a package can put the
// challenge's statement in its metadata, which the registry serves to
// anyone. npm: the latest version's "swarmmemo" field, description or a
// keyword; PyPI: the latest release's summary, description (its README),
// keywords or a project URL. The checker reads the registry through the
// safe fetcher, and keeps reading it: a statement removed by a later
// release lapses the link, as a TXT record or a gist would. Value inputs:
// the package's age (its first publish) and last month's downloads
// (saturating: downloads are cheap to inflate). Root: npm: or pypi: and the
// package name.

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"swarmmemo/internal/trust"
)

// statementMax bounds the metadata text a statement is looked for in.
const statementMax = 256 << 10

// npmNameRE is an npm package name, scoped or not (lowercase).
var npmNameRE = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]{0,99}/)?[a-z0-9][a-z0-9._-]{0,213}$`)

// pypiNameRE is a PyPI project name before normalization.
var pypiNameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,98}[A-Za-z0-9])?$`)

var pypiSepRE = regexp.MustCompile(`[-_.]+`)

// NormalizeNPM is an npm package name's canonical form.
func NormalizeNPM(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	return name, npmNameRE.MatchString(name) && len(name) <= 214
}

// NormalizePyPI is a project name normalized as PEP 503 says.
func NormalizePyPI(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if !pypiNameRE.MatchString(name) {
		return "", false
	}
	return pypiSepRE.ReplaceAllString(lowerASCII(name), "-"), true
}

// Package is the npm or PyPI provider.
type Package struct {
	spec Spec
	f    fetcher
}

func packageSpec(kind, label, where string) Spec {
	return Spec{Kind: kind, Label: label, Proves: "You can publish a package on " + where + "; its age and downloads count.", Reveals: "the package name.",
		Flow: FlowStatement, Method: kind + "-metadata", Root: "`" + kind + ":` + the package name",
		Priced: "package age (up to 100, one-year ramp) + last month's downloads (up to 100, saturating at 1,000)", Value: "the package name",
		ChallengeTTL: 86400, RecheckSeconds: 7 * 86400,
		Action: `standing.challenge {"schema":1,"kind":"` + kind + `","value":PACKAGE}, publish a release with data.statement in its metadata, then identity.link {"schema":1,"kind":"` + kind + `","value":PACKAGE,"nonce":NONCE}`}
}

func newNPM(f fetcher) *Package {
	return &Package{spec: packageSpec("npm", "Link an npm package", "npm"), f: f}
}

func newPyPI(f fetcher) *Package {
	return &Package{spec: packageSpec("pypi", "Link a PyPI package", "PyPI"), f: f}
}

func (p *Package) Spec() Spec    { return p.spec }
func (p *Package) Enabled() bool { return true }
func (p *Package) Assess(pr Proof, now time.Time) int64 {
	return trust.AccountCents(ageDays(pr.Inputs["created_at"], now), 100, 365, pr.Inputs["downloads"], 100, 1000)
}

// Verify finds the statement in the latest release's metadata and reads the
// package's age and downloads.
func (p *Package) Verify(ctx context.Context, e Evidence) (Proof, error) {
	if e.Statement == "" {
		return Proof{}, ErrFailed
	}
	var name string
	var ok bool
	if p.spec.Kind == "npm" {
		name, ok = NormalizeNPM(e.Value)
	} else {
		name, ok = NormalizePyPI(e.Value)
	}
	if !ok {
		return Proof{}, ErrFailed
	}
	pr := Proof{Kind: p.spec.Kind, Value: name, Root: p.spec.Kind + ":" + name, VerifiedAt: e.Now.Unix()}
	var err error
	if p.spec.Kind == "npm" {
		pr.Inputs, err = p.npm(ctx, name, e.Statement)
	} else {
		pr.Inputs, err = p.pypi(ctx, name, e.Statement)
	}
	if err != nil {
		return Proof{}, err
	}
	return pr, nil
}

// Recheck is Verify again: the statement must still be published.
func (p *Package) Recheck(ctx context.Context, pr Proof, e Evidence) (Proof, error) {
	e.Value = pr.Value
	return p.Verify(ctx, e)
}

// contains reports whether any of texts (each bounded) holds statement.
func contains(statement string, texts ...string) bool {
	for _, t := range texts {
		if len(t) > statementMax {
			t = t[:statementMax]
		}
		if strings.Contains(t, statement) {
			return true
		}
	}
	return false
}

// npmPath is a package name in a registry URL (a scope's slash escaped).
func npmPath(name string) string { return strings.Replace(url.PathEscape(name), "%40", "@", 1) }

func (p *Package) npm(ctx context.Context, name, statement string) (Inputs, error) {
	var latest struct {
		SwarmMemo   json.RawMessage `json:"swarmmemo"`
		Description string          `json:"description"`
		Keywords    json.RawMessage `json:"keywords"`
	}
	if err := p.f.do(ctx, request{url: "https://registry.npmjs.org/" + npmPath(name) + "/latest", into: &latest}); err != nil {
		return nil, err
	}
	var field string
	_ = json.Unmarshal(latest.SwarmMemo, &field)
	var keywords []string
	_ = json.Unmarshal(latest.Keywords, &keywords)
	if !contains(statement, append([]string{field, latest.Description}, keywords...)...) {
		return nil, ErrFailed
	}
	// The full document's time.created is the first publish (the document
	// of a large package runs to megabytes: bounded, never kept).
	var doc struct {
		Time map[string]string `json:"time"`
	}
	created := int64(0)
	if p.f.do(ctx, request{url: "https://registry.npmjs.org/" + npmPath(name), limit: bigFetchBodyMax, into: &doc}) == nil {
		created = isoTime(doc.Time["created"])
	}
	var dl struct {
		Downloads int64 `json:"downloads"`
	}
	_ = p.f.do(ctx, request{url: "https://api.npmjs.org/downloads/point/last-month/" + npmPath(name), into: &dl})
	return Inputs{"created_at": created, "downloads": max(0, dl.Downloads)}, nil
}

func (p *Package) pypi(ctx context.Context, name, statement string) (Inputs, error) {
	var doc struct {
		Info struct {
			Summary     string            `json:"summary"`
			Description string            `json:"description"`
			Keywords    string            `json:"keywords"`
			ProjectURLs map[string]string `json:"project_urls"`
		} `json:"info"`
		Releases map[string][]struct {
			UploadTime string `json:"upload_time_iso_8601"`
		} `json:"releases"`
	}
	if err := p.f.do(ctx, request{url: "https://pypi.org/pypi/" + url.PathEscape(name) + "/json", limit: bigFetchBodyMax, into: &doc}); err != nil {
		return nil, err
	}
	texts := []string{doc.Info.Summary, doc.Info.Description, doc.Info.Keywords}
	for k, v := range doc.Info.ProjectURLs {
		texts = append(texts, k, v)
	}
	if !contains(statement, texts...) {
		return nil, ErrFailed
	}
	created := int64(0)
	for _, files := range doc.Releases {
		for _, f := range files {
			if t := isoTime(f.UploadTime); t > 0 && (created == 0 || t < created) {
				created = t
			}
		}
	}
	var stats struct {
		Data struct {
			LastMonth int64 `json:"last_month"`
		} `json:"data"`
	}
	_ = p.f.do(ctx, request{url: "https://pypistats.org/api/packages/" + url.PathEscape(name) + "/recent", into: &stats})
	return Inputs{"created_at": created, "downloads": max(0, stats.Data.LastMonth)}, nil
}
