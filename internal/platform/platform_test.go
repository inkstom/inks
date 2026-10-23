package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShellLookup(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	path, found, err := ShellLookup([]string{"ls", "no-such-command-inks"})
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || !found["ls"] || found["no-such-command-inks"] {
		t.Errorf("path %q, found %v", path, found)
	}
}

func TestFindInPath(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "tool"), []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "notes"), []byte("text\n"), 0o644)
	path := "/nowhere" + string(filepath.ListSeparator) + dir
	if !FindInPath("tool", path) || FindInPath("notes", path) || FindInPath("missing", path) {
		t.Error("FindInPath: want only the executable found")
	}
}
