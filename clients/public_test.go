package clients

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"
)

// C445: every published version of a pinned client file is served
// unchanged at DIR/STEM-SHA8.EXT, the current one included, and listed in
// DIR/manifest.json; the plain path serves the current one.
func TestPinnedVersions(t *testing.T) {
	snapshots, err := fs.Glob(source, "*/"+versionsDir+"/*")
	if err != nil || len(snapshots) == 0 {
		t.Fatalf("no snapshots: %v", err)
	}
	for _, name := range snapshots {
		m := versionRE.FindStringSubmatch(path.Base(name))
		if m == nil {
			t.Fatalf("%s is not named STEM-SHA8.EXT", name)
		}
		content, _ := source.ReadFile(name)
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:])[:8] != m[2] {
			t.Fatalf("%s: content sha256 %x does not start with its name's", name, sum)
		}
		// A pinned file's current version has its snapshot: change the
		// file, and this asks for the new one.
		dir := strings.Split(name, "/")[0]
		current, err := source.ReadFile(dir + "/" + m[1] + "." + m[3])
		if err != nil {
			t.Fatalf("%s: no current %s.%s", name, m[1], m[3])
		}
		now := sha256.Sum256(current)
		if _, _, ok := ReadPath("/clients/" + dir + "/" + m[1] + "-" + hex.EncodeToString(now[:])[:8] + "." + m[3]); !ok {
			t.Fatalf("%s/%s.%s changed: copy it to %s/%s/%s-%x.%s (never remove a snapshot)", dir, m[1], m[3], dir, versionsDir, m[1], now[:4], m[3])
		}
	}

	// The versions task texts already pin stay served, byte for byte.
	for _, sum := range []string{"0de02b307206ce4b", "63bbbaad99e44cc7", "2b846ccc57577ede"} {
		p := "/clients/python/verify_log-" + sum[:8] + ".py"
		content, _, ok := ReadPath(p)
		got := sha256.Sum256(content)
		if !ok || !strings.HasPrefix(hex.EncodeToString(got[:]), sum) || !Immutable(p) {
			t.Fatalf("%s: found %v, sha256 %x", p, ok, got)
		}
	}
	for _, p := range []string{"/clients/python/verify_log-00000000.py", "/clients/python/versions/verify_log-63bbbaad.py", "/clients/python/verify_log-63BBBAAD.py", "/clients/python/../python/verify_log-63bbbaad.py"} {
		if _, _, ok := ReadPath(p); ok {
			t.Fatalf("%s is served", p)
		}
	}
	if Immutable("/clients/python/verify_log.py") || Immutable("/clients/python/manifest.json") {
		t.Fatal("a changing path reads immutable")
	}

	raw, name, ok := ReadPath("/clients/python/manifest.json")
	var m Manifest
	if !ok || name != "python/manifest.json" || json.Unmarshal(raw, &m) != nil {
		t.Fatalf("manifest: %v %s %s", ok, name, raw)
	}
	f := m.Files["verify_log.py"]
	current, _ := source.ReadFile("python/verify_log.py")
	sum := sha256.Sum256(current)
	if f.Path != "/clients/python/verify_log.py" || f.SHA256 != hex.EncodeToString(sum[:]) || f.ImmutablePath != "/clients/python/verify_log-"+f.SHA256[:8]+".py" {
		t.Fatalf("manifest entry %+v", f)
	}
	listed := map[string]string{}
	for _, v := range f.Versions {
		listed[v.SHA256] = v.Path
		content, _, ok := ReadPath(v.Path)
		got := sha256.Sum256(content)
		if !ok || hex.EncodeToString(got[:]) != v.SHA256 {
			t.Fatalf("manifest version %+v is not what its path serves", v)
		}
	}
	if listed[f.SHA256] != f.ImmutablePath || len(f.Versions) < 9 {
		t.Fatalf("manifest versions %v", f.Versions)
	}
	if _, _, ok := ReadPath("/clients/javascript/manifest.json"); ok {
		t.Fatal("a directory with no pinned file has a manifest")
	}
}
