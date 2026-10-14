// Package pkgctl installs, upgrades and removes packages from the public
// package index (packagecontrol.io channel), into the Packages folder.
package pkgctl

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Channel is the package index.
const Channel = "https://packagecontrol.io/channel_v3.json"

// Build is the editor API level reported to packages; releaseField names
// the release attribute that holds the supported build range.
const (
	Build        = 4400
	releaseField = "sublime_text"
)

// Release is one downloadable version.
type Release struct {
	Version      string   `json:"version"`
	URL          string   `json:"url"`
	Builds       string   `json:"-"`
	Platforms    []string `json:"platforms"`
	Dependencies []string `json:"dependencies"`
	Libraries    []string `json:"libraries"`
}

// Package is an index entry with the release that fits this editor.
type Package struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	LoadOrder   string `json:"load_order"`
	Release     Release
}

// Index is the parsed channel.
type Index struct {
	Packages map[string]*Package
	Deps     map[string]*Package
}

// Manager works on one Packages folder.
type Manager struct {
	Packages string // Packages folder
	Cache    string // cache folder
	Settings string // Packages.inks-settings (installed list)
	client   *http.Client
	index    *Index
}

func New(packages, cache, settingsFile string) *Manager {
	return &Manager{Packages: packages, Cache: cache, Settings: settingsFile, client: &http.Client{Timeout: 60 * time.Second}}
}

// VersionMatches reports whether a release build selector ("*", ">3000",
// "3000 - 4999", "4126") includes build.
func VersionMatches(sel string, build int) bool {
	s := strings.TrimSpace(sel)
	if s == "" || s == "*" {
		return true
	}
	if m := regexp.MustCompile(`^(\d+)\s*-\s*(\d+)$`).FindStringSubmatch(s); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		return build >= lo && build <= hi
	}
	if m := regexp.MustCompile(`^(>=|<=|>|<)\s*(\d+)$`).FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[2])
		switch m[1] {
		case ">=":
			return build >= n
		case "<=":
			return build <= n
		case ">":
			return build > n
		case "<":
			return build < n
		}
	}
	if n, err := strconv.Atoi(s); err == nil {
		return build == n
	}
	return false
}

func platformMatches(list []string) bool {
	if len(list) == 0 {
		return true
	}
	for _, p := range list {
		if p == "*" || p == "osx" || p == "osx-arm64" {
			return true
		}
	}
	return false
}

// Load fetches (or reads the cached) index; force skips the cache.
func (m *Manager) Load(force bool) (*Index, error) {
	if m.index != nil && !force {
		return m.index, nil
	}
	cache := filepath.Join(m.Cache, "Packages", "channel_v3.json")
	var data []byte
	if st, err := os.Stat(cache); err == nil && !force && time.Since(st.ModTime()) < time.Hour {
		data, _ = os.ReadFile(cache)
	}
	if data == nil {
		fresh, err := m.fetch(Channel)
		if err != nil {
			// Use a stale copy when offline.
			if old, rerr := os.ReadFile(cache); rerr == nil {
				data = old
			} else {
				return nil, err
			}
		} else {
			data = fresh
			_ = os.MkdirAll(filepath.Dir(cache), 0o755)
			_ = os.WriteFile(cache, data, 0o644)
		}
	}
	idx, err := parseIndex(data)
	if err != nil {
		return nil, err
	}
	m.index = idx
	return idx, nil
}

func parseIndex(data []byte) (*Index, error) {
	var raw struct {
		Packages     map[string][]map[string]any `json:"packages_cache"`
		Dependencies map[string][]map[string]any `json:"dependencies_cache"`
		Libraries    map[string][]map[string]any `json:"libraries_cache"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("package index: %w", err)
	}
	pick := func(groups map[string][]map[string]any, into map[string]*Package) {
		for _, list := range groups {
			for _, p := range list {
				pkg := &Package{}
				pkg.Name, _ = p["name"].(string)
				pkg.Description, _ = p["description"].(string)
				pkg.Homepage, _ = p["homepage"].(string)
				if lo, ok := p["load_order"]; ok {
					pkg.LoadOrder = fmt.Sprint(lo)
				}
				rels, _ := p["releases"].([]any)
				for _, r := range rels {
					rm, _ := r.(map[string]any)
					sel, _ := rm[releaseField].(string)
					var rel Release
					b, _ := json.Marshal(rm)
					_ = json.Unmarshal(b, &rel)
					if VersionMatches(sel, Build) && platformMatches(rel.Platforms) && rel.URL != "" {
						pkg.Release = rel
						into[pkg.Name] = pkg
						break
					}
				}
			}
		}
	}
	idx := &Index{Packages: map[string]*Package{}, Deps: map[string]*Package{}}
	pick(raw.Packages, idx.Packages)
	pick(raw.Dependencies, idx.Deps)
	pick(raw.Libraries, idx.Deps)
	return idx, nil
}

func (m *Manager) fetch(url string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "inks package installer")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// Installed lists package folders (excluding User).
func (m *Manager) Installed() []string {
	entries, _ := os.ReadDir(m.Packages)
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "User" && !strings.HasPrefix(e.Name(), ".") {
			if _, err := os.Stat(filepath.Join(m.Packages, e.Name(), ".inks-dependency")); err == nil {
				continue
			}
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Install downloads a package and its dependencies; log reports progress.
func (m *Manager) Install(name string, log func(string)) (string, error) {
	idx, err := m.Load(false)
	if err != nil {
		return "", err
	}
	p := idx.Packages[name]
	if p == nil {
		return "", fmt.Errorf("%s is not available for this editor", name)
	}
	for _, dep := range append(p.Release.Dependencies, p.Release.Libraries...) {
		d := idx.Deps[dep]
		if d == nil {
			log("dependency " + dep + " not found")
			continue
		}
		dest := filepath.Join(m.Packages, dep)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := m.download(d.Release.URL, dest); err != nil {
			log("unable to install dependency " + dep + ": " + err.Error())
			continue
		}
		order := d.LoadOrder
		if order == "" {
			order = "50"
		}
		_ = os.WriteFile(filepath.Join(dest, ".inks-dependency"), []byte(order), 0o644)
		log("installed dependency " + dep)
	}
	if err := m.download(p.Release.URL, filepath.Join(m.Packages, name)); err != nil {
		return "", err
	}
	m.setInstalled(name, true)
	return p.Release.Version, nil
}

// Remove deletes a package folder (to the Trash when possible via trash).
func (m *Manager) Remove(name string, trash func(string) error) error {
	dir := filepath.Join(m.Packages, name)
	if trash == nil || trash(dir) != nil {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	m.setInstalled(name, false)
	return nil
}

// download fetches a zip and extracts it into dest, replacing it. Archives
// with a single top-level folder (GitHub) are unwrapped.
func (m *Manager) download(url, dest string) error {
	data, err := m.fetch(url)
	if err != nil {
		return err
	}
	tmp := filepath.Join(m.Cache, "Packages", "downloads")
	_ = os.MkdirAll(tmp, 0o755)
	zf := filepath.Join(tmp, filepath.Base(dest)+".zip")
	if err := os.WriteFile(zf, data, 0o644); err != nil {
		return err
	}
	return Extract(zf, dest)
}

// Extract unpacks a zip into dest (replacing it), unwrapping a single
// top-level folder and refusing paths that escape dest.
func Extract(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	prefix := ""
	first := true
	for _, f := range r.File {
		top := strings.SplitN(f.Name, "/", 2)[0]
		if first {
			prefix, first = top, false
		} else if top != prefix {
			prefix = ""
			break
		}
	}
	if prefix != "" {
		prefix += "/"
		// A lone file at the top level is not a wrapper folder.
		for _, f := range r.File {
			if f.Name == strings.TrimSuffix(prefix, "/") {
				prefix = ""
			}
		}
	}
	staging := dest + ".installing"
	_ = os.RemoveAll(staging)
	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" || strings.HasSuffix(f.Name, "/") {
			continue
		}
		target := filepath.Join(staging, filepath.FromSlash(name))
		if !strings.HasPrefix(target, staging+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in archive: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, 512<<20))
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	_ = os.RemoveAll(dest)
	return os.Rename(staging, dest)
}

// setInstalled records the installed list in Packages.inks-settings.
func (m *Manager) setInstalled(name string, add bool) {
	data := map[string]any{}
	if b, err := os.ReadFile(m.Settings); err == nil {
		_ = json.Unmarshal(b, &data)
	}
	set := map[string]bool{}
	if list, ok := data["installed_packages"].([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				set[s] = true
			}
		}
	}
	if add {
		set[name] = true
	} else {
		delete(set, name)
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	data["installed_packages"] = out
	b, _ := json.MarshalIndent(data, "", "\t")
	_ = os.MkdirAll(filepath.Dir(m.Settings), 0o755)
	_ = os.WriteFile(m.Settings, append(b, '\n'), 0o644)
}

// Recorded returns the installed list from Packages.inks-settings.
func (m *Manager) Recorded() []string {
	data := map[string]any{}
	if b, err := os.ReadFile(m.Settings); err == nil {
		_ = json.Unmarshal(b, &data)
	}
	var out []string
	if list, ok := data["installed_packages"].([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
