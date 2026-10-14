package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"inks/internal/linediff"
)

// Git integration: change marks in the gutter (against HEAD), file status
// colors in the side bar, and the branch in the status bar. Git runs in the
// background; results are applied on the UI goroutine.

type gitInfo struct {
	mu     sync.Mutex
	roots  map[string]string // dir → repository root ("" = not a repo)
	status map[string]byte   // absolute path → 'M' modified, 'A' added, '?' untracked, 'D' deleted, 'C' conflict
	dirs   map[string]byte   // folders containing changes
	branch map[string]string // root → branch
	head   map[string]string // root → HEAD commit
	busy   bool
}

func newGitInfo() *gitInfo {
	return &gitInfo{roots: map[string]string{}, status: map[string]byte{}, dirs: map[string]byte{}, branch: map[string]string{}, head: map[string]string{}}
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

// root returns the repository root containing dir (cached).
func (g *gitInfo) root(dir string) string {
	g.mu.Lock()
	r, ok := g.roots[dir]
	g.mu.Unlock()
	if ok {
		return r
	}
	out, err := git(dir, "rev-parse", "--show-toplevel")
	r = ""
	if err == nil {
		r = strings.TrimSpace(out)
	}
	g.mu.Lock()
	g.roots[dir] = r
	g.mu.Unlock()
	return r
}

// refreshGit updates status, branches and HEADs for the open folders and
// files, then reloads gutter bases if HEAD moved.
func (a *App) refreshGit() {
	g := a.git
	if g.busy || !a.set.Bool("show_git_status") {
		return
	}
	g.busy = true
	dirs := map[string]bool{}
	for _, f := range a.folders {
		dirs[f] = true
	}
	for _, d := range a.allDocs() {
		if d.Path != "" {
			dirs[filepath.Dir(d.Path)] = true
		}
	}
	go func() {
		roots := map[string]bool{}
		for dir := range dirs {
			if r := g.root(dir); r != "" {
				roots[r] = true
			}
		}
		status := map[string]byte{}
		folders := map[string]byte{}
		branch := map[string]string{}
		head := map[string]string{}
		for r := range roots {
			if out, err := git(r, "status", "--porcelain=v1", "-z", "--untracked-files=all"); err == nil {
				parseStatus(r, out, status, folders)
			}
			if out, err := git(r, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
				branch[r] = strings.TrimSpace(out)
			}
			if out, err := git(r, "rev-parse", "HEAD"); err == nil {
				head[r] = strings.TrimSpace(out)
			}
		}
		a.Do(func() {
			g.busy = false
			moved := false
			for r, h := range head {
				if g.head[r] != h {
					moved = true
				}
			}
			g.status, g.dirs, g.branch, g.head = status, folders, branch, head
			if moved {
				for _, d := range a.allDocs() {
					d.gitBaseAt = time.Time{}
				}
			}
			for _, d := range a.allDocs() {
				a.loadGitBase(d)
			}
		})
	}()
}

func parseStatus(root, out string, status, dirs map[string]byte) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		x, y, rel := f[0], f[1], f[3:]
		if x == 'R' || x == 'C' {
			i++ // the original name follows
		}
		var s byte
		switch {
		case x == '?':
			s = '?'
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			s = 'C'
		case x == 'A':
			s = 'A'
		case x == 'D' || y == 'D':
			s = 'D'
		default:
			s = 'M'
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		status[p] = s
		for dir := filepath.Dir(p); len(dir) >= len(root) && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if dirs[dir] == 0 || s == 'M' {
				dirs[dir] = s
			}
			if dir == root {
				break
			}
		}
	}
}

// statusOf returns the git status of a path, following symlinked temp dirs.
func (g *gitInfo) statusOf(p string) byte {
	if s, ok := g.status[p]; ok {
		return s
	}
	if s, ok := g.dirs[p]; ok {
		return s
	}
	return 0
}

// loadGitBase fetches the HEAD version of a document's file for the gutter.
func (a *App) loadGitBase(d *Doc) {
	if d.Path == "" || d.gitLoading || !d.gitBaseAt.IsZero() || !a.set.Bool("git_gutter") {
		return
	}
	d.gitLoading = true
	path := d.Path
	go func() {
		var base []string
		tracked := false
		dir := filepath.Dir(path)
		if root := a.git.root(dir); root != "" {
			real := path
			if r, err := filepath.EvalSymlinks(path); err == nil {
				real = r
			}
			rootReal := root
			if r, err := filepath.EvalSymlinks(root); err == nil {
				rootReal = r
			}
			if rel, err := filepath.Rel(rootReal, real); err == nil && !strings.HasPrefix(rel, "..") {
				if out, err := git(root, "show", "HEAD:"+filepath.ToSlash(rel)); err == nil {
					text, _ := normalizeDiskText(out)
					base = strings.Split(text, "\n")
					tracked = true
				}
			}
		}
		a.Do(func() {
			d.gitLoading = false
			d.gitBaseAt = time.Now()
			d.gitTracked = tracked
			d.gitBase = base
			d.gitMarksVer = -1
			a.updateGitMarks(d)
		})
	}()
}

// updateGitMarks re-diffs the document against its base when it changed.
func (a *App) updateGitMarks(d *Doc) {
	if !d.gitTracked || d.gitDiffing || d.gitMarksVer == d.M.Buf.Version() {
		return
	}
	d.gitDiffing = true
	ver := d.M.Buf.Version()
	n := d.M.Buf.LineCount()
	cur := make([]string, n)
	for i := 0; i < n; i++ {
		cur[i] = d.M.Buf.Line(i)
	}
	base := d.gitBase
	go func() {
		marks := linediff.Marks(base, cur)
		a.Do(func() {
			d.gitDiffing = false
			d.gitMarks = marks
			d.gitMarksVer = ver
		})
	}()
}

// gitBranchFor returns the branch of the repository holding the active file.
func (a *App) gitBranchFor(d *Doc) string {
	dir := ""
	switch {
	case d != nil && d.Path != "":
		dir = filepath.Dir(d.Path)
	case len(a.folders) > 0:
		dir = a.folders[0]
	default:
		return ""
	}
	a.git.mu.Lock()
	r, ok := a.git.roots[dir]
	a.git.mu.Unlock()
	if !ok || r == "" {
		return ""
	}
	return a.git.branch[r]
}
