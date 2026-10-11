package rubric

import (
	"bytes"
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden results")

func load(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func score(t testing.TB, obs string) *Result {
	t.Helper()
	r, err := ParseRubric(load(t, "x402-endpoint-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := ParseObservations(load(t, obs), r)
	if err != nil {
		t.Fatal(err)
	}
	return Score(r, o)
}

// Same input, byte-identical output, pinned in testdata/golden.
func TestGolden(t *testing.T) {
	for _, name := range []string{"healthy", "free-only", "gated", "down"} {
		t.Run(name, func(t *testing.T) {
			got, err := ScoreFiles(load(t, "x402-endpoint-1.json"), load(t, "obs-"+name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			again, _ := ScoreFiles(load(t, "x402-endpoint-1.json"), load(t, "obs-"+name+".json"))
			if !bytes.Equal(got, again) {
				t.Fatal("two runs differ")
			}
			path := filepath.Join("testdata", "golden", name+".json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("result differs from %s (run with -update after a deliberate change)\n got: %s\nwant: %s", path, got, want)
			}
		})
	}
}

func TestVerdicts(t *testing.T) {
	cases := map[string]struct {
		verdict string
		flags   []string
		maxHi   int
	}{
		"healthy":   {"proceed", nil, 100},
		"free-only": {"unverified", nil, 100},
		"gated":     {"avoid", []string{"injection", "overcharge"}, 39},
		"down":      {"down", []string{"down"}, 100},
	}
	for name, want := range cases {
		res := score(t, "obs-"+name+".json")
		if res.Verdict != want.verdict {
			t.Errorf("%s: verdict %s, want %s (score %d %v, grade %s, conf %s, reasons %v)", name, res.Verdict, want.verdict, res.Score, res.Band, res.Grade, res.Confidence, res.Reasons)
		}
		if strings.Join(res.Flags, ",") != strings.Join(want.flags, ",") {
			t.Errorf("%s: flags %v, want %v", name, res.Flags, want.flags)
		}
		if res.Band[1] > want.maxHi || res.Score > want.maxHi {
			t.Errorf("%s: score %d band %v above cap %d", name, res.Score, res.Band, want.maxHi)
		}
		if res.Band[0] > res.Score || res.Score > res.Band[1] {
			t.Errorf("%s: score %d outside band %v", name, res.Score, res.Band)
		}
	}
	if res := score(t, "obs-gated.json"); !res.Watch || res.Uncapped == nil {
		t.Errorf("gated: watch %v uncapped %v", res.Watch, res.Uncapped)
	}
	if res := score(t, "obs-down.json"); !res.Stale {
		t.Error("down: not stale")
	}
	healthy, free := score(t, "obs-healthy.json"), score(t, "obs-free-only.json")
	if hw, fw := healthy.Band[1]-healthy.Band[0], free.Band[1]-free.Band[0]; hw >= fw {
		t.Errorf("more evidence should narrow the band: healthy %d, free-only %d", hw, fw)
	}
	for _, d := range healthy.Dimensions {
		if d.ID == "good" && !d.NA {
			t.Error("good is all na in healthy and must drop out")
		}
	}
}

// The seed comes from the canonical observations: reordering events,
// reformatting or using another time zone changes nothing.
func TestCanonicalOrderAndFormat(t *testing.T) {
	var o map[string]any
	if err := json.Unmarshal(load(t, "obs-healthy.json"), &o); err != nil {
		t.Fatal(err)
	}
	r1 := o["checks"].(map[string]any)["R1"].(map[string]any)
	ev := r1["events"].([]any)
	for i, j := 0, len(ev)-1; i < j; i, j = i+1, j-1 {
		ev[i], ev[j] = ev[j], ev[i]
	}
	ev[len(ev)-1].(map[string]any)["at"] = "2026-10-11T14:00:00+02:00" // = 12:00Z, the newest
	compact, _ := json.Marshal(o)
	a, err := ScoreFiles(load(t, "x402-endpoint-1.json"), load(t, "obs-healthy.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ScoreFiles(load(t, "x402-endpoint-1.json"), compact)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("reordered input scored differently:\n%s\n%s", a, b)
	}
	// One more probe changes the seed and the hash.
	ev = append(ev, map[string]any{"at": "2026-10-11T11:00:00Z", "pass": 1, "fail": 0})
	r1["events"] = ev
	more, _ := json.Marshal(o)
	c, err := ScoreFiles(load(t, "x402-endpoint-1.json"), more)
	if err != nil {
		t.Fatal(err)
	}
	var ra, rc Result
	_ = json.Unmarshal(a, &ra)
	_ = json.Unmarshal(c, &rc)
	if ra.Seed == rc.Seed || ra.ObservationsSHA256 == rc.ObservationsSHA256 {
		t.Fatal("new evidence kept the seed")
	}
}

func TestCurve(t *testing.T) {
	cv := &Curve{Input: "p95", Points: [][2]float64{{300, 100}, {1000, 90}, {3000, 50}, {10000, 0}}}
	for _, c := range []struct{ x, want float64 }{
		{0, 100}, {100, 100}, {300, 100}, {1000, 90}, {3000, 50}, {10000, 0}, {20000, 0},
		{math.Sqrt(1000 * 3000), 70}, // the geometric midpoint is the score midpoint
	} {
		if got := cv.eval(c.x); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("eval(%v) = %v, want %v", c.x, got, c.want)
		}
	}
}

func TestDecayAndRevision(t *testing.T) {
	r, err := ParseRubric(load(t, "x402-endpoint-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	obs := `{"schema":"swarmmemo.observations/1","subject":{"type":"x402","key":"GET https://a.example/x","revision_changed_at":"2026-10-10T00:00:00Z"},
		"as_of":"2026-10-25T00:00:00Z","checks":{
		"R1":{"events":[{"at":"2026-10-11T00:00:00Z","pass":1,"fail":0},{"at":"2026-10-25T00:00:00Z","pass":0,"fail":1,"weight":0.5}]},
		"C1":{"events":[{"at":"2026-10-01T00:00:00Z","pass":1,"fail":0}]},
		"R2":{"events":[{"at":"2026-10-01T00:00:00Z","pass":1,"fail":0}]}}}`
	o, err := ParseObservations([]byte(obs), r)
	if err != nil {
		t.Fatal(err)
	}
	st := prepare(r, o, 0)
	get := func(id string) checkState {
		for _, s := range st {
			if s.c.ID == id {
				return s
			}
		}
		t.Fatal(id)
		return checkState{}
	}
	if s := get("R1"); math.Abs(s.s-0.5) > 1e-12 || math.Abs(s.f-0.5) > 1e-12 {
		t.Errorf("R1: 14 days at h=14 is half, and an outside weight 0.5: s=%v f=%v", s.s, s.f)
	}
	if s := get("C1"); s.s != 0 || s.measured() {
		t.Errorf("C1 resets on a revision change: s=%v", s.s)
	}
	if s := get("R2"); math.Abs(s.s-0.5*math.Pow(2, -24.0/14)) > 1e-12 {
		t.Errorf("R2 keeps half its weight across a revision change: s=%v", s.s)
	}
}

func TestBetaSampler(t *testing.T) {
	g := newRNG([32]byte{1, 2, 3})
	for _, ab := range [][2]float64{{0.5, 0.5}, {1.5, 0.5}, {20.5, 0.5}, {3, 7}} {
		sum := 0.0
		n := 40000
		for range n {
			x := g.beta(ab[0], ab[1])
			if x < 0 || x > 1 || math.IsNaN(x) {
				t.Fatalf("beta out of range: %v", x)
			}
			sum += x
		}
		if mean, want := sum/float64(n), ab[0]/(ab[0]+ab[1]); math.Abs(mean-want) > 0.01 {
			t.Errorf("Beta%v mean %v, want %v", ab, mean, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	base := string(load(t, "x402-endpoint-1.json"))
	rubricCases := map[string]string{
		"unknown field":      strings.Replace(base, `"subject_types"`, `"subjekt_types": ["x"], "subject_types"`, 1),
		"bad dimension":      strings.Replace(base, `"dimension": "good"`, `"dimension": "fun"`, 1),
		"bad kind":           strings.Replace(base, `"kind": "free", "metric": "rate", "on_revision": "half", "title": "Reachable"`, `"kind": "gratis", "metric": "rate", "title": "Reachable"`, 1),
		"curve not log":      strings.Replace(base, `{"input": "p95", "points": [[300, 100]`, `{"space": "linear", "input": "p95", "points": [[300, 100]`, 1),
		"curve descending":   strings.Replace(base, `[[300, 100], [1000, 90]`, `[[1000, 100], [300, 90]`, 1),
		"gate unknown check": strings.Replace(base, `"check": "H8"`, `"check": "Z9"`, 1),
		"no F floor":         strings.Replace(base, `{"grade": "F", "min": 0}`, `{"grade": "F", "min": 10}`, 1),
		"version":            strings.Replace(base, `"1.0.0-draft.1"`, `"one"`, 1),
		"trailing":           base + "{}",
	}
	for name, raw := range rubricCases {
		if raw == base {
			t.Fatalf("%s: replacement did not apply", name)
		}
		if _, err := ParseRubric([]byte(raw)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	r, _ := ParseRubric([]byte(base))
	ok := `{"schema":"swarmmemo.observations/1","subject":{"type":"x402","key":"GET https://a.example/x"},"as_of":"2026-10-11T00:00:00Z","checks":%s}`
	obsCases := map[string]string{
		"unknown check":   `{"Z9":{"na":true}}`,
		"future event":    `{"R1":{"events":[{"at":"2026-10-12T00:00:00Z","pass":1,"fail":0}]}}`,
		"empty event":     `{"R1":{"events":[{"at":"2026-10-10T00:00:00Z","pass":0,"fail":0}]}}`,
		"samples on rate": `{"R1":{"samples":[{"at":"2026-10-10T00:00:00Z","value":1}]}}`,
		"events on curve": `{"R3":{"events":[{"at":"2026-10-10T00:00:00Z","pass":1,"fail":0}]}}`,
		"na with events":  `{"R1":{"na":true,"events":[{"at":"2026-10-10T00:00:00Z","pass":1,"fail":0}]}}`,
		"weight > 1":      `{"R1":{"events":[{"at":"2026-10-10T00:00:00Z","pass":1,"fail":0,"weight":2}]}}`,
	}
	for name, checks := range obsCases {
		if _, err := ParseObservations([]byte(strings.Replace(ok, "%s", checks, 1)), r); err == nil {
			t.Errorf("observations %s: parsed", name)
		}
	}
	if _, err := ParseObservations([]byte(strings.Replace(ok, "%s", `{}`, 1)), r); err != nil {
		t.Errorf("empty checks: %v", err)
	}
}

// The loader also reads the alternative shapes a sibling rubric file may use.
func TestTolerantShapes(t *testing.T) {
	raw := `{"id":"x","version":"1.0.0","subject_types":["x402"],
		"dimensions":{"works":45,"as_described":{"weight":30},"safe":15,"good":10},
		"checks":[{"id":"A","dimension":"works","weight":1,"kind":"free","type":"rate"},
		          {"id":"B","dimension":"as_described","weight":1,"kind":"free","metric":"curve","curve":[{"x":1,"score":100},{"x":10,"score":0}]},
		          {"id":"C","dimension":"safe","weight":1,"kind":"paid","metric":"rate"},
		          {"id":"D","dimension":"good","weight":1,"kind":"paid","metric":"rate","curve_points":[[0.001,100],[0.1,0]]}],
		"grades":{"A":90,"B":80,"F":0},"confidence":{"high":10,"low":100}}`
	r, err := ParseRubric([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{r.Dimensions[0].ID, r.Dimensions[1].ID, r.Dimensions[3].ID}; got[0] != "works" || got[1] != "as_described" || got[2] != "good" {
		t.Errorf("dimension order %v", got)
	}
	if r.Checks[1].Curve.Input != "p95" || r.Checks[3].Curve.Input != "fail_rate" || r.Grades[0].Grade != "A" {
		t.Errorf("defaults: %+v %+v %+v", r.Checks[1].Curve, r.Checks[3].Curve, r.Grades)
	}
}

func BenchmarkScore(b *testing.B) {
	r, err := ParseRubric(load(b, "x402-endpoint-1.json"))
	if err != nil {
		b.Fatal(err)
	}
	o, err := ParseObservations(load(b, "obs-healthy.json"), r)
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		Score(r, o)
	}
}

func FuzzParseRubric(f *testing.F) {
	f.Add(load(f, "x402-endpoint-1.json"))
	f.Add([]byte(`{"id":"x","version":"1.0.0","subject_types":["x402"],"dimensions":{"works":1},"checks":[{"id":"A","dimension":"works","weight":1,"kind":"free","metric":"curve","curve":[[1,1],[2,2]]}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, err := ParseRubric(raw)
		if err != nil {
			return
		}
		r.Draws = 200 // keep each fuzz iteration fast
		o, err := ParseObservations([]byte(`{"schema":"swarmmemo.observations/1","subject":{"type":"`+r.SubjectTypes[0]+`","key":"k"},"as_of":"2026-10-11T00:00:00Z","checks":{}}`), r)
		if err != nil {
			t.Fatal(err)
		}
		res := Score(r, o)
		if res.Score < 0 || res.Score > 100 || res.Band[0] > res.Band[1] {
			t.Fatalf("score %d band %v", res.Score, res.Band)
		}
	})
}

func FuzzScoreObservations(f *testing.F) {
	rb := load(f, "x402-endpoint-1.json")
	for _, n := range []string{"healthy", "free-only", "gated", "down"} {
		f.Add(load(f, "obs-"+n+".json"))
	}
	r, err := ParseRubric(rb)
	if err != nil {
		f.Fatal(err)
	}
	r.Draws = 200
	f.Fuzz(func(t *testing.T, raw []byte) {
		o, err := ParseObservations(raw, r)
		if err != nil {
			return
		}
		a := Score(r, o).Encode()
		o2, err := ParseObservations(o.Canonical(), r)
		if err != nil {
			t.Fatalf("canonical form does not parse: %v", err)
		}
		if b := Score(r, o2).Encode(); !bytes.Equal(a, b) {
			t.Fatalf("canonical round trip changed the result")
		}
		var res Result
		if json.Unmarshal(a, &res) != nil || res.Score < 0 || res.Score > 100 || res.Band[0] > res.Score || res.Score > res.Band[1] {
			t.Fatalf("bad result %s", a)
		}
	})
}
