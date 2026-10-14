// Package plugins ships the Python plugin host: the `inks` / `inks_plugin`
// API modules and the host loop that talks to the editor over stdio.
package plugins

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed python/*.py
var files embed.FS

// Install writes the host files into dir (only those that changed) and
// returns the path of plugin_host.py.
func Install(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := fs.ReadDir(files, "python")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		data, _ := files.ReadFile("python/" + e.Name())
		dst := filepath.Join(dir, e.Name())
		if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "plugin_host.py"), nil
}
