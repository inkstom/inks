package ui

import (
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"inks/internal/buffer"
	"inks/internal/fuzzy"
	"inks/internal/search"
)

// QuickItem is one row in the quick panel.
type QuickItem struct {
	Caption string
	Detail  string
	Keys    string
	Match   []int // matched rune indexes in Caption
	Score   float64
	Data    any
}

// QuickPanel is the overlay used by Goto Anything, the Command Palette and pickers.
type QuickPanel struct {
	a        *App
	visible  bool
	editor   widget.Editor
	items    []QuickItem
	index    int
	scroll   int
	source   func(q string) []QuickItem
	onSelect func(it *QuickItem, q string)
	onCancel func()
	onMove   func(it *QuickItem, q string)
	lastQ    string
	rowTags  [60]struct{ i int }
}

func newQuickPanel(a *App) *QuickPanel {
	q := &QuickPanel{a: a}
	q.editor.SingleLine = true
	q.editor.Submit = true
	return q
}

func (q *QuickPanel) show(text string, source func(string) []QuickItem, onSelect func(*QuickItem, string)) {
	q.visible = true
	q.source = source
	q.onSelect = onSelect
	q.onCancel = nil
	q.onMove = nil
	q.editor.SetText(text)
	n := len([]rune(text))
	q.editor.SetCaret(n, n)
	q.index = 0
	q.scroll = 0
	q.lastQ = "\x00"
	q.a.exec(key.FocusCmd{Tag: &q.editor})
}

func (q *QuickPanel) hide() {
	if !q.visible {
		return
	}
	q.visible = false
	q.a.focusEditor = true
}

func (q *QuickPanel) cancel() {
	cb := q.onCancel
	q.hide()
	if cb != nil {
		cb()
	}
}

func (q *QuickPanel) commit() {
	if q.index < 0 || q.index >= len(q.items) {
		if q.onSelect != nil && len(q.items) == 0 {
			text := q.editor.Text()
			q.hide()
			q.onSelect(nil, text)
		}
		return
	}
	it := q.items[q.index]
	text := q.editor.Text()
	cb := q.onSelect
	q.hide()
	if cb != nil {
		cb(&it, text)
	}
}

func (q *QuickPanel) move(delta int) {
	if len(q.items) == 0 {
		return
	}
	q.index = (q.index + delta + len(q.items)) % len(q.items)
	if q.onMove != nil {
		q.onMove(&q.items[q.index], q.editor.Text())
	}
}

// Layout draws the panel centered at the top of the window.
func (q *QuickPanel) Layout(gtx layout.Context) {
	a := q.a
	c := a.chrome
	// Keys for list navigation (the editor widget keeps text keys).
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: &q.editor, Name: key.NameUpArrow},
			key.Filter{Focus: &q.editor, Name: key.NameDownArrow},
			key.Filter{Focus: &q.editor, Name: key.NamePageUp},
			key.Filter{Focus: &q.editor, Name: key.NamePageDown},
			key.Filter{Focus: &q.editor, Name: key.NameEscape},
			key.Filter{Focus: &q.editor, Name: key.NameTab},
		)
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			switch e.Name {
			case key.NameUpArrow:
				q.move(-1)
			case key.NameDownArrow:
				q.move(1)
			case key.NamePageUp:
				q.move(-10)
			case key.NamePageDown:
				q.move(10)
			case key.NameEscape:
				q.cancel()
				return
			case key.NameTab:
				q.commit()
				return
			}
		}
	}
	for {
		ev, ok := q.editor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			q.commit()
			return
		}
	}
	if text := q.editor.Text(); text != q.lastQ {
		q.lastQ = text
		q.items = q.source(text)
		q.index = 0
		q.scroll = 0
		if q.onMove != nil && len(q.items) > 0 {
			q.onMove(&q.items[0], text)
		}
	}
	if !q.visible {
		return
	}

	px := a.uiPx(gtx)
	met := a.fonts.metrics(a.fonts.UI, px)
	win := gtx.Constraints.Max
	w := min(gtx.Dp(640), win.X-gtx.Dp(40))
	x := (win.X - w) / 2
	y := gtx.Dp(6)
	inputH := gtx.Dp(32)
	rowH := gtx.Dp(28)
	twoLine := false
	for _, it := range q.items {
		if it.Detail != "" {
			twoLine = true
			break
		}
	}
	if twoLine {
		rowH = gtx.Dp(42)
	}
	visibleRows := min(len(q.items), min(14, (win.Y-gtx.Dp(120))/rowH))
	h := gtx.Dp(8)*2 + inputH + visibleRows*rowH
	if visibleRows > 0 {
		h += gtx.Dp(6)
	}
	box := image.Rect(x, y, x+w, y+h)
	// Shadow and box.
	fillRound(gtx.Ops, box.Add(image.Pt(0, gtx.Dp(4))).Inset(-gtx.Dp(2)), gtx.Dp(9), c.Shadow)
	fillRound(gtx.Ops, box, gtx.Dp(7), c.Overlay)
	stroke(gtx.Ops, box, gtx.Dp(7), 1, c.Border)
	// Clicks outside close it; inside are absorbed.
	full := clip.Rect{Max: win}.Push(gtx.Ops)
	event.Op(gtx.Ops, &q.items)
	full.Pop()
	inside := clip.Rect(box).Push(gtx.Ops)
	event.Op(gtx.Ops, &q.index)
	inside.Pop()
	if _, ok := clicked(gtx, &q.items); ok {
		q.cancel()
		return
	}
	clicked(gtx, &q.index)

	// Input.
	in := image.Rect(x+gtx.Dp(8), y+gtx.Dp(8), x+w-gtx.Dp(8), y+gtx.Dp(8)+inputH)
	fillRound(gtx.Ops, in, gtx.Dp(5), c.Input)
	stroke(gtx.Ops, in, gtx.Dp(5), 1, c.Accent)
	{
		off := op.Offset(image.Pt(in.Min.X+gtx.Dp(9), in.Min.Y+(inputH-met.Ascent-met.Descent)/2)).Push(gtx.Ops)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(in.Dx()-gtx.Dp(18), met.Ascent+met.Descent+gtx.Dp(2)))
		q.editor.Layout(g, a.fonts.Shaper, a.fonts.UI, unit.Sp(float32(a.set.Int("ui_font_size"))+0.5),
			colorMaterial(gtx.Ops, c.OverlayFg), colorMaterial(gtx.Ops, a.scheme.Selection))
		off.Pop()
	}

	// Rows.
	if q.index < q.scroll {
		q.scroll = q.index
	} else if q.index >= q.scroll+visibleRows {
		q.scroll = q.index - visibleRows + 1
	}
	listTop := in.Max.Y + gtx.Dp(6)
	for r := 0; r < visibleRows; r++ {
		i := q.scroll + r
		it := q.items[i]
		ry := listTop + r*rowH
		rr := image.Rect(x+gtx.Dp(1), ry, x+w-gtx.Dp(1), ry+rowH)
		if i == q.index {
			fill(gtx.Ops, rr, c.OverlaySel)
			fill(gtx.Ops, image.Rect(rr.Min.X, ry, rr.Min.X+gtx.Dp(2), ry+rowH), c.Accent)
		}
		tx := x + gtx.Dp(16)
		base := ry + gtx.Dp(18)
		if !twoLine {
			base = ry + rowH/2 + met.Ascent/2 - gtx.Dp(1)
		}
		q.drawHighlighted(gtx, it.Caption, it.Match, tx, base, px, w-gtx.Dp(140))
		if it.Detail != "" {
			a.fonts.Draw(gtx.Ops, a.fonts.Elide(it.Detail, a.fonts.UI, px-gtx.Dp(2), w-gtx.Dp(40)), tx, ry+gtx.Dp(34), c.OverlayDim, a.fonts.UI, px-gtx.Dp(2))
		}
		if it.Keys != "" {
			kw := a.fonts.Width(it.Keys, a.fonts.UI, px-gtx.Dp(1))
			kr := image.Rect(x+w-gtx.Dp(18)-kw-gtx.Dp(10), base-met.Ascent-gtx.Dp(2), x+w-gtx.Dp(18), base+met.Descent+gtx.Dp(2))
			stroke(gtx.Ops, kr, gtx.Dp(4), 1, c.Border)
			a.fonts.Draw(gtx.Ops, it.Keys, kr.Min.X+gtx.Dp(5), base, c.OverlayDim, a.fonts.UI, px-gtx.Dp(1))
		}
		hit := clip.Rect(rr).Push(gtx.Ops)
		tag := &q.rowTags[r]
		event.Op(gtx.Ops, tag)
		hit.Pop()
		if _, ok := clicked(gtx, tag); ok {
			q.index = i
			q.commit()
			return
		}
	}
	_ = pointer.ButtonPrimary
}

func (q *QuickPanel) drawHighlighted(gtx layout.Context, s string, match []int, x, y, px, maxW int) {
	a := q.a
	c := a.chrome
	s = a.fonts.Elide(s, a.fonts.UI, px, maxW)
	if len(match) == 0 {
		a.fonts.Draw(gtx.Ops, s, x, y, c.OverlayFg, a.fonts.UI, px)
		return
	}
	set := map[int]bool{}
	for _, i := range match {
		set[i] = true
	}
	rs := []rune(s)
	start := 0
	for start < len(rs) {
		hl := set[start]
		end := start
		for end < len(rs) && set[end] == hl {
			end++
		}
		col := c.OverlayFg
		if hl {
			col = c.Match
		}
		x += a.fonts.Draw(gtx.Ops, string(rs[start:end]), x, y, col, a.fonts.UI, px)
		start = end
	}
}

// ---------------------------------------------------------------------------
// Goto Anything

var gotoOpRe = regexp.MustCompile(`^(.*?)([@:#])(.*)$`)

func (a *App) projectFiles() []string {
	if a.fileCache != nil && time.Since(a.fileCacheAt) < 20*time.Second {
		return a.fileCache
	}
	var files []string
	excludeDir := a.set.Strings("folder_exclude_patterns")
	excludeFile := append(a.set.Strings("file_exclude_patterns"), a.set.Strings("binary_file_patterns")...)
	for _, root := range a.folders {
		// In git repositories, list files the way git sees them (honours .gitignore).
		if out, err := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z").Output(); err == nil {
			for _, rel := range strings.Split(string(out), "\x00") {
				if rel == "" || matchAny(excludeFile, filepath.Base(rel)) || inExcludedDir(rel, excludeDir) {
					continue
				}
				files = append(files, filepath.Join(root, rel))
			}
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && matchAny(excludeDir, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !matchAny(excludeFile, d.Name()) {
				files = append(files, p)
			}
			if len(files) > 60000 {
				return filepath.SkipAll
			}
			return nil
		})
	}
	a.fileCache = files
	a.fileCacheAt = time.Now()
	return files
}

func (a *App) relPath(p string) string {
	for _, f := range a.folders {
		if strings.HasPrefix(p, f+string(filepath.Separator)) {
			rel := p[len(f)+1:]
			if len(a.folders) > 1 {
				rel = filepath.Base(f) + "/" + rel
			}
			return rel
		}
	}
	return tildify(p)
}

func (a *App) showGoto(initial string) {
	origin := a.doc()
	var originSels []buffer.Range
	var originScroll float32
	if origin != nil {
		originSels = append([]buffer.Range(nil), origin.M.Sels...)
		originScroll = origin.scrollY
	}
	a.quick.show(initial, func(q string) []QuickItem {
		if m := gotoOpRe.FindStringSubmatch(q); m != nil {
			target := a.doc()
			if strings.TrimSpace(m[1]) != "" {
				files := a.gotoFiles(m[1])
				if len(files) == 0 {
					return nil
				}
				return []QuickItem{files[0]}
			}
			if target == nil {
				return nil
			}
			switch m[2] {
			case ":":
				parts := strings.SplitN(m[3], ":", 2)
				if line, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
					col := 1
					if len(parts) > 1 {
						col, _ = strconv.Atoi(parts[1])
					}
					a.gotoLine(target, line, col)
				}
				return nil
			case "@":
				return a.symbolItems(target, m[3])
			case "#":
				return a.wordItems(target, m[3])
			}
		}
		return a.gotoFiles(q)
	}, func(it *QuickItem, q string) {
		if it == nil {
			return
		}
		switch v := it.Data.(type) {
		case string:
			a.openPath(v)
			if m := gotoOpRe.FindStringSubmatch(q); m != nil && m[2] == ":" {
				if line, err := strconv.Atoi(strings.TrimSpace(m[3])); err == nil {
					a.gotoLine(a.doc(), line, 1)
				}
			}
		case *Doc:
			a.activate(v)
		case buffer.Range:
			if d := a.doc(); d != nil {
				d.M.SetSels([]buffer.Range{v}, 0)
				d.reveal = true
			}
		}
		a.focusEditor = true
	})
	a.quick.onMove = func(it *QuickItem, q string) {
		if r, ok := it.Data.(buffer.Range); ok {
			if d := a.doc(); d != nil {
				d.M.Sels = []buffer.Range{r}
				d.reveal = true
			}
		}
	}
	a.quick.onCancel = func() {
		if origin != nil && originSels != nil {
			origin.M.Sels = originSels
			origin.scrollY = originScroll
		}
	}
}

func (a *App) gotoLine(d *Doc, line, col int) {
	if d == nil {
		return
	}
	p := d.M.Buf.Clamp(buffer.Pos{Line: line - 1, Col: max(0, col-1)})
	d.M.SetSels([]buffer.Range{buffer.Cursor(p)}, 0)
	d.reveal = true
}

func (a *App) gotoFiles(q string) []QuickItem {
	q = strings.TrimSpace(q)
	var items []QuickItem
	seen := map[string]bool{}
	all := a.allDocs()
	for i := len(all) - 1; i >= 0; i-- {
		d := all[i]
		rel := d.Title()
		if d.Path != "" {
			rel = a.relPath(d.Path)
			seen[d.Path] = true
		}
		var data any = d
		if d.Path != "" {
			data = d.Path
		}
		if q == "" {
			items = append(items, QuickItem{Caption: filepath.Base(rel), Detail: rel, Data: data, Score: 1000 - float64(i)})
			continue
		}
		if r, ok := fuzzy.MatchPath(q, rel); ok {
			items = append(items, QuickItem{Caption: filepath.Base(rel), Detail: rel, Data: data, Score: r.Score + 3, Match: baseIndexes(rel, r.Indexes)})
		}
	}
	for _, p := range a.projectFiles() {
		if seen[p] {
			continue
		}
		rel := a.relPath(p)
		if q == "" {
			if len(items) < 300 {
				items = append(items, QuickItem{Caption: filepath.Base(rel), Detail: rel, Data: p, Score: -1})
			}
			continue
		}
		if r, ok := fuzzy.MatchPath(q, rel); ok {
			items = append(items, QuickItem{Caption: filepath.Base(rel), Detail: rel, Data: p, Score: r.Score, Match: baseIndexes(rel, r.Indexes)})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	if len(items) > 300 {
		items = items[:300]
	}
	return items
}

func inExcludedDir(rel string, globs []string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts[:len(parts)-1] {
		if matchAny(globs, p) {
			return true
		}
	}
	return false
}

// baseIndexes keeps the matched indexes that fall in the file name part.
func baseIndexes(rel string, idx []int) []int {
	rs := []rune(rel)
	off := len(rs) - len([]rune(filepath.Base(rel)))
	var out []int
	for _, i := range idx {
		if i >= off {
			out = append(out, i-off)
		}
	}
	return out
}

// Symbols: definitions found by a language-agnostic pattern plus Markdown headings.
var symbolRe = regexp.MustCompile(`^[ \t]*(?:export\s+)?(?:default\s+)?(?:pub(?:\([^)]*\))?\s+)?(?:public\s+|private\s+|protected\s+|static\s+|async\s+)*(func(?:\s*\([^)]*\))?|def|function\*?|class|fn|struct|interface|enum|trait|type|module|impl|resource|data|variable|output|provider|locals)\s+"?([A-Za-z_$][\w$.-]*)"?(?:\s+"([^"]+)")?`)
var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

func (a *App) symbolItems(d *Doc, q string) []QuickItem {
	q = strings.TrimSpace(q)
	var items []QuickItem
	for l := 0; l < d.M.Buf.LineCount(); l++ {
		line := d.M.Buf.Line(l)
		var name string
		col := 0
		if m := symbolRe.FindStringSubmatchIndex(line); m != nil {
			name = line[m[4]:m[5]]
			col = m[4]
			if m[6] >= 0 {
				name += "." + line[m[6]:m[7]] // Terraform: resource "type" "name"
			}
		} else if m := headingRe.FindStringSubmatch(line); m != nil && d.Lang == "markdown" || (m != nil && strings.EqualFold(d.Lang, "Markdown")) {
			name = m[2]
		}
		if name == "" {
			continue
		}
		r := buffer.Range{Anchor: buffer.Pos{Line: l, Col: col}, Head: buffer.Pos{Line: l, Col: col}, Goal: -1}
		if q == "" {
			items = append(items, QuickItem{Caption: name, Detail: "", Data: r, Score: -float64(l)})
			continue
		}
		if res, ok := fuzzy.Match(q, name); ok {
			items = append(items, QuickItem{Caption: name, Data: r, Score: res.Score, Match: res.Indexes})
		}
	}
	if q != "" {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	}
	return items
}

func (a *App) wordItems(d *Doc, q string) []QuickItem {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	m := search.New(q, search.Options{})
	text := d.M.Buf.Text("\n")
	var items []QuickItem
	for _, r := range m.FindAll(text, 0, 0) {
		p := d.M.Buf.PosAt(r.From)
		e := d.M.Buf.PosAt(r.To)
		items = append(items, QuickItem{Caption: strings.TrimSpace(d.M.Buf.Line(p.Line)), Detail: "line " + strconv.Itoa(p.Line+1), Data: buffer.Range{Anchor: p, Head: e, Goal: -1}})
		if len(items) > 300 {
			break
		}
	}
	return items
}
