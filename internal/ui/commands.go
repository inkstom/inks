package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"inks/internal/buffer"
	"inks/internal/editor"
	"inks/internal/fuzzy"
	"inks/internal/platform"
	"inks/internal/settings"
	"inks/internal/syntax"
	"inks/internal/texttools"
	"inks/internal/theme"
)

// Command is a named action. Text commands act on the active document.
type Command struct {
	Run func(args map[string]any)
}

// PaletteEntry is a Command Palette row.
type PaletteEntry struct {
	Caption string
	Command string
	Args    map[string]any
}

func (a *App) run(name string, args map[string]any) {
	a.recordCmd(name, args)
	c, ok := a.commands[name]
	if !ok {
		if a.py.runCommand(name, args, nil) {
			return
		}
		a.setStatus("Unknown command: "+name, 0)
		return
	}
	c.Run(args)
	if d := a.doc(); d != nil {
		d.blink = time.Now()
	}
	a.win.Invalidate()
}

func argString(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}
func argInt(args map[string]any, k string) int {
	switch v := args[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}
func argBool(args map[string]any, k string) bool {
	b, _ := args[k].(bool)
	return b
}

func (a *App) registerCommands() {
	a.commands = map[string]*Command{}
	reg := func(name, caption string, fn func(map[string]any)) {
		a.commands[name] = &Command{Run: fn}
		if caption != "" {
			a.palette = append(a.palette, PaletteEntry{Caption: caption, Command: name})
		}
	}
	// text registers a command acting on the active document.
	text := func(name, caption string, fn func(d *Doc, args map[string]any)) {
		reg(name, caption, func(args map[string]any) {
			if d := a.doc(); d != nil {
				fn(d, args)
				a.afterEdit(d)
			}
		})
	}
	tx := func(name, caption string, fn func(string) (string, error)) {
		text(name, caption, func(d *Doc, _ map[string]any) {
			if err := d.M.ReplaceLinesOrAll(fn); err != nil {
				a.setStatus(err.Error(), 0)
			}
		})
	}
	prompt := func(name, caption, label, def string, fn func(string, string) (string, error)) {
		reg(name, caption, func(map[string]any) {
			a.input.show(label, def, func(v string) {
				if d := a.doc(); d != nil {
					if err := d.M.ReplaceLinesOrAll(func(t string) (string, error) { return fn(t, v) }); err != nil {
						a.setStatus(err.Error(), 0)
					}
					a.afterEdit(d)
				}
			})
		})
	}

	// File
	reg("new_file", "File: New File", func(map[string]any) { a.py.event("on_new", a.newDoc("", ""), nil, nil) })
	reg("prompt_open", "File: Open…", func(map[string]any) {
		start := a.defaultDir()
		if d := a.doc(); d == nil || d.Path == "" {
			start = a.dialogDir(start)
		}
		go func() {
			files := platform.OpenFiles("Open", start)
			a.Do(func() {
				if len(files) > 0 {
					a.lastDir = filepath.Dir(files[0])
				}
				for _, f := range files {
					a.openPath(f)
				}
			})
		}()
	})
	reg("prompt_open_folder", "File: Open Folder…", func(map[string]any) {
		start := a.dialogDir(a.defaultDir())
		go func() {
			if dir := platform.OpenFolder("Open Folder", start); dir != "" {
				a.Do(func() { a.openFolder(dir) })
			}
		}()
	})
	reg("prompt_add_folder", "File: Add Folder to Workspace…", func(map[string]any) {
		start := a.dialogDir(a.defaultDir())
		go func() {
			if dir := platform.OpenFolder("Add Folder to Workspace", start); dir != "" {
				a.Do(func() {
					a.addFolder(dir)
					a.showSidebar = true
					a.sideMode = "folders"
				})
			}
		}()
	})
	reg("open_folder_path", "", func(args map[string]any) {
		if p := argString(args, "folder"); p != "" {
			a.openFolder(p)
		}
	})
	reg("close_folder", "File: Close Folder", func(map[string]any) {
		a.folders = nil
		a.tree.refresh()
		a.fileCache = nil
	})
	reg("new_window", "File: New Window", func(map[string]any) { a.NewWindow(nil) })
	reg("duplicate_workspace", "File: Duplicate Workspace", func(map[string]any) {
		a.NewWindow(func(n *App) {
			for _, f := range a.folders {
				n.addFolder(f)
			}
			n.showSidebar, n.sidebarPinned, n.sidebarW = a.showSidebar, a.sidebarPinned, a.sidebarW
		})
	})
	reg("close_window", "File: Close Window", func(map[string]any) { a.closeWindow() })
	reg("move_to_new_window", "File: Move File to New Window", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.moveToNewWindow(d, false)
		}
	})
	reg("save", "File: Save", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.save(d, nil)
		}
	})
	reg("prompt_save_as", "File: Save As…", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.saveAs(d, nil)
		}
	})
	reg("save_all", "File: Save All", func(map[string]any) {
		for _, d := range a.allDocs() {
			if d.Dirty() && d.Path != "" {
				a.save(d, nil)
			}
		}
	})
	reg("close", "File: Close", func(map[string]any) { a.closeDoc(a.active, nil) })
	reg("close_all", "File: Close All", func(map[string]any) {
		var next func()
		next = func() {
			if len(a.docs) == 1 && a.docs[0].Path == "" && !a.docs[0].Dirty() {
				return
			}
			a.closeDoc(0, next)
		}
		next()
	})
	reg("reopen_last_file", "File: Reopen Closed File", func(map[string]any) {
		if n := len(a.closed); n > 0 {
			p := a.closed[n-1]
			a.closed = a.closed[:n-1]
			a.openPath(p)
		}
	})
	reg("quit", "", func(map[string]any) { a.quit() })
	reg("open_file", "", func(args map[string]any) {
		if p := argString(args, "file"); p != "" {
			a.openPath(p)
		}
	})
	reg("clear_recent_files", "File: Clear Recent Files", func(map[string]any) {
		a.recent = nil
		a.recentFolders = nil
		a.menuStale = true
	})
	reg("reveal_in_finder", "File: Reveal in Finder", func(map[string]any) {
		if d := a.doc(); d != nil && d.Path != "" {
			go platform.Reveal(d.Path)
		}
	})
	reg("revert_file", "File: Revert File", func(map[string]any) {
		d := a.doc()
		if d == nil || d.Path == "" {
			return
		}
		text, mt, err := readFile(d.Path)
		if err != nil {
			a.setStatus("Unable to revert: "+err.Error(), 0)
			return
		}
		a.reloadDoc(d, text, mt)
	})
	reg("open_paths", "", func(args map[string]any) {
		if list, ok := args["paths"].([]any); ok {
			for _, p := range list {
				if s, ok := p.(string); ok {
					a.openPath(s)
				}
			}
		}
	})

	// Editing
	text("undo", "Edit: Undo", func(d *Doc, _ map[string]any) {
		if sels, ok := d.M.Buf.Undo(); ok {
			d.M.SetSels(sels, len(sels)-1)
		}
	})
	text("redo", "Edit: Redo", func(d *Doc, _ map[string]any) {
		if sels, ok := d.M.Buf.Redo(); ok {
			d.M.SetSels(sels, len(sels)-1)
		}
	})
	text("soft_undo", "Edit: Undo Selection", func(d *Doc, _ map[string]any) { d.M.SoftUndo() })
	text("soft_redo", "Edit: Redo Selection", func(d *Doc, _ map[string]any) { d.M.SoftRedo() })
	reg("copy", "", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.copySelection(d, false)
		}
	})
	reg("cut", "", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.copySelection(d, true)
		}
	})
	reg("paste", "", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.requestPaste(d)
		}
	})
	text("indent", "Indentation: Indent", func(d *Doc, _ map[string]any) { d.M.Indent() })
	text("unindent", "Indentation: Unindent", func(d *Doc, _ map[string]any) { d.M.Unindent() })
	text("swap_line_up", "Edit: Swap Line Up", func(d *Doc, _ map[string]any) { d.M.MoveLines(-1) })
	text("swap_line_down", "Edit: Swap Line Down", func(d *Doc, _ map[string]any) { d.M.MoveLines(1) })
	text("duplicate_line", "Edit: Duplicate Line", func(d *Doc, _ map[string]any) { d.M.DuplicateLine() })
	text("delete_line", "Edit: Delete Line", func(d *Doc, _ map[string]any) { d.M.DeleteLine() })
	text("join_lines", "Edit: Join Lines", func(d *Doc, _ map[string]any) { d.M.JoinLines() })
	text("toggle_comment", "Edit: Toggle Comment", func(d *Doc, _ map[string]any) { d.M.ToggleComment() })
	text("insert_line_after", "Edit: Insert Line After", func(d *Doc, _ map[string]any) { d.M.InsertLineAfter() })
	text("insert_line_before", "Edit: Insert Line Before", func(d *Doc, _ map[string]any) { d.M.InsertLineBefore() })
	text("delete_to_end", "Edit: Delete to End", func(d *Doc, _ map[string]any) { d.M.DeleteToLineEnd() })
	text("delete_to_beginning", "Edit: Delete to Beginning", func(d *Doc, _ map[string]any) { d.M.DeleteToLineStart() })
	text("upper_case", "Convert Case: Upper Case", func(d *Doc, _ map[string]any) { d.M.TransformSelections(strings.ToUpper) })
	text("lower_case", "Convert Case: Lower Case", func(d *Doc, _ map[string]any) { d.M.TransformSelections(strings.ToLower) })
	text("title_case", "Convert Case: Title Case", func(d *Doc, _ map[string]any) { d.M.TransformSelections(titleCase) })

	// Selection
	text("select_all", "Selection: Select All", func(d *Doc, _ map[string]any) {
		d.M.SetSels([]buffer.Range{{Anchor: buffer.Pos{}, Head: d.M.Buf.EndPos(), Goal: -1}}, 0)
	})
	text("expand_selection_line", "Selection: Expand to Line", func(d *Doc, _ map[string]any) {
		var sels []buffer.Range
		for _, r := range d.M.Sels {
			s, e := r.Start(), r.End()
			start := buffer.Pos{Line: s.Line}
			end := buffer.Pos{Line: e.Line + 1}
			if e.Col == 0 && e.Line > s.Line {
				end = buffer.Pos{Line: e.Line + 1}
			}
			if end.Line >= d.M.Buf.LineCount() {
				end = d.M.Buf.EndPos()
			}
			sels = append(sels, buffer.Range{Anchor: start, Head: end, Goal: -1})
		}
		d.M.SetSels(sels, len(sels)-1)
	})
	text("split_selection_into_lines", "Selection: Split into Lines", func(d *Doc, _ map[string]any) {
		var sels []buffer.Range
		for _, r := range d.M.Sels {
			if r.Empty() {
				sels = append(sels, r)
				continue
			}
			s, e := r.Start(), r.End()
			for l := s.Line; l <= e.Line; l++ {
				from, to := 0, len(d.M.Buf.Line(l))
				if l == s.Line {
					from = s.Col
				}
				if l == e.Line {
					to = e.Col
					if to == 0 && l > s.Line {
						continue
					}
				}
				sels = append(sels, buffer.Range{Anchor: buffer.Pos{Line: l, Col: from}, Head: buffer.Pos{Line: l, Col: to}, Goal: -1})
			}
		}
		d.M.SetSels(sels, len(sels)-1)
	})
	text("select_lines", "", func(d *Doc, args map[string]any) {
		dir := -1
		if argBool(args, "forward") {
			dir = 1
		}
		sels := append([]buffer.Range(nil), d.M.Sels...)
		r := d.M.MainSel()
		l := r.Head.Line + dir
		if l >= 0 && l < d.M.Buf.LineCount() {
			v := editor.VisualCol(d.M.Buf.Line(r.Head.Line), r.Head.Col, d.M.TabSize)
			p := buffer.Pos{Line: l, Col: editor.ColForVisual(d.M.Buf.Line(l), v, d.M.TabSize)}
			sels = append(sels, buffer.Cursor(p))
			d.M.SetSels(sels, len(sels)-1)
		}
	})
	text("single_selection", "Selection: Single Selection", func(d *Doc, _ map[string]any) {
		r := d.M.MainSel()
		d.M.SetSels([]buffer.Range{buffer.Cursor(r.Head)}, 0)
	})
	text("find_under_expand", "Selection: Expand to Word", func(d *Doc, _ map[string]any) { a.findUnderExpand(d, false) })
	text("find_under_expand_skip", "Selection: Quick Skip Next", func(d *Doc, _ map[string]any) { a.findUnderExpand(d, true) })
	text("find_all_under", "Find: Quick Find All", func(d *Doc, _ map[string]any) {
		r := d.M.MainSel()
		s, e := r.Start(), r.End()
		if r.Empty() {
			var ok bool
			if s, e, ok = d.M.WordAt(r.Head); !ok {
				return
			}
		}
		word := d.M.Buf.Slice(s, e)
		text := d.M.Buf.Text("\n")
		var sels []buffer.Range
		for i := 0; ; {
			k := strings.Index(text[i:], word)
			if k < 0 {
				break
			}
			from := i + k
			sels = append(sels, buffer.Range{Anchor: d.M.Buf.PosAt(from), Head: d.M.Buf.PosAt(from + len(word)), Goal: -1})
			i = from + len(word)
		}
		d.M.SetSels(sels, len(sels)-1)
	})

	// Find
	reg("show_find_panel", "Find: Find…", func(map[string]any) { a.find.show(false) })
	reg("show_replace_panel", "Find: Replace…", func(map[string]any) { a.find.show(true) })
	reg("find_next", "Find: Find Next", func(map[string]any) { a.find.next(true) })
	reg("find_prev", "Find: Find Previous", func(map[string]any) { a.find.next(false) })
	text("slurp_find_string", "Find: Use Selection for Find", func(d *Doc, _ map[string]any) {
		r := d.M.MainSel()
		s, e := r.Start(), r.End()
		if r.Empty() {
			s, e, _ = d.M.WordAt(r.Head)
		}
		if t := d.M.Buf.Slice(s, e); t != "" {
			a.find.find.SetText(t)
			a.setStatus("Find string set to \""+t+"\"", 2*time.Second)
		}
	})

	// Find option toggles act on the find panel, or on the Search side bar
	// when that is what's showing.
	for _, t := range []struct{ cmd, opt, caption string }{
		{"toggle_regex", "regex", "Find: Toggle Regular Expression"},
		{"toggle_case_sensitive", "case", "Find: Toggle Case Sensitive"},
		{"toggle_whole_word", "word", "Find: Toggle Whole Word"},
		{"toggle_structural", "structural", "Find: Toggle Structural Search"},
		{"toggle_preserve_case", "preserve", "Find: Toggle Preserve Case"},
	} {
		opt := t.opt
		reg(t.cmd, t.caption, func(map[string]any) { a.toggleFindOption(opt) })
	}

	// Find in files
	reg("show_find_in_files", "Find: Find in Files…", func(map[string]any) { a.search.show() })
	reg("refresh_folders", "View: Refresh Folders", func(map[string]any) {
		a.tree.refresh()
		a.fileCache = nil
		a.refreshGit()
	})
	reg("show_folders", "View: Show Folders", func(map[string]any) {
		a.sideMode = "folders"
		a.setSidebar(true)
	})
	reg("next_result", "Find: Next Result", func(map[string]any) {
		if !(a.output.visible && a.output.step(1)) {
			a.search.step(1)
		}
	})
	reg("prev_result", "Find: Previous Result", func(map[string]any) {
		if !(a.output.visible && a.output.step(-1)) {
			a.search.step(-1)
		}
	})

	// Layout
	for _, l := range []struct{ name, caption string }{
		{"single", "Layout: Single"}, {"columns_2", "Layout: Columns: 2"}, {"columns_3", "Layout: Columns: 3"},
		{"columns_4", "Layout: Columns: 4"}, {"rows_2", "Layout: Rows: 2"}, {"rows_3", "Layout: Rows: 3"}, {"grid_4", "Layout: Grid: 4"},
	} {
		name := l.name
		a.palette = append(a.palette, PaletteEntry{Caption: l.caption, Command: "set_layout", Args: map[string]any{"layout": name}})
	}
	reg("set_layout", "", func(args map[string]any) { a.setLayout(argString(args, "layout")) })
	reg("focus_group", "", func(args map[string]any) { a.setFocus(argInt(args, "group")) })
	reg("move_to_group", "", func(args map[string]any) { a.moveToGroup(argInt(args, "group")) })
	reg("focus_neighboring_group", "Layout: Focus Next Group", func(map[string]any) { a.setFocus((a.focus + 1) % len(a.groups)) })
	for i := 0; i < 4; i++ {
		a.palette = append(a.palette,
			PaletteEntry{Caption: fmt.Sprintf("Layout: Focus Group %d", i+1), Command: "focus_group", Args: map[string]any{"group": float64(i)}},
			PaletteEntry{Caption: fmt.Sprintf("Layout: Move File to Group %d", i+1), Command: "move_to_group", Args: map[string]any{"group": float64(i)}})
	}

	a.registerPackageCommands(reg)
	a.registerClassicCommands(reg, text)
	tx("sort_lines_case_sensitive", "Text: Sort Lines (Case Sensitive)", func(t string) (string, error) { return texttools.SortLines(t, "case", false, 0, ""), nil })

	// Folding, bookmarks, jumps, macros
	text("fold", "Code Folding: Fold", func(d *Doc, _ map[string]any) {
		if !d.foldSelections() {
			a.setStatus("Nothing to fold here", 2*time.Second)
		}
	})
	text("unfold", "Code Folding: Unfold", func(d *Doc, _ map[string]any) {
		for _, r := range d.M.Sels {
			d.unfoldLines(r.Start().Line, r.End().Line+1)
		}
	})
	text("toggle_fold", "Code Folding: Toggle Fold", func(d *Doc, _ map[string]any) {
		l := d.M.MainSel().Head.Line
		if !d.unfoldLines(l, l+1) {
			d.foldSelections()
		}
	})
	text("unfold_all", "Code Folding: Unfold All", func(d *Doc, _ map[string]any) { d.unfoldAll() })
	for lvl := 1; lvl <= 9; lvl++ {
		a.palette = append(a.palette, PaletteEntry{Caption: fmt.Sprintf("Code Folding: Fold Level %d", lvl), Command: "fold_by_level", Args: map[string]any{"level": float64(lvl)}})
	}
	text("fold_by_level", "", func(d *Doc, args map[string]any) { d.foldByLevel(max(1, argInt(args, "level"))) })
	text("toggle_bookmark", "Bookmarks: Toggle Bookmark", func(d *Doc, _ map[string]any) { d.toggleBookmark() })
	text("next_bookmark", "Bookmarks: Next Bookmark", func(d *Doc, _ map[string]any) {
		if !d.nextBookmark(1) {
			a.setStatus("No bookmarks (⌘F2 adds one)", 0)
		}
	})
	text("prev_bookmark", "Bookmarks: Previous Bookmark", func(d *Doc, _ map[string]any) { d.nextBookmark(-1) })
	text("clear_bookmarks", "Bookmarks: Clear Bookmarks", func(d *Doc, _ map[string]any) { d.bookmarks = nil })
	text("select_all_bookmarks", "Bookmarks: Select All Bookmarks", func(d *Doc, _ map[string]any) {
		var sels []buffer.Range
		for _, b := range d.bookmarks {
			sels = append(sels, buffer.Cursor(d.M.Buf.Clamp(b)))
		}
		if len(sels) > 0 {
			d.M.SetSels(sels, len(sels)-1)
		}
	})
	reg("jump_back", "Goto: Jump Back", func(map[string]any) { a.jumpBy(-1) })
	reg("jump_forward", "Goto: Jump Forward", func(map[string]any) { a.jumpBy(1) })
	reg("toggle_record_macro", "Macros: Start / Stop Recording", func(map[string]any) { a.toggleMacro() })
	reg("run_macro", "Macros: Play Macro", func(map[string]any) { a.runMacro() })

	// Plugins and panels
	reg("auto_complete", "Edit: Show Completions", func(map[string]any) {
		if d := a.doc(); d != nil {
			a.complete.show(d)
		}
	})
	reg("show_panel", "", func(args map[string]any) { a.showPanel(argString(args, "panel"), argBool(args, "toggle")) })
	reg("hide_panel", "", func(map[string]any) {
		a.output.visible = false
		a.find.visible = false
		a.focusEditor = true
	})
	a.palette = append(a.palette, PaletteEntry{Caption: "View: Show Console", Command: "show_panel", Args: map[string]any{"panel": "console", "toggle": true}})
	reg("restart_plugin_host", "Plugins: Restart Plugin Host", func(map[string]any) {
		a.py.stop()
		a.py.start()
	})
	reg("open_packages_folder", "Preferences: Browse Packages", func(map[string]any) {
		_ = os.MkdirAll(packagesDir(), 0o755)
		go platform.Open(packagesDir())
	})
	reg("new_plugin", "Plugins: New Plugin…", func(map[string]any) {
		d := a.newDoc(pluginTemplate, "")
		d.setLang("Python", a.set)
		d.Name = "untitled plugin"
		a.setStatus("Save it in Packages/User (it loads as soon as it is saved)", 6*time.Second)
	})

	// Build
	reg("build", "Build: Build", func(map[string]any) { a.output.build(false) })
	reg("build_with", "Build: Build With…", func(map[string]any) { a.output.build(true) })
	reg("cancel_build", "Build: Cancel", func(map[string]any) { a.output.cancel() })
	reg("toggle_output_panel", "Build: Toggle Output Panel", func(map[string]any) { a.output.visible = !a.output.visible })
	reg("undo_replace_in_files", "Find: Undo Replace in Files", func(map[string]any) { a.search.undoReplace() })

	// Goto & view
	reg("show_overlay_goto", "Goto: Goto Anything…", func(args map[string]any) { a.showGoto(argString(args, "text")) })
	a.palette = append(a.palette, PaletteEntry{"Goto: Goto Symbol…", "show_overlay_goto", map[string]any{"text": "@"}}, PaletteEntry{"Goto: Goto Line…", "show_overlay_goto", map[string]any{"text": ":"}})
	reg("show_overlay_command_palette", "", func(map[string]any) { a.showPalette() })
	reg("next_view", "Goto: Next File", func(map[string]any) {
		if len(a.docs) > 0 {
			a.active = (a.active + 1) % len(a.docs)
			a.focusEditor = true
		}
	})
	reg("prev_view", "Goto: Previous File", func(map[string]any) {
		if len(a.docs) > 0 {
			a.active = (a.active - 1 + len(a.docs)) % len(a.docs)
			a.focusEditor = true
		}
	})
	reg("select_by_index", "", func(args map[string]any) {
		if i := argInt(args, "index"); i < len(a.docs) {
			a.active = i
			a.focusEditor = true
		}
	})
	reg("toggle_side_bar", "View: Toggle Side Bar", func(map[string]any) { a.setSidebar(!a.sidebarVisible()) })
	reg("toggle_word_wrap", "View: Toggle Word Wrap", func(map[string]any) {
		if d := a.doc(); d != nil {
			v := !d.wordWrap(a)
			d.wrapOverride = &v
			d.reveal = true
		}
	})
	reg("toggle_minimap", "View: Toggle Minimap", func(map[string]any) { a.setSetting("draw_minimap", !a.set.Bool("draw_minimap")) })
	reg("toggle_indent_guides", "View: Toggle Indent Guides", func(map[string]any) {
		a.setSetting("draw_indent_guides", !a.set.Bool("draw_indent_guides"))
	})
	reg("increase_font_size", "Font: Increase Size", func(map[string]any) { a.setSetting("font_size", float64(a.fontSize()+1)) })
	reg("decrease_font_size", "Font: Decrease Size", func(map[string]any) { a.setSetting("font_size", float64(max(6, a.fontSize()-1))) })
	reg("escape", "", func(map[string]any) {
		switch {
		case a.complete.visible:
			a.complete.hide()
		case a.popup != nil:
			a.popup = nil
		case a.quick.visible:
			a.quick.cancel()
		case a.find.visible:
			a.find.hide()
		case a.input.visible:
			a.input.visible = false
			a.focusEditor = true
		case a.output.visible && (a.doc() == nil || len(a.doc().M.Sels) == 1):
			a.output.visible = false
		default:
			if d := a.doc(); d != nil && len(d.M.Sels) > 1 {
				r := d.M.MainSel()
				d.M.SetSels([]buffer.Range{buffer.Cursor(r.Head)}, 0)
			}
		}
	})
	reg("pick_syntax", "Set Syntax…", func(map[string]any) {
		var items []QuickItem
		for _, n := range syntax.Names() {
			items = append(items, QuickItem{Caption: n, Data: n})
		}
		a.pick(items, func(it *QuickItem) {
			if d := a.doc(); d != nil {
				d.setLang(it.Data.(string), a.set)
			}
		})
	})
	reg("pick_indentation", "Indentation: Settings…", func(map[string]any) {
		d := a.doc()
		if d == nil {
			return
		}
		var items []QuickItem
		items = append(items, QuickItem{Caption: "Indent Using Spaces", Data: "spaces"}, QuickItem{Caption: "Indent Using Tabs", Data: "tabs"})
		for n := 1; n <= 8; n++ {
			items = append(items, QuickItem{Caption: fmt.Sprintf("Tab Width: %d", n), Data: n})
		}
		a.pick(items, func(it *QuickItem) {
			switch v := it.Data.(type) {
			case string:
				d.M.UseSpaces = v == "spaces"
			case int:
				d.M.TabSize = v
			}
		})
	})
	reg("select_color_scheme", "UI: Select Color Scheme", func(map[string]any) {
		var items []QuickItem
		for _, n := range theme.Names() {
			items = append(items, QuickItem{Caption: n, Data: n})
		}
		orig := a.scheme.Name
		a.pick(items, func(it *QuickItem) { a.setSetting("color_scheme", it.Data.(string)) })
		a.quick.onMove = func(it *QuickItem, _ string) {
			a.scheme = theme.Get(it.Data.(string))
			a.chrome = theme.ChromeFor(a.scheme)
		}
		a.quick.onCancel = func() {
			a.scheme = theme.Get(orig)
			a.chrome = theme.ChromeFor(a.scheme)
		}
	})
	reg("edit_settings", "Preferences: Settings", func(map[string]any) {
		d := a.newDoc(settings.DefaultText, "")
		d.Name = "Default settings (read-only reference)"
		d.Scratch = true
		d.M.ReadOnly = true
		d.setLang("JSON", a.set)
		a.openPath(a.set.Path)
	})
	reg("edit_keybindings", "Preferences: Key Bindings", func(map[string]any) {
		a.openPath(filepath.Join(settings.UserDir(), "Default (OSX).inks-keymap"))
	})

	// Text tools
	text("sort_lines", "Text: Sort Lines", func(d *Doc, _ map[string]any) {
		d.M.ReplaceLinesOrAll(func(t string) (string, error) { return texttools.SortLines(t, "natural", false, 0, ""), nil })
	})
	tx("sort_lines_numeric", "Text: Sort Lines (Numeric)", func(t string) (string, error) { return texttools.SortLines(t, "numeric", false, 0, ""), nil })
	tx("sort_lines_reverse", "Text: Sort Lines (Reverse)", func(t string) (string, error) { return texttools.SortLines(t, "natural", true, 0, ""), nil })
	tx("sort_blocks", "Text: Sort Blocks (Terraform/HCL, JSON, CSS…)", func(t string) (string, error) { return texttools.SortBlocks(t, "", false) })
	text("sort_block_body", "Text: Sort Block Contents", func(d *Doc, _ map[string]any) { a.blockTransform(d, texttools.SortBlockBody) })
	text("align_equals", "Text: Align \"=\" (terraform fmt style)", func(d *Doc, _ map[string]any) {
		a.blockTransform(d, func(t string) string { return texttools.AlignOn(t, "=") })
	})
	tx("uniq_lines", "Text: Unique Lines", func(t string) (string, error) { return texttools.Uniq(t, false, false, false, false), nil })
	tx("uniq_count", "Text: Count Duplicate Lines (uniq -c)", func(t string) (string, error) { return texttools.Uniq(t, false, true, false, false), nil })
	tx("remove_blank_lines", "Text: Remove Blank Lines", func(t string) (string, error) { return texttools.RemoveBlankLines(t), nil })
	tx("number_lines", "Text: Number Lines", func(t string) (string, error) { return texttools.NumberLines(t), nil })
	prompt("sort_blocks_by_key", "Text: Sort Blocks by Key…", "Sort key regex (group 1):", `^\w+\s+"[^"]*"\s+"([^"]*)"`, func(t, k string) (string, error) { return texttools.SortBlocks(t, k, false) })
	prompt("cut_fields", "Text: Cut Fields… (cut)", "cut:", "-d, -f1", func(t, s string) (string, error) { return texttools.Cut(t, s), nil })
	prompt("translate_chars", "Text: Translate Characters… (tr)", "tr:", "a-z A-Z", func(t, s string) (string, error) { return texttools.Translate(t, s), nil })
	prompt("keep_lines_matching", "Text: Keep Lines Matching… (grep)", "Keep lines matching:", "", func(t, p string) (string, error) { return texttools.Grep(t, p, false, true) })
	prompt("remove_lines_matching", "Text: Remove Lines Matching… (grep -v)", "Remove lines matching:", "", func(t, p string) (string, error) { return texttools.Grep(t, p, true, true) })
	prompt("join_lines_with", "Text: Join Lines With…", "Separator:", ", ", func(t, s string) (string, error) { return texttools.JoinWith(t, s), nil })
	prompt("sed_substitute", "Text: Substitute… (sed)", "sed:", "s/old/new/g", texttools.Sed)
	prompt("filter_through_command", "Text: Filter Through Command… (awk, sed, sort, jq…)", "Filter through command:", "sort | uniq -c | sort -rn", func(t, cmd string) (string, error) {
		return a.filterThrough(cmd, t)
	})
}

func titleCase(s string) string {
	prev := ' '
	return strings.Map(func(r rune) rune {
		out := unicode.ToLower(r)
		if !unicode.IsLetter(prev) && prev != '\'' {
			out = unicode.ToUpper(r)
		}
		prev = r
		return out
	}, s)
}

func (a *App) defaultDir() string {
	if d := a.doc(); d != nil && d.Path != "" {
		return filepath.Dir(d.Path)
	}
	if len(a.folders) > 0 {
		return a.folders[0]
	}
	home, _ := os.UserHomeDir()
	return home
}

func (a *App) setSetting(key string, value any) {
	if err := a.set.Set(key, value); err != nil {
		a.setStatus(err.Error(), 0)
		return
	}
	a.reloadSettings()
}

// findUnderExpand is ⌘D: select the word, then add its next occurrence.
func (a *App) findUnderExpand(d *Doc, skip bool) {
	m := d.M
	r := m.MainSel()
	if r.Empty() {
		if s, e, ok := m.WordAt(r.Head); ok {
			sels := append([]buffer.Range(nil), m.Sels...)
			sels[m.Main] = buffer.Range{Anchor: s, Head: e, Goal: -1}
			m.SetSels(sels, m.Main)
		}
		return
	}
	word := m.Buf.Slice(r.Start(), r.End())
	text := m.Buf.Text("\n")
	start := m.Buf.Offset(r.End())
	k := strings.Index(text[start:], word)
	var from int
	if k >= 0 {
		from = start + k
	} else if k = strings.Index(text, word); k >= 0 {
		from = k
	} else {
		return
	}
	next := buffer.Range{Anchor: m.Buf.PosAt(from), Head: m.Buf.PosAt(from + len(word)), Goal: -1}
	sels := append([]buffer.Range(nil), m.Sels...)
	if skip {
		sels = append(sels[:m.Main], sels[m.Main+1:]...)
	}
	sels = append(sels, next)
	m.SetSels(sels, len(sels)-1)
}

// blockTransform applies fn to the selection, or to the block around the caret.
func (a *App) blockTransform(d *Doc, fn func(string) string) {
	m := d.M
	if !m.MainSel().Empty() {
		m.ReplaceLinesOrAll(func(t string) (string, error) { return fn(t), nil })
		return
	}
	text := m.Buf.Text("\n")
	s, e, ok := texttools.EnclosingBlock(text, m.Buf.Offset(m.MainSel().Head))
	if !ok {
		m.ReplaceLinesOrAll(func(t string) (string, error) { return fn(t), nil })
		return
	}
	out := fn(text[s:e])
	if out != text[s:e] {
		sels := m.Buf.Apply([]buffer.Edit{{Start: m.Buf.PosAt(s), End: m.Buf.PosAt(e), Text: out}}, m.Sels, buffer.ApplyOptions{Kind: "transform"})
		m.SetSels(sels, m.Main)
	}
}

func (a *App) filterThrough(cmdline, input string) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", cmdline)
	cmd.Dir = a.defaultDir()
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return input, fmt.Errorf("command failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return input, err
	}
	return string(out), nil
}

// pick shows a fuzzy list of items.
func (a *App) pick(items []QuickItem, onSelect func(*QuickItem)) {
	a.quick.show("", func(q string) []QuickItem {
		if q == "" {
			return items
		}
		var out []QuickItem
		for _, it := range items {
			if r, ok := fuzzy.Match(q, it.Caption); ok {
				it.Score, it.Match = r.Score, r.Indexes
				out = append(out, it)
			}
		}
		sortByScore(out)
		return out
	}, func(it *QuickItem, _ string) {
		if it != nil {
			onSelect(it)
		}
	})
}

func (a *App) showPalette() {
	entries := append([]PaletteEntry(nil), a.palette...)
	for _, e := range a.pkgPalette {
		if _, ok := a.commands[e.Command]; ok || a.py.commands[e.Command] != "" {
			entries = append(entries, e)
		}
	}
	a.quick.show("", func(q string) []QuickItem {
		var out []QuickItem
		for _, e := range entries {
			it := QuickItem{Caption: e.Caption, Keys: a.keymap.KeysFor(e.Command, e.Args), Data: e}
			if q != "" {
				r, ok := fuzzy.Match(q, e.Caption)
				if !ok {
					continue
				}
				it.Score, it.Match = r.Score, r.Indexes
			}
			out = append(out, it)
		}
		if q != "" {
			sortByScore(out)
		} else {
			sortByCaption(out)
		}
		return out
	}, func(it *QuickItem, _ string) {
		if it == nil {
			return
		}
		e := it.Data.(PaletteEntry)
		a.focusEditor = true
		a.run(e.Command, e.Args)
	})
}

func sortByScore(items []QuickItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Score > items[j-1].Score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func sortByCaption(items []QuickItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && strings.ToLower(items[j].Caption) < strings.ToLower(items[j-1].Caption); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

var _ = strconv.Itoa
