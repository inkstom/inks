package pkgctl

import (
	"os"
	"path/filepath"
	"testing"
)

// INKS_LIVE=1 go test ./internal/pkgctl -run Live downloads the real index.
func TestLiveInstall(t *testing.T) {
	if os.Getenv("INKS_LIVE") == "" {
		t.Skip("set INKS_LIVE=1 to test against packagecontrol.io")
	}
	dir := t.TempDir()
	m := New(filepath.Join(dir, "Packages"), filepath.Join(dir, "Cache"), filepath.Join(dir, "Packages", "User", "Packages.inks-settings"))
	idx, err := m.Load(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d packages, %d dependencies in the index", len(idx.Packages), len(idx.Deps))
	name := "Pretty JSON"
	if idx.Packages[name] == nil {
		t.Fatalf("%s missing from index", name)
	}
	ver, err := m.Install(name, func(s string) { t.Log(s) })
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "Packages", name, "*.py"))
	t.Logf("installed %s %s: %d plugin files", name, ver, len(files))
	if len(files) == 0 || len(m.Installed()) != 1 || len(m.Recorded()) != 1 {
		t.Fatalf("install result: files %v installed %v recorded %v", files, m.Installed(), m.Recorded())
	}
}
