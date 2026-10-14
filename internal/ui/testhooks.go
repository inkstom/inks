package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"inks/internal/buffer"
	"inks/internal/linediff"
	"inks/internal/macmenu"
	"inks/internal/settings"
)

// Snapshot renders the current UI off-screen into a PNG.
func (a *App) Snapshot(path string, w, h int, scale float32) error {
	win, err := headless.NewWindow(w, h)
	if err != nil {
		return err
	}
	defer win.Release()
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Constraints: layout.Exact(image.Pt(w, h)), Metric: unit.Metric{PxPerDp: scale, PxPerSp: scale}, Now: time.Now()}
	saved := a.gtx
	a.gtx = gtx
	a.drawUI(gtx)
	a.gtx = saved
	if err := win.Frame(ops); err != nil {
		return err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// TestHooks runs scripted steps inside the live window, one per frame, and
// reports results on stdout (INKS_SELFTEST=1).
type TestHooks struct {
	steps  []func(a *App) error
	i      int
	passed int
	failed []string
	wait   int
	tries  int
}

var menuDump chan string

const selftestPlugin = `import inks
import inks_plugin


class SelftestInsertCommand(inks_plugin.TextCommand):
    def run(self, edit, text=""):
        for r in self.view.sel():
            self.view.insert(edit, r.begin(), text)
        found = self.view.find("beta", 0)
        self.view.add_regions("selftest", [found], "invalid", flags=inks.DRAW_SQUIGGLY_UNDERLINE | inks.DRAW_NO_FILL)
        self.view.set_status("selftest", "size %d" % self.view.size())


class SelftestListener(inks_plugin.EventListener):
    def on_query_completions(self, view, prefix, locations):
        return [["fooFromPlugin\tplugin", "fooFromPlugin"]]
`

// errWait makes a step run again on the next frame (for background work).
var errWait = fmt.Errorf("wait")

func (t *TestHooks) frame(a *App) {
	if t.wait > 0 {
		t.wait--
		a.win.Invalidate()
		return
	}
	if t.i >= len(t.steps) {
		if len(t.failed) > 0 {
			fmt.Printf("[selftest] FAIL %d checks failed:\n  %s\n", len(t.failed), strings.Join(t.failed, "\n  "))
			for _, l := range a.console {
				fmt.Println("[console]", l.text)
			}
			os.Exit(1)
		}
		fmt.Printf("[selftest] %d checks passed\n", t.passed)
		if shot := os.Getenv("INKS_SNAPSHOT"); shot != "" {
			snapshotScenes(a, shot)
		}
		os.Exit(0)
	}
	err := t.steps[t.i](a)
	if err == errWait && t.tries < 300 {
		t.tries++
		a.win.Invalidate()
		return
	}
	t.tries = 0
	if err != nil {
		t.failed = append(t.failed, fmt.Sprintf("step %d: %v", t.i+1, err))
	} else {
		t.passed++
	}
	t.i++
	t.wait = 1 // let a frame render between steps
	a.win.Invalidate()
}

func expect(cond bool, format string, args ...any) error {
	if cond {
		return nil
	}
	return fmt.Errorf(format, args...)
}

// SelfTest returns the built-in end-to-end checks.
func SelfTest(dir string) *TestHooks {
	file := dir + "/main.tf"
	_ = os.WriteFile(file, []byte("resource \"b\" \"x\" {\n  y = 2\n}\n\nresource \"a\" \"y\" {\n  long_name = 1\n  z = 3\n}\n"), 0o644)
	return &TestHooks{steps: []func(a *App) error{
		func(a *App) error {
			d := a.openPath(file)
			return expect(d != nil && d.Lang == "Terraform", "open + detect: %+v", d)
		},
		func(a *App) error {
			d := a.doc()
			return expect(d.hl.Version() > 0 && len(d.hl.Line(0)) > 0, "highlighting not ready")
		},
		func(a *App) error {
			a.run("sort_blocks", nil)
			return expect(strings.HasPrefix(a.doc().M.Buf.Text("\n"), "resource \"a\""), "sort blocks: %q", a.doc().M.Buf.Line(0))
		},
		func(a *App) error {
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 1, Col: 2})}, 0)
			a.run("align_equals", nil)
			return expect(d.M.Buf.Line(1) == "  long_name = 1" && d.M.Buf.Line(2) == "  z         = 3", "align: %q / %q", d.M.Buf.Line(1), d.M.Buf.Line(2))
		},
		func(a *App) error {
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{})}, 0)
			a.run("find_under_expand", nil)
			a.run("find_under_expand", nil)
			return expect(len(d.M.Sels) == 2, "cmd+d: %d selections", len(d.M.Sels))
		},
		func(a *App) error {
			d := a.doc()
			d.M.Insert("data")
			return expect(strings.Count(d.M.Buf.Text("\n"), "data \"") == 2 && d.Dirty(), "multi-cursor typing: %q", d.M.Buf.Text("\n"))
		},
		func(a *App) error {
			a.run("undo", nil)
			return expect(strings.Count(a.doc().M.Buf.Text("\n"), "resource") == 2, "undo")
		},
		func(a *App) error {
			a.run("show_find_panel", nil)
			a.find.find.SetText(`resource :[t] :[n] {:[body]}`)
			a.find.opts["structural"] = true
			return expect(a.find.visible && len(a.find.allMatches(a.doc())) == 2, "structural find: %d", len(a.find.allMatches(a.doc())))
		},
		func(a *App) error {
			a.find.repl.SetText(`module :[n] {:[body]}`)
			a.find.replaceAll()
			return expect(strings.HasPrefix(a.doc().M.Buf.Line(0), `module "y"`), "structural replace: %q", a.doc().M.Buf.Line(0))
		},
		func(a *App) error {
			a.run("escape", nil)
			a.run("show_overlay_command_palette", nil)
			return expect(a.quick.visible, "palette not visible")
		},
		func(a *App) error {
			a.quick.editor.SetText("upper")
			return nil
		},
		func(a *App) error {
			ok := len(a.quick.items) > 0 && a.quick.items[0].Caption == "Convert Case: Upper Case"
			a.quick.cancel()
			return expect(ok, "palette match: %+v", firstCaption(a.quick.items))
		},
		func(a *App) error {
			a.run("save", nil)
			data, _ := os.ReadFile(file)
			return expect(strings.HasPrefix(string(data), `module "y"`) && !a.doc().Dirty(), "save: %q", string(data))
		},
		func(a *App) error {
			a.addFolder(dir)
			a.showSidebar = true
			items := a.gotoFiles("main")
			return expect(len(items) > 0 && items[0].Caption == "main.tf", "goto anything: %+v", firstCaption(items))
		},
		// Find in files: search, skip one match, replace on disk, undo.
		func(a *App) error {
			_ = os.WriteFile(dir+"/a.tf", []byte("foo foo\n"), 0o644)
			_ = os.MkdirAll(dir+"/b", 0o755)
			_ = os.WriteFile(dir+"/b/c.tf", []byte("x = foo\n"), 0o644)
			_ = os.WriteFile(dir+"/notes.md", []byte("foo\n"), 0o644)
			a.run("show_find_in_files", nil)
			a.search.find.SetText("foo")
			a.search.exclude.SetText("*.md")
			a.search.start()
			return expect(a.sideMode == "search", "search view not shown")
		},
		func(a *App) error {
			s := a.search
			if s.running {
				return errWait
			}
			return expect(s.total == 3 && len(s.files) == 2 && s.files[0].rel == "a.tf", "find in files: %d results in %d files", s.total, len(s.files))
		},
		func(a *App) error {
			s := a.search
			s.files[0].matches[1].excluded = true
			s.repl.SetText("bar")
			s.replace(nil, -1, false)
			x, _ := os.ReadFile(dir + "/a.tf")
			y, _ := os.ReadFile(dir + "/b/c.tf")
			z, _ := os.ReadFile(dir + "/notes.md")
			return expect(string(x) == "bar foo\n" && string(y) == "x = bar\n" && string(z) == "foo\n", "replace in files: %q %q %q", x, y, z)
		},
		func(a *App) error {
			if a.search.running {
				return errWait
			}
			a.search.undoReplace()
			x, _ := os.ReadFile(dir + "/a.tf")
			y, _ := os.ReadFile(dir + "/b/c.tf")
			return expect(string(x) == "foo foo\n" && string(y) == "x = foo\n", "undo replace: %q %q", x, y)
		},
		func(a *App) error {
			// Open files are searched and replaced in the editor, not on disk.
			if a.search.running {
				return errWait
			}
			d := a.openPath(dir + "/b/c.tf")
			d.M.SetSels([]buffer.Range{buffer.Cursor(d.M.Buf.EndPos())}, 0)
			d.M.Insert("foo again")
			a.search.start()
			return nil
		},
		func(a *App) error {
			s := a.search
			if s.running {
				return errWait
			}
			if s.total != 4 {
				return fmt.Errorf("unsaved doc search: %d", s.total)
			}
			s.replace(nil, -1, false)
			d := a.doc()
			disk, _ := os.ReadFile(dir + "/b/c.tf")
			return expect(d.M.Buf.Text("\n") == "x = bar\nbar again" && string(disk) == "x = foo\n", "replace in open doc: %q / disk %q", d.M.Buf.Text("\n"), disk)
		},
		func(a *App) error {
			// Changes made by other programs are picked up.
			d := a.openPath(dir + "/a.tf")
			_ = os.WriteFile(dir+"/a.tf", []byte("changed outside\n"), 0o644)
			later := time.Now().Add(time.Minute)
			_ = os.Chtimes(dir+"/a.tf", later, later)
			a.checkDisk()
			return expect(d.M.Buf.Line(0) == "changed outside" && !d.Dirty(), "reload from disk: %q", d.M.Buf.Line(0))
		},
		func(a *App) error {
			// Git gutter: commit a file, change it, and wait for the marks.
			repo := dir + "/repo"
			_ = os.MkdirAll(repo, 0o755)
			_ = os.WriteFile(repo+"/g.txt", []byte("one\ntwo\nthree\nfour\n"), 0o644)
			for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "x"}} {
				if _, err := git(repo, args...); err != nil {
					return fmt.Errorf("git %v: %v", args, err)
				}
			}
			d := a.openPath(repo + "/g.txt")
			d.M.Buf.Apply([]buffer.Edit{{Start: buffer.Pos{Line: 1}, End: buffer.Pos{Line: 1, Col: 3}, Text: "TWO"}, {Start: buffer.Pos{Line: 3}, End: buffer.Pos{Line: 3}, Text: "new\n"}}, nil, buffer.ApplyOptions{})
			return nil
		},
		func(a *App) error {
			d := a.doc()
			a.updateGitMarks(d)
			if len(d.gitMarks) != d.M.Buf.LineCount() || d.gitMarksVer != d.M.Buf.Version() {
				return errWait
			}
			mk := d.gitMarks
			return expect(mk[0] == linediff.None && mk[1] == linediff.Modified && mk[3] == linediff.Added, "git marks: %v", mk)
		},
		func(a *App) error {
			a.refreshGit()
			return nil
		},
		func(a *App) error {
			if a.git.busy {
				return errWait
			}
			_, onDisk := a.git.status[dir+"/repo/g.txt"]
			br := a.gitBranchFor(a.doc())
			return expect(br != "" && !onDisk, "git status: branch %q, status %v", br, a.git.status)
		},
		func(a *App) error {
			// Word wrap: a long line takes several rows; ↓ moves by row.
			d := a.newDoc("short\n"+strings.Repeat("wrap me please ", 40)+"\nend", "")
			v := true
			d.wrapOverride = &v
			g := a.geom(a.gtx, d)
			if g.rows.total() <= 3 {
				return fmt.Errorf("wrap rows: %d", g.rows.total())
			}
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 1, Col: 0})}, 0)
			a.moveRows(d, 1, false)
			h := d.M.MainSel().Head
			return expect(h.Line == 1 && h.Col > 0, "row move: %+v", h)
		},
		func(a *App) error {
			// Build: a Go module with a compile error; ⌘B, then F4 jumps to it.
			mod := dir + "/gomod"
			_ = os.MkdirAll(mod, 0o755)
			_ = os.WriteFile(mod+"/go.mod", []byte("module x\n\ngo 1.21\n"), 0o644)
			_ = os.WriteFile(mod+"/main.go", []byte("package main\n\nfunc main() {\n\tundefinedThing()\n}\n"), 0o644)
			d := a.openPath(mod + "/main.go")
			names := []string{}
			for _, b := range a.buildsFor(d) {
				names = append(names, b.Name)
			}
			if len(names) == 0 || names[0] != "Go: Build" {
				return fmt.Errorf("builds for go: %v", names)
			}
			a.run("build", nil)
			return expect(a.output.visible && a.output.running, "build did not start: %+v", a.output.lines)
		},
		func(a *App) error {
			if a.output.running {
				return errWait
			}
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{})}, 0)
			a.run("next_result", nil)
			h := d.M.MainSel().Head
			var text []string
			for _, l := range a.output.lines {
				text = append(text, l.text)
			}
			return expect(h.Line == 3 && h.Col == 1, "build error jump: %+v\n%s", h, strings.Join(text, "\n"))
		},
		func(a *App) error {
			// Split layout: move the active file to a second column.
			n := len(a.allDocs())
			d := a.doc()
			a.run("move_to_group", map[string]any{"group": 1.0})
			if a.layoutName != "columns_2" || a.focus != 1 || a.doc() != d || len(a.allDocs()) != n {
				return fmt.Errorf("move to group: layout %s focus %d docs %d/%d", a.layoutName, a.focus, len(a.allDocs()), n)
			}
			a.run("focus_group", map[string]any{"group": 0.0})
			return expect(a.focus == 0 && a.doc() != d && a.isOpen(d), "focus group 0")
		},
		func(a *App) error {
			// Opening a file shown in another group switches to it.
			d := a.groups[1].docs[0]
			a.openPath(d.Path)
			if a.focus != 1 || a.doc() != d {
				return fmt.Errorf("open in other group: focus %d", a.focus)
			}
			n := len(a.allDocs())
			a.run("set_layout", map[string]any{"layout": "single"})
			return expect(len(a.groups) == 1 && len(a.allDocs()) == n, "back to single: %d groups, %d docs", len(a.groups), len(a.allDocs()))
		},
		func(a *App) error {
			// Preview tabs: a second preview replaces the first; editing keeps it.
			for _, f := range []string{"p1.txt", "p2.txt", "p3.txt"} {
				_ = os.WriteFile(dir+"/"+f, []byte(f+"\n"), 0o644)
			}
			n := len(a.docs)
			a.openPreview(dir + "/p1.txt")
			a.openPreview(dir + "/p2.txt")
			if len(a.docs) != n+1 && !a.isOpen(a.doc()) {
				return fmt.Errorf("preview replace: %d → %d", n, len(a.docs))
			}
			for _, d := range a.docs {
				if strings.HasSuffix(d.Path, "p1.txt") {
					return fmt.Errorf("first preview still open")
				}
			}
			d := a.doc()
			d.M.Insert("x")
			a.afterEdit(d)
			a.openPreview(dir + "/p3.txt")
			return expect(a.isOpen(d) && !d.preview && a.doc().preview, "preview kept after edit")
		},
		func(a *App) error {
			// Python plugins: write one into Packages/User and start the host.
			user := filepath.Join(settings.DataDir(), "Packages", "User")
			_ = os.MkdirAll(user, 0o755)
			_ = os.WriteFile(filepath.Join(user, "selftest_plugin.py"), []byte(selftestPlugin), 0o644)
			d := a.newDoc("alpha beta\ngamma", "")
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 1, Col: 5})}, 0)
			a.py.start()
			return expect(a.py.started, "plugin host did not start: %s", a.py.failed)
		},
		func(a *App) error {
			if !a.py.ready || a.py.commands["selftest_insert"] == "" {
				return errWait
			}
			a.run("selftest_insert", map[string]any{"text": "!"})
			return nil
		},
		func(a *App) error {
			d := a.doc()
			if !strings.Contains(d.M.Buf.Text("\n"), "gamma!") {
				return errWait
			}
			rs := d.regions["selftest"]
			if rs == nil || len(rs.ranges) != 1 || d.M.Buf.Slice(rs.ranges[0].Start(), rs.ranges[0].End()) != "beta" {
				return fmt.Errorf("plugin regions: %+v", rs)
			}
			return expect(d.status["selftest"] == "size 17", "plugin status: %q", d.status["selftest"])
		},
		func(a *App) error {
			// Plugin completions join buffer words.
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(d.M.Buf.EndPos())}, 0)
			d.M.Insert(" fo")
			a.complete.afterType(d, "o")
			return nil
		},
		func(a *App) error {
			c := a.complete
			for _, it := range c.items {
				if it.label == "fooFromPlugin" {
					c.index = 0
					for i, x := range c.items {
						if x.label == "fooFromPlugin" {
							c.index = i
						}
					}
					c.accept()
					line := a.doc().M.Buf.Line(1)
					return expect(strings.HasSuffix(line, " fooFromPlugin"), "accept completion: %q", line)
				}
			}
			return errWait
		},
		func(a *App) error {
			// Undo: the completion, the typing, then the plugin edit.
			d := a.doc()
			a.run("undo", nil)
			a.run("undo", nil)
			if t := d.M.Buf.Text("\n"); t != "alpha beta\ngamma!" {
				return fmt.Errorf("undo typing: %q", t)
			}
			a.run("undo", nil)
			return expect(d.M.Buf.Text("\n") == "alpha beta\ngamma", "undo plugin edit: %q", d.M.Buf.Text("\n"))
		},
		func(a *App) error {
			a.py.eval("print(6 * 7, view.size())")
			return nil
		},
		func(a *App) error {
			for _, l := range a.console {
				if l.text == "42 16" {
					return nil
				}
			}
			return errWait
		},
		func(a *App) error {
			// Folding: the block under the caret collapses to its header.
			d := a.newDoc("func a() {\n\tx := 1\n\ty := 2\n}\n\nfunc b() {\n\tz()\n}\n", "")
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 1, Col: 2})}, 0)
			a.run("fold", nil)
			g := a.geom(a.gtx, d)
			if g.rows.total() != d.M.Buf.LineCount()-2 || d.M.MainSel().Head.Line != 0 {
				return fmt.Errorf("fold: %d rows for %d lines, caret %+v, folds %+v", g.rows.total(), d.M.Buf.LineCount(), d.M.MainSel().Head, d.folds)
			}
			if l, _ := g.rows.lineOfRow(1); l != 3 {
				return fmt.Errorf("row after fold shows line %d", l)
			}
			a.run("fold_by_level", map[string]any{"level": 1.0})
			if len(d.folds) != 2 {
				return fmt.Errorf("fold level 1: %+v", d.folds)
			}
			a.run("unfold_all", nil)
			return expect(len(d.folds) == 0 && a.geom(a.gtx, d).rows.total() == d.M.Buf.LineCount(), "unfold all")
		},
		func(a *App) error {
			// Bookmarks follow edits; F2 cycles through them.
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 5})}, 0)
			a.run("toggle_bookmark", nil)
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 1})}, 0)
			a.run("toggle_bookmark", nil)
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{})}, 0)
			d.M.Insert("// new\n")
			a.run("next_bookmark", nil)
			first := d.M.MainSel().Head.Line
			a.run("next_bookmark", nil)
			second := d.M.MainSel().Head.Line
			return expect(first == 2 && second == 6, "bookmarks: %d %d (%+v)", first, second, d.bookmarks)
		},
		func(a *App) error {
			// Macros: record typing and a command, play it back on another line.
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 0})}, 0)
			a.run("toggle_record_macro", nil)
			a.recordText("> ")
			d.M.Insert("> ")
			a.run("upper_case", nil)
			a.editorKey(d, key.Event{Name: key.NameDownArrow, State: key.Press})
			a.run("toggle_record_macro", nil)
			before := d.M.MainSel().Head.Line
			a.run("run_macro", nil)
			return expect(len(a.macro) == 3 && strings.Contains(d.M.Buf.Line(before), "> ") && d.M.MainSel().Head.Line == before+1, "macro: %d steps, line %q", len(a.macro), d.M.Buf.Line(before))
		},
		func(a *App) error {
			// Jump back returns to where a big move started.
			d := a.doc()
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 1})}, 0)
			a.noteJumps()
			long := a.newDoc(strings.Repeat("x\n", 100), "")
			long.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 80})}, 0)
			a.noteJumps()
			a.run("jump_back", nil)
			return expect(a.doc() == d && d.M.MainSel().Head.Line == 1, "jump back: doc %v line %d", a.doc() == d, a.doc().M.MainSel().Head.Line)
		},
		func(a *App) error {
			// The side bar toggles even with no folder open.
			folders := a.folders
			a.folders = nil
			a.sideMode = "folders"
			a.setSidebar(false)
			a.run("toggle_side_bar", nil)
			on := a.sidebarVisible()
			a.run("toggle_side_bar", nil)
			off := !a.sidebarVisible()
			a.run("show_folders", nil)
			shown := a.sidebarVisible()
			a.reloadSettings() // must not reset it
			kept := a.sidebarVisible()
			a.folders = folders
			return expect(on && off && shown && kept, "side bar toggles: %v %v %v %v", on, off, shown, kept)
		},
		func(a *App) error {
			// ⌥⌘R / ⌥⌘B toggle regex and structural search (exclusive).
			a.run("show_find_panel", nil)
			a.run("toggle_regex", nil)
			r1 := a.find.opts["regex"]
			a.run("toggle_structural", nil)
			ok := r1 && !a.find.opts["regex"] && a.find.opts["structural"]
			a.run("toggle_structural", nil)
			a.run("hide_panel", nil)
			return expect(ok, "find toggles: %v", a.find.opts)
		},
		func(a *App) error {
			// Escape and the close command dismiss the find panel.
			a.run("show_find_panel", nil)
			a.run("escape", nil)
			if a.find.visible {
				return fmt.Errorf("escape left the find panel open")
			}
			a.run("show_replace_panel", nil)
			a.run("hide_panel", nil)
			return expect(!a.find.visible, "hide_panel left the find panel open")
		},
		func(a *App) error {
			// Multiple windows: File › New Window opens a second, focused one.
			before := len(a.windows)
			a.run("new_window", nil)
			if len(a.windows) != before+1 || a.focused == a {
				return fmt.Errorf("new window: %d → %d windows", before, len(a.windows))
			}
			a.focused = a
			// Keep this window in front: Gio stops drawing a covered window,
			// and these steps run in its frames.
			a.perform(system.ActionRaise)
			return nil
		},
		func(a *App) error {
			// Open in New Window moves the tab; opening the file again from the
			// first window shows it where it is instead of opening a copy.
			p := dir + "/moved.txt"
			_ = os.WriteFile(p, []byte("moved\n"), 0o644)
			d := a.openPath(p)
			n := len(a.windows)
			a.moveToNewWindow(d, false)
			if a.isOpen(d) || len(a.windows) != n+1 {
				return fmt.Errorf("move to new window: still here %v, %d windows", a.isOpen(d), len(a.windows))
			}
			other := a.ownerOf(d)
			if other == nil || other == a || other.doc() != d {
				return fmt.Errorf("moved doc not active in the new window")
			}
			a.perform(system.ActionRaise)
			count := len(a.allDocs())
			got := a.openPath(p)
			return expect(got == d && len(a.allDocs()) == count, "reopen should find the other window's tab (copies: %d→%d)", count, len(a.allDocs()))
		},
		func(a *App) error {
			// Dragging a tab within the bar reorders it.
			a.focused = a
			x := a.newDoc("x", "")
			y := a.newDoc("y", "")
			z := a.newDoc("z", "")
			a.moveTab(z, a.focus, a.indexOf(x))
			if a.indexOf(z) != a.indexOf(x)-1 || a.indexOf(y) != a.indexOf(x)+1 {
				return fmt.Errorf("reorder: x=%d y=%d z=%d", a.indexOf(x), a.indexOf(y), a.indexOf(z))
			}
			// Dropping it outside the window tears it off into a new window.
			n := len(a.windows)
			a.dropTab(&tabDrag{doc: y, group: a.focus, pos: image.Pt(-200, -200)})
			a.perform(system.ActionRaise)
			return expect(!a.isOpen(y) && len(a.windows) == n+1 && a.ownerOf(y) != nil, "tear off: %d windows", len(a.windows))
		},
		func(a *App) error {
			// Closing windows without unsaved files needs no questions.
			a.focused = a
			for _, w := range a.windows {
				if w != a && !w.hasUnsaved() {
					w.closeWindow()
				}
			}
			return nil
		},
		func(a *App) error {
			if len(a.windows) > 1 {
				a.perform(system.ActionRaise)
				return errWait
			}
			return expect(a.windows[0] == a && a.active >= 0, "windows left: %d", len(a.windows))
		},
		func(a *App) error {
			// Classic commands: brackets, marks, find under, layouts, distraction free.
			d := a.newDoc("call(alpha, [beta, gamma])\nalpha again\n", "")
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Col: 14})}, 0)
			a.run("expand_selection_to_brackets", nil)
			if r := d.M.MainSel(); d.M.Buf.Slice(r.Start(), r.End()) != "beta, gamma" {
				return fmt.Errorf("expand to brackets: %q", d.M.Buf.Slice(r.Start(), r.End()))
			}
			a.run("expand_selection_to_brackets", nil)
			if r := d.M.MainSel(); d.M.Buf.Slice(r.Start(), r.End()) != "[beta, gamma]" {
				return fmt.Errorf("expand again: %q", d.M.Buf.Slice(r.Start(), r.End()))
			}
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Col: 4})}, 0)
			a.run("move_to_brackets", nil)
			if h := d.M.MainSel().Head; h.Col != 25 {
				return fmt.Errorf("move to brackets: %+v", h)
			}
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Col: 5})}, 0)
			a.run("set_mark", nil)
			d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Col: 10})}, 0)
			a.run("select_to_mark", nil)
			if r := d.M.MainSel(); d.M.Buf.Slice(r.Start(), r.End()) != "alpha" {
				return fmt.Errorf("select to mark: %q", d.M.Buf.Slice(r.Start(), r.End()))
			}
			a.run("find_under", nil)
			if h := d.M.MainSel().Head; h.Line != 1 {
				return fmt.Errorf("find under: %+v", h)
			}
			a.run("set_layout", map[string]any{"layout": "columns_4"})
			four := len(a.groups) == 4
			a.run("set_layout", map[string]any{"layout": "single"})
			a.run("toggle_distraction_free", nil)
			df := a.distractionFree
			a.run("toggle_distraction_free", nil)
			return expect(four && df && !a.distractionFree, "layouts %v / distraction free %v", four, df)
		},
		func(a *App) error {
			// Open and Save dialogs start in the folder last used.
			a.lastDir = dir
			return expect(a.dialogDir("/elsewhere") == dir, "dialogDir: %q", a.dialogDir("/elsewhere"))
		},
		func(a *App) error {
			// Menu shortcuts reflect key bindings.
			k, m := menuKey(a.keymap.SingleKeyFor("show_find_in_files", nil))
			if err := expect(k == "f" && m == macmenuCmdShift, "menu key: %q %x", k, m); err != nil {
				return err
			}
			return nil
		},
		func(a *App) error {
			// The dump runs on the main thread, which waits on frames: do it
			// outside the frame and poll.
			if menuDump == nil {
				menuDump = make(chan string, 1)
				go func() { menuDump <- macmenu.Dump() }()
			}
			var dump string
			select {
			case dump = <-menuDump:
			default:
				return errWait
			}
			if os.Getenv("INKS_DUMP_MENU") != "" {
				fmt.Print(dump)
			}
			return expect(strings.Contains(dump, "Find in Files… [⇧⌘f]") && strings.Contains(dump, "Quit inks [⌘q]") && strings.Contains(dump, "Open Recent"), "native menu missing items:\n%s", dump)
		},
	}}
}

func firstCaption(items []QuickItem) string {
	if len(items) == 0 {
		return "(none)"
	}
	return items[0].Caption
}

// snapshotScenes renders a few representative screens for visual checks.
func snapshotScenes(a *App, base string) {
	repo, _ := os.Getwd()
	a.folders = nil
	a.sideMode = "folders"
	a.addFolder(repo)
	a.tree.expanded[repo+"/internal"] = true
	a.showSidebar = true
	d := a.openPath(repo + "/internal/editor/model.go")
	if d != nil {
		for i := 0; i < 50 && d.hl.Version() == 0; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		d.Highlight()
		time.Sleep(200 * time.Millisecond)
		d.M.SetSels([]buffer.Range{{Anchor: buffer.Pos{Line: 120, Col: 6}, Head: buffer.Pos{Line: 120, Col: 15}, Goal: -1}}, 0)
		d.scrollY = float32(100 * 26)
		d.addFold(144, 155)
		d.bookmarks = []buffer.Pos{{Line: 139}}
	}
	shot := func(name string) {
		if err := a.Snapshot(base+"-"+name+".png", 1440, 900, 1); err != nil {
			fmt.Println("[snapshot] error:", err)
		} else {
			fmt.Println("[snapshot] " + base + "-" + name + ".png")
		}
	}
	shot("editor")
	if md := a.openPath(repo + "/README.md"); md != nil {
		for i := 0; i < 50 && md.hl.Version() == 0; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		shot("wrap")
		a.activate(d)
	}
	a.showGoto("model")
	shot("goto")
	a.quick.hide()
	a.find.visible, a.find.replace = true, true
	a.find.find.SetText("Sels")
	shot("find")
	a.find.visible = false
	a.output.visible = false
	a.setLayout("columns_2")
	a.moveToGroup(1)
	a.setFocus(0)
	shot("split")
	a.setLayout("single")
	// Plugin regions, autocomplete and the console.
	pd := a.newDoc("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n\tundefinedThing()\n\tfm\n}\n", "")
	pd.setLang("Go", a.set)
	pd.Highlight()
	time.Sleep(150 * time.Millisecond)
	pd.regions = map[string]*regionSet{"lint": {ranges: []buffer.Range{{Anchor: buffer.Pos{Line: 4, Col: 1}, Head: buffer.Pos{Line: 4, Col: 15}}}, scope: "invalid", flags: 2048 | 32}}
	pd.phantoms = map[string][]phantom{"lint": {{pos: buffer.Pos{Line: 4, Col: 1}, text: "undefined: undefinedThing"}}}
	pd.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 5, Col: 3})}, 0)
	a.complete.refresh(pd)
	a.consoleLog("plugin host ready (Python 3.14)", false)
	a.consoleLog(">>> view.file_name()", false)
	a.showPanel("console", false)
	a.Snapshot(base+"-plugin.png", 1440, 900, 1) // lays out once so the caret position is known
	shot("plugin")
	a.output.visible = false
	a.search.find.SetText("SetSels")
	a.search.repl.SetText("SetSelections")
	a.search.include.SetText("*.go")
	a.search.show()
	a.search.start()
	for i := 0; i < 200 && a.search.running; i++ {
		time.Sleep(10 * time.Millisecond)
		a.runTasks()
	}
	a.search.files[0].matches[0].excluded = true
	a.search.hovering = true
	a.search.hover = image.Pt(100, 330)
	shot("search")
}
