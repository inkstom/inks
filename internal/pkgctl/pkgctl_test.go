package pkgctl

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionMatches(t *testing.T) {
	for sel, want := range map[string]bool{"*": true, ">3000": true, "<4000": false, "3000 - 4999": true, "4400": true, "4126": false, ">=4400": true} {
		if VersionMatches(sel, 4400) != want {
			t.Errorf("%q: want %v", sel, want)
		}
	}
}

func TestParseIndex(t *testing.T) {
	idx, err := parseIndex([]byte(`{"packages_cache": {"x": [
		{"name": "Old", "releases": [{"sublime_text": "<3000", "url": "u1", "version": "1"}]},
		{"name": "New", "releases": [{"sublime_text": "<3000", "url": "u0", "version": "0"}, {"sublime_text": ">3000", "url": "u2", "version": "2", "platforms": ["osx"], "dependencies": ["dep"]}]}
	]}, "dependencies_cache": {"x": [{"name": "dep", "releases": [{"sublime_text": "*", "url": "d", "version": "1"}]}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Packages["Old"] != nil || idx.Packages["New"] == nil || idx.Packages["New"].Release.URL != "u2" || idx.Deps["dep"] == nil {
		t.Fatalf("index: %+v", idx)
	}
	if deps := idx.Packages["New"].Release.Dependencies; len(deps) != 1 {
		t.Fatalf("deps: %v", deps)
	}
}

func TestExtractUnwrapsFolder(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	f, _ := os.Create(zp)
	w := zip.NewWriter(f)
	for name, body := range map[string]string{"Pkg-main/plugin.py": "x", "Pkg-main/sub/a.txt": "y"} {
		fw, _ := w.Create(name)
		fw.Write([]byte(body))
	}
	w.Close()
	f.Close()
	dest := filepath.Join(dir, "Pkg")
	if err := Extract(zp, dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "sub", "a.txt")); err != nil || string(b) != "y" {
		t.Fatalf("extract: %v %q", err, b)
	}
	// Zip slip is refused.
	f, _ = os.Create(zp)
	w = zip.NewWriter(f)
	fw, _ := w.Create("../evil.txt")
	fw.Write([]byte("z"))
	fw, _ = w.Create("ok.txt")
	fw.Write([]byte("z"))
	w.Close()
	f.Close()
	if err := Extract(zp, filepath.Join(dir, "Evil")); err == nil {
		t.Fatal("zip slip not refused")
	}
}
