package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gioui.org/io/system"

	"inks/internal/platform"
	"inks/internal/remote"
)

// The inks command: requests from bin/inks arrive on a socket (package
// remote) and open in the window that fits them.

// listenCLI starts taking requests from the inks command.
func (s *Shared) listenCLI() {
	if err := remote.Listen(s.handleCLI); err != nil {
		fmt.Fprintln(os.Stderr, "inks command:", err)
	}
}

// handleCLI runs a request on the UI side and, for --wait, returns a
// function that returns once what it opened is closed.
func (s *Shared) handleCLI(r remote.Request) func() {
	opened := make(chan func() bool, 1)
	s.Do(func() { opened <- s.openRequest(r) })
	isOpen := <-opened
	if !r.Wait {
		return nil
	}
	return func() {
		for {
			time.Sleep(300 * time.Millisecond)
			still := make(chan bool, 1)
			s.Do(func() { still <- isOpen() })
			if !<-still {
				return
			}
		}
	}
}

// openRequest opens what the inks command asked for: folders in a window of
// their own (the one already showing them, if any), files in the window
// whose folder holds them, else the active one. With no paths, the current
// folder. It returns a check that reports whether the files (or, for
// folders alone, the window) are still open.
func (s *Shared) openRequest(r remote.Request) func() bool {
	var folders, files []remote.File
	for _, f := range r.Files {
		if st, err := os.Stat(f.Path); err == nil && st.IsDir() {
			folders = append(folders, f)
		} else {
			files = append(files, f)
		}
	}
	if len(r.Files) == 0 && r.Stdin == nil && !r.NewWindow && r.Cwd != "" {
		folders = []remote.File{{Path: r.Cwd}}
	}
	var t *App
	switch {
	case r.Add:
		t = s.active()
	case r.NewWindow:
		t = s.emptyWindow() // the one a cold start just opened
	case len(folders) > 0:
		if t = s.windowWithFolder(folders[0].Path); t == nil {
			t = s.emptyWindow()
		}
	case len(files) > 0:
		if t = s.windowHolding(files[0].Path); t == nil {
			t = s.active()
		}
	default:
		t = s.active()
	}
	if t == nil {
		t = s.NewWindow(nil)
	}
	for _, f := range folders {
		t.addFolder(f.Path)
	}
	if len(folders) > 0 {
		t.showSidebar = true
		t.sideMode = "folders"
	}
	var docs []*Doc
	raise := t
	for _, f := range files {
		d := t.openPath(f.Path)
		if d == nil {
			continue
		}
		d.preview = false
		w := s.ownerOf(d)
		if w == nil {
			w = t
		}
		if f.Line > 0 {
			w.gotoLine(d, f.Line, f.Col)
		}
		raise = w
		docs = append(docs, d)
	}
	if r.Stdin != nil {
		t.replaceEmptyTab()
		d := t.newDoc(*r.Stdin, "")
		docs = append(docs, d)
		raise = t
	}
	raise.focusEditor = true
	raise.perform(system.ActionRaise)
	if len(docs) > 0 {
		return func() bool {
			for _, d := range docs {
				if s.ownerOf(d) != nil {
					return true
				}
			}
			return false
		}
	}
	return func() bool { return slices.Contains(s.windows, t) }
}

// windowWithFolder returns the window showing folder p, if any.
func (s *Shared) windowWithFolder(p string) *App {
	for _, w := range s.windows {
		if slices.Contains(w.folders, p) {
			return w
		}
	}
	return nil
}

// windowHolding returns a window with a folder that contains file p,
// preferring the active window.
func (s *Shared) windowHolding(p string) *App {
	in := func(w *App) bool {
		for _, f := range w.folders {
			if strings.HasPrefix(p, f+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	if a := s.active(); a != nil && in(a) {
		return a
	}
	for _, w := range s.windows {
		if in(w) {
			return w
		}
	}
	return nil
}

// emptyWindow returns a window with no folders and nothing but an empty
// untitled tab (the one a fresh start opens), if there is one.
func (s *Shared) emptyWindow() *App {
	for _, w := range s.windows {
		docs := w.allDocs()
		if len(w.folders) == 0 && len(docs) == 1 && docs[0].Path == "" && !docs[0].Dirty() && docs[0].M.Buf.Len() == 0 {
			return w
		}
	}
	return nil
}

// replaceEmptyTab drops a lone empty untitled tab, as opening a file does.
func (a *App) replaceEmptyTab() {
	if len(a.docs) == 1 && a.docs[0].Path == "" && !a.docs[0].Dirty() && a.docs[0].M.Buf.Len() == 0 {
		a.docs[0].hl.Close()
		a.docs = nil
	}
}

// ---------------------------------------------------------------------------
// Installing the command: a link on the PATH to the launcher in the bundle.

// cliTarget is the launcher inside the running app bundle ("" when inks
// isn't running from a bundle).
func cliTarget() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	i := strings.Index(exe, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	p := filepath.Join(exe[:i+len(".app")], "Contents", "Resources", "bin", "inks")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// cliDirs are the folders the command can be linked into, in order of
// preference: both are on the default PATH (Homebrew's when installed).
var cliDirs = []string{"/usr/local/bin", "/opt/homebrew/bin"}

// linkCLI links dir/inks to target, replacing an earlier link but never a
// file that isn't a link.
func linkCLI(dir, target string) error {
	link := filepath.Join(dir, "inks")
	if st, err := os.Lstat(link); err == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and isn't a link", link)
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.Symlink(target, link)
}

// writable reports whether files can be created in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".inks-")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// installCLI links the inks command onto the PATH, asking for an
// administrator password only when no folder on the PATH is writable.
func (a *App) installCLI() {
	target := cliTarget()
	if target == "" {
		go platform.Message("Install the inks command", "Run inks from inks.app to install the command (for a development build, link bin/inks onto your PATH).")
		return
	}
	go func() {
		var err error
		dir := ""
		for _, d := range cliDirs {
			if writable(d) {
				dir = d
				err = linkCLI(d, target)
				break
			}
		}
		if dir == "" {
			dir = cliDirs[0]
			sh := fmt.Sprintf("mkdir -p %s && ln -sfn %s %s", platform.ShellQuote(dir), platform.ShellQuote(target), platform.ShellQuote(filepath.Join(dir, "inks")))
			script := fmt.Sprintf(`do shell script "%s" with prompt "inks wants to add the inks command to %s." with administrator privileges`, appleQuote.Replace(sh), dir)
			if out, e := exec.Command("osascript", "-e", script).CombinedOutput(); e != nil {
				if strings.Contains(string(out), "-128") { // cancelled
					return
				}
				err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
		}
		if err != nil {
			platform.Message("Unable to install the inks command", err.Error())
			return
		}
		a.Do(func() {
			a.setStatus("Installed the inks command in "+dir+": run inks in a terminal to open its folder", 8*time.Second)
		})
	}()
}

// appleQuote escapes text for an AppleScript string literal.
var appleQuote = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// uninstallCLI removes links to this (or any) inks launcher from the PATH.
func (a *App) uninstallCLI() {
	go func() {
		removed := ""
		for _, d := range cliDirs {
			link := filepath.Join(d, "inks")
			if t, err := os.Readlink(link); err == nil && strings.HasSuffix(t, "/bin/inks") {
				if os.Remove(link) == nil {
					removed = d
				} else {
					platform.Message("Unable to remove the inks command", "Remove "+link+" yourself (it needs an administrator).")
					return
				}
			}
		}
		a.Do(func() {
			if removed == "" {
				a.setStatus("The inks command isn't installed", 4*time.Second)
			} else {
				a.setStatus("Removed the inks command from "+removed, 4*time.Second)
			}
		})
	}()
}
