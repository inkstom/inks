package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"inks/internal/macmenu"
	"inks/internal/platform"
)

// The side bar's right-click menus (a file, a folder, or the empty space
// below the tree) and the file operations behind them.

// fileClipboard holds files cut or copied in the side bar, for Paste.
type fileClipboard struct {
	paths []string
	cut   bool
}

type menuEntry struct {
	label    string
	cmd      string // shows the command's shortcut
	disabled bool
	fn       func()
}

var menuSep = menuEntry{}

// showMenu pops up entries at the pointer and runs the chosen one.
func (a *App) showMenu(entries []menuEntry) {
	items := make([]macmenu.PopupItem, len(entries))
	for i, e := range entries {
		items[i] = macmenu.PopupItem{Title: e.label, Disabled: e.disabled}
		if e.cmd != "" {
			items[i].Key, items[i].Mods = menuKey(a.keymap.SingleKeyFor(e.cmd, nil))
		}
	}
	macmenu.Popup(items, func(i int) {
		if i >= 0 && i < len(entries) && entries[i].fn != nil && !entries[i].disabled {
			a.Do(entries[i].fn)
		}
	})
}

// treeMenu shows the menu for a side bar row, or for the space below the
// rows when r is nil.
func (a *App) treeMenu(r *treeRow) {
	t := a.tree
	var target string // the item clicked
	dir := ""         // where new files go
	switch {
	case r != nil && r.isDir:
		target, dir = r.path, r.path
	case r != nil:
		target, dir = r.path, filepath.Dir(r.path)
	case len(a.folders) > 0:
		dir = a.folders[0]
	}
	if r != nil {
		t.selected = r.path
	}
	var m []menuEntry
	if r != nil && !r.isDir {
		p := r.path
		m = append(m,
			menuEntry{label: "Open", fn: func() {
				if d := a.openPath(p); d != nil {
					d.preview = false
				}
			}},
			menuEntry{label: "Open to the Side", fn: func() { a.openToSide(p) }},
			menuEntry{label: "Open in New Window", fn: func() {
				a.NewWindow(func(n *App) { n.openPath(p) })
			}},
			menuEntry{label: "Open in Default App", fn: func() { go platform.Open(p) }},
			menuSep)
	}
	if dir != "" {
		m = append(m,
			menuEntry{label: "New File…", fn: func() { a.promptNewFile(dir) }},
			menuEntry{label: "New Folder…", fn: func() { a.promptNewFolder(dir) }},
			menuSep,
			menuEntry{label: "Reveal in Finder", fn: func() { go platform.Reveal(orDir(target, dir)) }},
			menuEntry{label: "Open Terminal Here", fn: func() { a.terminalHere(dir, "") }},
			menuEntry{label: "Open Claude Code Here", fn: func() { a.terminalHere(dir, a.cliCommand("claude_command", "claude")) }},
			menuEntry{label: "Open Codex Here", fn: func() { a.terminalHere(dir, a.cliCommand("codex_command", "codex")) }},
			menuSep)
	}
	if r != nil && r.isDir {
		p := r.path
		m = append(m, menuEntry{label: "Find in Folder…", fn: func() { a.findInFolder(p) }}, menuSep)
	}
	if r != nil {
		p := r.path
		m = append(m,
			menuEntry{label: "Cut", disabled: r.root, fn: func() { a.fileClip = &fileClipboard{paths: []string{p}, cut: true} }},
			menuEntry{label: "Copy", fn: func() { a.fileClip = &fileClipboard{paths: []string{p}} }})
	}
	if dir != "" {
		label := "Paste"
		if a.fileClip != nil && len(a.fileClip.paths) == 1 {
			label = "Paste “" + filepath.Base(a.fileClip.paths[0]) + "”"
		}
		m = append(m, menuEntry{label: label, disabled: a.fileClip == nil, fn: func() { a.pasteFiles(dir) }})
	}
	if r != nil {
		p := r.path
		m = append(m,
			menuEntry{label: "Duplicate", disabled: r.root, fn: func() { a.duplicate(p) }},
			menuSep,
			menuEntry{label: "Copy Path", fn: func() { a.copyText(p) }},
			menuEntry{label: "Copy Relative Path", fn: func() { a.copyText(filepath.ToSlash(a.relPath(p))) }},
			menuEntry{label: "Copy Name", fn: func() { a.copyText(filepath.Base(p)) }},
			menuSep,
			menuEntry{label: "Rename…", disabled: r.root, fn: func() { a.promptRename(p) }},
			menuEntry{label: "Delete", disabled: r.root, fn: func() { a.trashPath(p) }})
	}
	m = append(m, menuSep, menuEntry{label: "Add Folder to Workspace…", fn: func() { a.run("prompt_add_folder", nil) }})
	if r != nil && r.root {
		p := r.path
		m = append(m, menuEntry{label: "Remove Folder from Workspace", fn: func() { a.removeFolder(p) }})
	}
	m = append(m,
		menuEntry{label: "Collapse All", fn: func() {
			for k := range t.expanded {
				t.expanded[k] = false
			}
			for _, f := range a.folders {
				t.expanded[f] = true
			}
		}},
		menuEntry{label: "Refresh", fn: func() { a.run("refresh_folders", nil) }})
	a.showMenu(trimSeparators(m))
}

func orDir(target, dir string) string {
	if target != "" {
		return target
	}
	return dir
}

// trimSeparators drops leading, trailing and doubled separators.
func trimSeparators(m []menuEntry) []menuEntry {
	var out []menuEntry
	for _, e := range m {
		if e.label == "" && (len(out) == 0 || out[len(out)-1].label == "") {
			continue
		}
		out = append(out, e)
	}
	for len(out) > 0 && out[len(out)-1].label == "" {
		out = out[:len(out)-1]
	}
	return out
}

// cliCommand returns a command line from settings, defaulting to def.
func (a *App) cliCommand(key, def string) string {
	if s := strings.TrimSpace(a.set.String(key)); s != "" {
		return s
	}
	return def
}

// terminalHere opens a terminal tab in dir, running command if given.
func (a *App) terminalHere(dir, command string) {
	app := a.set.String("terminal_app") // "iTerm", "Terminal" or "" (auto)
	what := "a terminal"
	if command != "" {
		what = command
	}
	a.setStatus("Opening "+what+" in "+tildify(dir)+"…", 3*time.Second)
	go func() {
		if err := platform.OpenTerminal(dir, command, app); err != nil {
			a.Do(func() {
				a.setStatus("Couldn't open the terminal: "+err.Error()+" (allow inks to control it in System Settings › Privacy & Security › Automation)", 10*time.Second)
			})
		}
	}()
}

func (a *App) openToSide(p string) {
	if len(a.groups) < 2 {
		a.setLayout("columns_2")
	}
	a.setFocus((a.focus + 1) % len(a.groups))
	if d := a.openPath(p); d != nil {
		d.preview = false
	}
}

// findInFolder opens Search limited to a folder.
func (a *App) findInFolder(dir string) {
	rel := filepath.ToSlash(a.relPath(dir))
	if len(a.folders) > 1 {
		// relPath prefixes the root folder's name; Search matches paths
		// relative to their root.
		if i := strings.Index(rel, "/"); i >= 0 {
			rel = rel[i+1:]
		} else {
			rel = ""
		}
	}
	a.search.show()
	if rel == "" || strings.HasPrefix(rel, "~") || filepath.IsAbs(rel) {
		a.search.include.SetText("")
	} else {
		a.search.include.SetText(rel + "/**")
	}
	a.search.details = true
	a.search.queryChanged()
}

func (a *App) removeFolder(p string) {
	for i, f := range a.folders {
		if f == p {
			a.folders = append(a.folders[:i], a.folders[i+1:]...)
			break
		}
	}
	a.fileCache = nil
	a.tree.refresh()
}

func (a *App) afterFileChange() {
	a.tree.refresh()
	a.fileCache = nil
	for _, w := range a.windows {
		if w != a {
			w.tree.refresh()
			w.fileCache = nil
			w.win.Invalidate()
		}
	}
}

func (a *App) promptNewFile(dir string) {
	a.input.show("New file in "+tildify(dir)+":", "", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			a.setStatus(name+" already exists", 0)
			a.openPath(p)
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			a.setStatus(err.Error(), 0)
			return
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			a.setStatus(err.Error(), 0)
			return
		}
		a.tree.expanded[dir] = true
		a.afterFileChange()
		if d := a.openPath(p); d != nil {
			d.preview = false
		}
	})
}

func (a *App) promptNewFolder(dir string) {
	a.input.show("New folder in "+tildify(dir)+":", "", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			a.setStatus(err.Error(), 0)
			return
		}
		a.tree.expanded[dir] = true
		a.tree.expanded[p] = true
		a.afterFileChange()
	})
}

func (a *App) promptRename(old string) {
	a.input.show("Rename to:", filepath.Base(old), func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == filepath.Base(old) {
			return
		}
		p := filepath.Join(filepath.Dir(old), name)
		if _, err := os.Stat(p); err == nil {
			a.setStatus(name+" already exists", 0)
			return
		}
		if err := os.Rename(old, p); err != nil {
			a.setStatus(err.Error(), 0)
			return
		}
		a.pathMoved(old, p)
		a.afterFileChange()
	})
}

// pathMoved updates open documents and the tree after a rename or move.
func (a *App) pathMoved(old, p string) {
	for _, w := range a.windows {
		for _, d := range w.allDocs() {
			switch {
			case d.Path == old:
				d.Path = p
			case strings.HasPrefix(d.Path, old+string(filepath.Separator)):
				d.Path = p + d.Path[len(old):]
			default:
				continue
			}
			if st, err := os.Stat(d.Path); err == nil {
				d.modTime = st.ModTime()
			}
			d.gitBaseAt = time.Time{}
		}
		if w.tree.expanded[old] {
			w.tree.expanded[p] = true
		}
	}
}

func (a *App) trashPath(p string) {
	what := "“" + filepath.Base(p) + "”"
	go func() {
		choice := platform.Ask("Move "+what+" to the Trash?", "You can restore it from the Trash.", []string{"Cancel", "Move to Trash"}, 1, 0)
		if choice != 1 {
			return
		}
		err := platform.Trash(p)
		a.Do(func() {
			if err != nil {
				a.setStatus("Couldn't move "+what+" to the Trash: "+err.Error(), 0)
				return
			}
			a.setStatus("Moved "+what+" to the Trash", 3*time.Second)
			a.afterFileChange()
		})
	}()
}

func (a *App) duplicate(p string) {
	dst := freeName(p)
	if err := copyPath(p, dst); err != nil {
		a.setStatus(err.Error(), 0)
		return
	}
	a.afterFileChange()
	a.tree.selected = dst
}

// pasteFiles copies (or, after Cut, moves) the side bar clipboard into dir.
func (a *App) pasteFiles(dir string) {
	c := a.fileClip
	if c == nil {
		return
	}
	n := 0
	for _, src := range c.paths {
		dst := filepath.Join(dir, filepath.Base(src))
		if dst == src && c.cut {
			continue
		}
		if strings.HasPrefix(dir+string(filepath.Separator), src+string(filepath.Separator)) {
			a.setStatus("Can't paste a folder into itself", 0)
			return
		}
		if _, err := os.Stat(dst); err == nil {
			dst = freeName(dst)
		}
		var err error
		if c.cut {
			if err = os.Rename(src, dst); err != nil {
				// Another volume: copy, then remove the original.
				if err = copyPath(src, dst); err == nil {
					err = os.RemoveAll(src)
				}
			}
			if err == nil {
				a.pathMoved(src, dst)
			}
		} else {
			err = copyPath(src, dst)
		}
		if err != nil {
			a.setStatus(err.Error(), 0)
			break
		}
		n++
	}
	if c.cut {
		a.fileClip = nil
	}
	a.tree.expanded[dir] = true
	a.afterFileChange()
	a.setStatus(fmt.Sprintf("Pasted %d item(s) into %s", n, tildify(dir)), 3*time.Second)
}

// freeName returns "name copy.ext", "name copy 2.ext"… that doesn't exist.
func freeName(p string) string {
	dir, base := filepath.Dir(p), filepath.Base(p)
	ext := filepath.Ext(base)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		ext = ""
	}
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		name := stem + " copy" + ext
		if i > 1 {
			name = fmt.Sprintf("%s copy %d%s", stem, i, ext)
		}
		cand := filepath.Join(dir, name)
		if _, err := os.Lstat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// copyPath copies a file or a folder tree.
func copyPath(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case st.IsDir():
		if err := os.MkdirAll(dst, st.Mode().Perm()|0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
