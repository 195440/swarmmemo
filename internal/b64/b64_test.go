package b64

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type vectors struct {
	Accept []struct{ Value, Hex string }
	Reject []struct{ Value, Why string }
}

func load(t *testing.T) map[string]vectors {
	raw, err := os.ReadFile("../../testdata/base64_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		URL vectors `json:"base64url"`
		Std vectors `json:"base64"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return map[string]vectors{"base64url": v.URL, "base64": v.Std}
}

func TestKnownAnswers(t *testing.T) {
	v := load(t)
	for name, decode := range map[string]func(string) ([]byte, error){"base64url": URL, "base64": Std} {
		set := v[name]
		if len(set.Accept) < 3 || len(set.Reject) < 8 {
			t.Fatalf("%s: too few vectors", name)
		}
		for _, a := range set.Accept {
			got, err := decode(a.Value)
			if err != nil || hex.EncodeToString(got) != a.Hex {
				t.Errorf("%s %q: got %x, %v; want %s", name, a.Value, got, err, a.Hex)
			}
		}
		for _, r := range set.Reject {
			if got, err := decode(r.Value); err == nil {
				t.Errorf("%s %q (%s) decoded to %x; want refused", name, r.Value, r.Why, got)
			}
		}
	}
}

func TestURLSize(t *testing.T) {
	if _, err := URLSize("aGVsbG8", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := URLSize("aGVsbG8", 32); err == nil {
		t.Fatal("wrong length accepted")
	}
	if _, err := URLSize("aGVsbG9", 5); err != ErrURL {
		t.Fatalf("got %v", err)
	}
}

func FuzzURL(f *testing.F) {
	for _, s := range []string{"aGVsbG8", "aGVsbG9", "-_8", "aGVs\nbG8", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if raw, err := URL(s); err == nil && base64.RawURLEncoding.EncodeToString(raw) != s {
			t.Fatalf("%q accepted but re-encodes differently", s)
		}
		if raw, err := Std(s); err == nil && base64.StdEncoding.EncodeToString(raw) != s {
			t.Fatalf("%q accepted but re-encodes differently", s)
		}
	})
}
