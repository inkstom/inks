package ui

import (
	"fmt"

	"gioui.org/app"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"inks/internal/buffer"
	"inks/internal/editor"
	"inks/internal/fuzzy"
)

// Commands behind the rest of the classic macOS key bindings: brackets,
// indentation and scope selection, transpose, marks, paste history, hard
// wrap, definitions, distraction-free mode and more.

func (a *App) registerClassicCommands(reg func(name, caption string, fn func(map[string]any)), text func(name, caption string, fn func(d *Doc, args map[string]any))) {
	text("move_to_brackets", "Goto: Jump to Matching Bracket", func(d *Doc, _ map[string]any) { d.moveToBrackets() })
	text("expand_selection_to_brackets", "Selection: Expand to Brackets", func(d *Doc, _ map[string]any) { d.expandToBrackets() })
	text("expand_selection_to_indentation", "Selection: Expand to Indentation", func(d *Doc, _ map[string]any) { d.expandToIndentation() })
	text("expand_selection_to_scope", "Selection: Expand to Scope", func(d *Doc, _ map[string]any) {
		// A word first, then the brackets around it, then the next ones out.
		r := d.M.MainSel()
		if r.Empty() {
			if s, e, ok := d.M.WordAt(r.Head); ok {
				d.M.SetSels([]buffer.Range{{Anchor: s, Head: e, Goal: -1}}, 0)
				return
			}
		}
		d.expandToBrackets()
	})
	text("transpose", "Edit: Transpose", func(d *Doc, _ map[string]any) { d.transpose() })
	text("toggle_block_comment", "Edit: Toggle Block Comment", func(d *Doc, _ map[string]any) { d.M.ToggleBlockComment() })
	text("set_mark", "Selection: Set Mark", func(d *Doc, _ map[string]any) {
		d.marks = d.marks[:0]
		for _, r := range d.M.Sels {
			d.marks = append(d.marks, r.Head)
		}
		a.setStatus("Mark set", 2*time.Second)
	})
	text("select_to_mark", "Selection: Select to Mark", func(d *Doc, _ map[string]any) { d.selectToMark(false) })
	text("swap_with_mark", "Selection: Swap with Mark", func(d *Doc, _ map[string]any) { d.selectToMark(true) })
	text("clear_mark", "Selection: Clear Mark", func(d *Doc, _ map[string]any) { d.marks = nil })
	text("show_at_center", "View: Center the Caret", func(d *Doc, _ map[string]any) {
		d.reveal, d.centerAlways = true, true
	})
	text("scroll_lines", "", func(d *Doc, args map[string]any) {
		g := a.geom(a.gtx, d)
		d.scrollY -= float32(argInt(args, "amount") * g.lineH)
	})
	text("paste_and_indent", "Edit: Paste and Indent", func(d *Doc, _ map[string]any) {
		a.pasteIndent = true
		a.requestPaste(d)
	})
	reg("paste_from_history", "Edit: Paste from History", func(map[string]any) {
		d := a.doc()
		if d == nil || len(a.pasteHistory) == 0 {
			a.setStatus("Nothing copied yet", 0)
			return
		}
		var items []QuickItem
		for i := len(a.pasteHistory) - 1; i >= 0; i-- {
			t := a.pasteHistory[i]
			first := strings.TrimSpace(strings.SplitN(t, "\n", 2)[0])
			detail := ""
			if n := strings.Count(t, "\n"); n > 0 {
				detail = fmt.Sprintf("%d more lines", n)
			}
			items = append(items, QuickItem{Caption: first, Detail: detail, Data: t})
		}
		a.pick(items, func(it *QuickItem) {
			d.M.Insert(it.Data.(string))
			a.afterEdit(d)
		})
	})
	text("wrap_lines", "Edit: Wrap Paragraph at Ruler", func(d *Doc, args map[string]any) {
		width := argInt(args, "width")
		if width <= 0 {
			if r := a.set.Ints("rulers"); len(r) > 0 {
				width = r[0]
			} else if w := a.set.Int("wrap_width"); w > 0 {
				width = w
			} else {
				width = 80
			}
		}
		d.wrapParagraphs(width)
	})
	reg("goto_definition", "Goto: Goto Definition", func(map[string]any) { a.gotoDefinition() })
	reg("goto_symbol_in_project", "Goto: Goto Symbol in Project…", func(map[string]any) { a.projectSymbols("", nil) })
	reg("switch_file", "Goto: Switch File (header ↔ source)", func(map[string]any) { a.switchFile() })
	reg("prompt_select_workspace", "Project: Switch Folder…", func(map[string]any) {
		var items []QuickItem
		for _, f := range a.recentFolders {
			items = append(items, QuickItem{Caption: filepath.Base(f), Detail: tildify(f), Data: f})
		}
		if len(items) == 0 {
			a.setStatus("No recent folders yet (File › Open Folder…)", 0)
			return
		}
		a.pick(items, func(it *QuickItem) { a.openFolder(it.Data.(string)) })
	})
	reg("toggle_distraction_free", "View: Toggle Distraction Free Mode", func(map[string]any) {
		a.distractionFree = !a.distractionFree
		mode := app.Windowed
		if a.distractionFree {
			mode = app.Fullscreen
		}
		go a.win.Option(mode.Option())
	})
	text("show_scope_name", "Developer: Show Scope Name", func(d *Doc, _ map[string]any) {
		h := d.M.MainSel().Head
		a.popup = &popup{doc: d, pos: h, text: strings.TrimSpace(scopeAt(d, h))}
	})
	text("find_under", "Find: Find Under Caret", func(d *Doc, _ map[string]any) { a.findUnder(d, true) })
	text("find_under_prev", "Find: Find Under Caret (Previous)", func(d *Doc, _ map[string]any) { a.findUnder(d, false) })
	text("slurp_replace_string", "Find: Use Selection for Replace", func(d *Doc, _ map[string]any) {
		r := d.M.MainSel()
		if t := d.M.Buf.Slice(r.Start(), r.End()); t != "" {
			a.find.repl.SetText(t)
			a.setStatus("Replace string set to \""+t+"\"", 2*time.Second)
		}
	})
	reg("replace_next", "Find: Replace Next", func(map[string]any) { a.find.replaceNext() })
}

// ---------------------------------------------------------------------------
// Brackets

const openers, closers = "([{", ")]}"

// matchBracket returns the offset of the bracket matching the one at i.
func matchBracket(text string, i int) (int, bool) {
	if i < 0 || i >= len(text) {
		return 0, false
	}
	c := text[i]
	if k := strings.IndexByte(openers, c); k >= 0 {
		depth := 0
		for j := i; j < len(text); j++ {
			switch text[j] {
			case c:
				depth++
			case closers[k]:
				depth--
				if depth == 0 {
					return j, true
				}
			}
		}
	}
	if k := strings.IndexByte(closers, c); k >= 0 {
		depth := 0
		for j := i; j >= 0; j-- {
			switch text[j] {
			case c:
				depth++
			case openers[k]:
				depth--
				if depth == 0 {
					return j, true
				}
			}
		}
	}
	return 0, false
}

// enclosing returns the innermost bracket pair around [from, to).
func enclosing(text string, from, to int) (int, int, bool) {
	var stack []int
	for i := 0; i < from && i < len(text); i++ {
		if strings.IndexByte(openers, text[i]) >= 0 {
			stack = append(stack, i)
		} else if strings.IndexByte(closers, text[i]) >= 0 && len(stack) > 0 {
			stack = stack[:len(stack)-1]
		}
	}
	for k := len(stack) - 1; k >= 0; k-- {
		if end, ok := matchBracket(text, stack[k]); ok && end >= to {
			return stack[k], end, true
		}
	}
	return 0, 0, false
}

func (d *Doc) moveToBrackets() {
	b := d.M.Buf
	text := b.Text("\n")
	var sels []buffer.Range
	for _, r := range d.M.Sels {
		off := b.Offset(r.Head)
		target := -1
		switch {
		case off < len(text) && strings.IndexByte(openers+closers, text[off]) >= 0:
			target, _ = matchBracket(text, off)
		case off > 0 && strings.IndexByte(openers+closers, text[off-1]) >= 0:
			if m, ok := matchBracket(text, off-1); ok {
				target = m
			}
		default:
			if _, e, ok := enclosing(text, off, off); ok {
				target = e
			}
		}
		if target < 0 {
			sels = append(sels, r)
			continue
		}
		sels = append(sels, buffer.Cursor(b.PosAt(target)))
	}
	d.M.SetSels(sels, d.M.Main)
	d.reveal = true
}

func (d *Doc) expandToBrackets() {
	b := d.M.Buf
	text := b.Text("\n")
	var sels []buffer.Range
	for _, r := range d.M.Sels {
		from, to := b.Offset(r.Start()), b.Offset(r.End())
		s, e, ok := enclosing(text, from, to)
		if !ok {
			sels = append(sels, r)
			continue
		}
		if from == s+1 && to == e {
			// The inside was selected: include the brackets.
			sels = append(sels, buffer.Range{Anchor: b.PosAt(s), Head: b.PosAt(e + 1), Goal: -1})
			continue
		}
		if from == s && to == e+1 {
			if s2, e2, ok := enclosing(text, s, e+1); ok {
				sels = append(sels, buffer.Range{Anchor: b.PosAt(s2 + 1), Head: b.PosAt(e2), Goal: -1})
				continue
			}
		}
		sels = append(sels, buffer.Range{Anchor: b.PosAt(s + 1), Head: b.PosAt(e), Goal: -1})
	}
	d.M.SetSels(sels, d.M.Main)
}

// expandToIndentation selects the lines around the caret indented at least
// as much as the caret line.
func (d *Doc) expandToIndentation() {
	b := d.M.Buf
	tab := d.M.TabSize
	var sels []buffer.Range
	for _, r := range d.M.Sels {
		l := r.Head.Line
		base := indentWidth(b.Line(l), tab)
		blank := func(k int) bool { return strings.TrimSpace(b.Line(k)) == "" }
		first, last := l, l
		for first > 0 && (blank(first-1) || indentWidth(b.Line(first-1), tab) >= base) {
			first--
		}
		for last+1 < b.LineCount() && (blank(last+1) || indentWidth(b.Line(last+1), tab) >= base) {
			last++
		}
		for first < l && blank(first) {
			first++
		}
		for last > l && blank(last) {
			last--
		}
		sels = append(sels, buffer.Range{Anchor: buffer.Pos{Line: first}, Head: buffer.Pos{Line: last, Col: len(b.Line(last))}, Goal: -1})
	}
	d.M.SetSels(sels, d.M.Main)
}

// ---------------------------------------------------------------------------
// Transpose and marks

func (d *Doc) transpose() {
	m := d.M
	b := m.Buf
	if len(m.Sels) > 1 && !allCarets(m) {
		// Rotate the selected texts.
		texts := make([]string, len(m.Sels))
		for i, r := range m.Sels {
			texts[i] = b.Slice(r.Start(), r.End())
		}
		edits := make([]buffer.Edit, len(m.Sels))
		for i, r := range m.Sels {
			edits[i] = buffer.Edit{Start: r.Start(), End: r.End(), Text: texts[(i+len(texts)-1)%len(texts)]}
		}
		m.SetSels(b.Apply(edits, m.Sels, buffer.ApplyOptions{Kind: "transpose"}), m.Main)
		return
	}
	var edits []buffer.Edit
	var sels []buffer.Range
	for _, r := range m.Sels {
		line := b.Line(r.Head.Line)
		c := r.Head.Col
		if c >= len(line) && c > 0 {
			_, size := utf8.DecodeLastRuneInString(line[:c])
			c -= size // at the end of a line: swap the last two characters
		}
		if c <= 0 || c >= len(line) {
			sels = append(sels, r)
			continue
		}
		pr, ps := utf8.DecodeLastRuneInString(line[:c])
		nr, ns := utf8.DecodeRuneInString(line[c:])
		edits = append(edits, buffer.Edit{Start: buffer.Pos{Line: r.Head.Line, Col: c - ps}, End: buffer.Pos{Line: r.Head.Line, Col: c + ns}, Text: string(nr) + string(pr)})
		sels = append(sels, buffer.Cursor(buffer.Pos{Line: r.Head.Line, Col: c + ns}))
	}
	if len(edits) > 0 {
		b.Apply(edits, nil, buffer.ApplyOptions{Kind: "transpose"})
		m.SetSels(sels, m.Main)
	}
}

func (d *Doc) selectToMark(swap bool) {
	if len(d.marks) == 0 {
		return
	}
	m := d.M
	var sels []buffer.Range
	for i, r := range m.Sels {
		mk := d.marks[min(i, len(d.marks)-1)]
		mk = m.Buf.Clamp(mk)
		if swap {
			sels = append(sels, buffer.Cursor(mk))
			d.marks[min(i, len(d.marks)-1)] = r.Head
		} else {
			sels = append(sels, buffer.Range{Anchor: mk, Head: r.Head, Goal: -1})
		}
	}
	m.SetSels(sels, m.Main)
	d.reveal = true
}

// ---------------------------------------------------------------------------
// Paste helpers

// reindent moves pasted text to the indentation of the line it lands on.
func reindent(text, indent string) string {
	lines := strings.Split(text, "\n")
	common := -1
	for i, l := range lines {
		if i == 0 || strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	lines[0] = strings.TrimLeft(lines[0], " \t")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = indent + lines[i][max(common, 0):]
	}
	return strings.Join(lines, "\n")
}

// rememberCopy keeps recent copies for Paste from History.
func (s *Shared) rememberCopy(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	for i, t := range s.pasteHistory {
		if t == text {
			s.pasteHistory = append(s.pasteHistory[:i], s.pasteHistory[i+1:]...)
			break
		}
	}
	s.pasteHistory = append(s.pasteHistory, text)
	if len(s.pasteHistory) > 15 {
		s.pasteHistory = s.pasteHistory[1:]
	}
}

// ---------------------------------------------------------------------------
// Hard wrap

var wrapPrefix = []string{"// ", "# ", "-- ", "; ", "* ", "> ", "//", "#"}

// wrapParagraphs re-flows the selected lines (or the paragraph at the
// caret) to width columns, keeping indentation and comment markers.
func (d *Doc) wrapParagraphs(width int) {
	m := d.M
	b := m.Buf
	r := m.MainSel()
	first, last := r.Start().Line, r.End().Line
	if r.Empty() {
		blank := func(k int) bool { return strings.TrimSpace(b.Line(k)) == "" }
		for first > 0 && !blank(first-1) {
			first--
		}
		for last+1 < b.LineCount() && !blank(last+1) {
			last++
		}
	}
	line := b.Line(first)
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	prefix := indent
	rest := strings.TrimLeft(line, " \t")
	for _, p := range wrapPrefix {
		if strings.HasPrefix(rest, p) {
			prefix = indent + p
			break
		}
	}
	var words []string
	for l := first; l <= last; l++ {
		t := strings.TrimPrefix(strings.TrimLeft(b.Line(l), " \t"), strings.TrimLeft(prefix, " \t"))
		words = append(words, strings.Fields(t)...)
	}
	if len(words) == 0 {
		return
	}
	limit := width - visualCol(prefix, len(prefix), m.TabSize)
	var out []string
	cur := ""
	for _, w := range words {
		if cur != "" && utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > limit {
			out = append(out, prefix+cur)
			cur = w
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	out = append(out, prefix+cur)
	end := buffer.Pos{Line: last, Col: len(b.Line(last))}
	sels := b.Apply([]buffer.Edit{{Start: buffer.Pos{Line: first}, End: end, Text: strings.Join(out, "\n")}}, m.Sels, buffer.ApplyOptions{Kind: "wrap"})
	m.SetSels(sels, m.Main)
}

// ---------------------------------------------------------------------------
// Definitions and project symbols

type projectSymbol struct {
	name, path string
	line, col  int
}

// projectSymbols lists definitions in the project's files (in the
// background), then shows them; with want set, it jumps straight to an
// exact match when there is just one.
func (a *App) projectSymbols(want string, done func(found bool)) {
	files := a.projectFiles()
	open := map[string]*Doc{}
	for _, d := range a.allDocs() {
		if d.Path != "" {
			open[d.Path] = d
		}
	}
	if len(files) == 0 {
		for p := range open {
			files = append(files, p)
		}
	}
	// Snapshot open documents (they may have unsaved changes).
	texts := map[string][]string{}
	for p, d := range open {
		lines := make([]string, d.M.Buf.LineCount())
		for i := range lines {
			lines[i] = d.M.Buf.Line(i)
		}
		texts[p] = lines
	}
	a.setStatus("Indexing symbols…", 10*time.Second)
	go func() {
		var syms []projectSymbol
		for i, p := range files {
			if i > 5000 || len(syms) > 100000 {
				break
			}
			lines, ok := texts[p]
			if !ok {
				st, err := os.Stat(p)
				if err != nil || st.Size() > 2<<20 {
					continue
				}
				data, err := os.ReadFile(p)
				if err != nil || !utf8.Valid(data) {
					continue
				}
				lines = strings.Split(string(data), "\n")
			}
			for l, line := range lines {
				if m := symbolRe.FindStringSubmatchIndex(line); m != nil {
					name := line[m[4]:m[5]]
					if m[6] >= 0 {
						name += "." + line[m[6]:m[7]]
					}
					syms = append(syms, projectSymbol{name: name, path: p, line: l, col: m[4]})
				}
			}
		}
		a.Do(func() {
			a.setStatus("", time.Millisecond)
			var matches []projectSymbol
			for _, s := range syms {
				base := s.name
				if i := strings.LastIndex(base, "."); i >= 0 && want != "" && !strings.Contains(want, ".") {
					base = base[i+1:]
				}
				if want != "" && (base == want || s.name == want) {
					matches = append(matches, s)
				}
			}
			jump := func(s projectSymbol) {
				d := a.openPath(s.path)
				if d != nil {
					d.M.SetSels([]buffer.Range{buffer.Cursor(d.M.Buf.Clamp(buffer.Pos{Line: s.line, Col: s.col}))}, 0)
					d.reveal, d.centerOnReveal = true, true
				}
			}
			if want != "" && len(matches) == 1 {
				jump(matches[0])
				if done != nil {
					done(true)
				}
				return
			}
			list := syms
			if want != "" {
				list = matches
				if len(list) == 0 {
					if done != nil {
						done(false)
					}
					return
				}
			}
			a.showSymbolPicker(list, want, jump)
			if done != nil {
				done(true)
			}
		})
	}()
}

func (a *App) showSymbolPicker(syms []projectSymbol, initial string, jump func(projectSymbol)) {
	a.quick.show(initial, func(q string) []QuickItem {
		var items []QuickItem
		for i, s := range syms {
			it := QuickItem{Caption: s.name, Detail: fmt.Sprintf("%s:%d", a.relPath(s.path), s.line+1), Data: i}
			if q != "" {
				r, ok := fuzzy.Match(q, s.name)
				if !ok {
					continue
				}
				it.Score, it.Match = r.Score, r.Indexes
			}
			items = append(items, it)
			if q == "" && len(items) > 2000 {
				break
			}
		}
		if q != "" {
			sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
		}
		if len(items) > 300 {
			items = items[:300]
		}
		return items
	}, func(it *QuickItem, _ string) {
		if it != nil {
			jump(syms[it.Data.(int)])
		}
	})
}

// gotoDefinition jumps to the definition of the word under the caret: in
// this file first, then across the project.
func (a *App) gotoDefinition() {
	d := a.doc()
	if d == nil {
		return
	}
	r := d.M.MainSel()
	s, e := r.Start(), r.End()
	if r.Empty() {
		var ok bool
		if s, e, ok = d.M.WordAt(r.Head); !ok {
			return
		}
	}
	word := d.M.Buf.Slice(s, e)
	for _, it := range a.symbolItems(d, "") {
		name := it.Caption
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if name == word || it.Caption == word {
			rg := it.Data.(buffer.Range)
			if rg.Head.Line != s.Line {
				d.M.SetSels([]buffer.Range{rg}, 0)
				d.reveal, d.centerOnReveal = true, true
				return
			}
		}
	}
	a.projectSymbols(word, func(found bool) {
		if !found {
			a.setStatus("No definition found for "+word, 0)
		}
	})
}

// switchFile toggles between a header and its source file (foo.h ↔ foo.c).
func (a *App) switchFile() {
	d := a.doc()
	if d == nil || d.Path == "" {
		return
	}
	ext := filepath.Ext(d.Path)
	base := strings.TrimSuffix(d.Path, ext)
	pairs := map[string][]string{
		".h": {".c", ".cc", ".cpp", ".cxx", ".m", ".mm"}, ".hpp": {".cpp", ".cc", ".cxx"},
		".c": {".h"}, ".cc": {".h", ".hh", ".hpp"}, ".cpp": {".h", ".hpp"}, ".cxx": {".h", ".hpp"},
		".m": {".h"}, ".mm": {".h"}, ".hh": {".cc"},
		".go": {"_test.go"}, ".ts": {".spec.ts", ".test.ts"}, ".js": {".test.js", ".spec.js"},
	}
	if strings.HasSuffix(d.Path, "_test.go") {
		a.openIfExists(strings.TrimSuffix(d.Path, "_test.go") + ".go")
		return
	}
	for _, alt := range pairs[ext] {
		if strings.HasPrefix(alt, "_") || strings.Count(alt, ".") > 1 {
			if a.openIfExists(base + alt) {
				return
			}
			continue
		}
		if a.openIfExists(base + alt) {
			return
		}
	}
	a.setStatus("No matching file for "+filepath.Base(d.Path), 0)
}

func (a *App) openIfExists(p string) bool {
	if _, err := os.Stat(p); err != nil {
		return false
	}
	a.openPath(p)
	return true
}

// findUnder selects the next (or previous) occurrence of the selection or
// the word under the caret, as a single selection.
func (a *App) findUnder(d *Doc, forward bool) {
	m := d.M
	r := m.MainSel()
	s, e := r.Start(), r.End()
	if r.Empty() {
		var ok bool
		if s, e, ok = m.WordAt(r.Head); !ok {
			return
		}
	}
	word := m.Buf.Slice(s, e)
	text := m.Buf.Text("\n")
	from, to := m.Buf.Offset(s), m.Buf.Offset(e)
	at := -1
	if forward {
		if k := strings.Index(text[to:], word); k >= 0 {
			at = to + k
		} else {
			at = strings.Index(text, word)
		}
	} else {
		if k := strings.LastIndex(text[:from], word); k >= 0 {
			at = k
		} else {
			at = strings.LastIndex(text, word)
		}
	}
	if at < 0 {
		return
	}
	m.SetSels([]buffer.Range{{Anchor: m.Buf.PosAt(at), Head: m.Buf.PosAt(at + len(word)), Goal: -1}}, 0)
	a.find.find.SetText(word)
	d.reveal = true
}

var _ = editor.VisualCol
