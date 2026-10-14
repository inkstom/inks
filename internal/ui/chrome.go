package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"inks/internal/search"
	"inks/internal/theme"
)

// clicked reports presses on tag inside the current clip area.
func clicked(gtx layout.Context, tag event.Tag) (pointer.Event, bool) {
	var hit pointer.Event
	got := false
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			hit, got = e, true
		}
	}
	return hit, got
}

// hit registers a clickable area. It reports a primary click and whether
// the pointer is over the area, and shows tip in the status bar on hover.
func (a *App) hit(gtx layout.Context, tag event.Tag, r image.Rectangle, tip string) (click, hover bool) {
	area := clip.Rect(r).Push(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()
	if a.hovered == nil {
		a.hovered = map[event.Tag]bool{}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Enter | pointer.Leave})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Buttons == pointer.ButtonPrimary {
				click = true
			}
		case pointer.Enter:
			a.hovered[tag] = true
			if tip != "" {
				a.setStatus(tip, 6*time.Second)
			}
		case pointer.Leave:
			delete(a.hovered, tag)
			if tip != "" && a.status == tip {
				a.status = ""
			}
		}
	}
	return click, a.hovered[tag]
}

// closeButton draws a panel's × at the top right corner of a panel w wide.
func (a *App) closeButton(gtx layout.Context, tag event.Tag, w int, tip string) bool {
	sz := gtx.Dp(24)
	return a.closeButtonIn(gtx, tag, image.Rect(w-sz-gtx.Dp(8), gtx.Dp(6), w-gtx.Dp(8), gtx.Dp(6)+sz), tip)
}

// closeButtonIn draws a × button in r.
func (a *App) closeButtonIn(gtx layout.Context, tag event.Tag, r image.Rectangle, tip string) bool {
	c := a.chrome
	click, hover := a.hit(gtx, tag, r, tip)
	fg := c.TabFg
	if hover {
		fillRound(gtx.Ops, r, gtx.Dp(4), c.SidebarHover)
		fg = c.TabActiveFg
	}
	// Draw the × as two strokes so it is crisp at any size.
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	arm := gtx.Dp(5)
	th := max(1, gtx.Dp(1.5))
	for i := -arm; i <= arm; i++ {
		fill(gtx.Ops, image.Rect(cx+i, cy+i, cx+i+th, cy+i+th), fg)
		fill(gtx.Ops, image.Rect(cx+i, cy-i, cx+i+th, cy-i+th), fg)
	}
	return click
}

func (a *App) uiPx(gtx layout.Context) int {
	s := a.set.Int("ui_font_size")
	if s <= 0 {
		s = 13
	}
	return gtx.Sp(unit.Sp(float32(s)))
}

// ---------------------------------------------------------------------------
// Tabs

type tabTag struct{ i int }
type tabCloseTag struct{ i int }

func (a *App) layoutTabs(gtx layout.Context) layout.Dimensions {
	c := a.chrome
	h := gtx.Dp(32)
	w := gtx.Constraints.Max.X
	fill(gtx.Ops, image.Rect(0, 0, w, h), c.TabBar)
	px := a.uiPx(gtx) - gtx.Dp(0.5)
	met := a.fonts.metrics(a.fonts.UI, px)
	tabH := gtx.Dp(27)
	grp := a.groups[a.drawGroup]
	focused := a.drawGroup == a.focus
	// Fit the tabs: shrink long titles first, then scroll so the active tab
	// stays visible.
	avail := w - gtx.Dp(12)
	maxTitle := gtx.Dp(180)
	natural := func(limit int) int {
		total := 0
		for _, d := range a.docs {
			tw := a.fonts.Width(a.fonts.Elide(d.Title(), a.fonts.UI, px, limit), a.fonts.UI, px)
			total += max(gtx.Dp(96), tw+gtx.Dp(44)) + gtx.Dp(1)
		}
		return total
	}
	for maxTitle > gtx.Dp(40) && natural(maxTitle) > avail {
		maxTitle -= gtx.Dp(20)
	}
	minTab := gtx.Dp(96)
	if natural(maxTitle) > avail {
		minTab = gtx.Dp(70)
	}
	widths := make([]int, len(a.docs))
	activeL, activeR, total := 0, 0, 0
	for i, d := range a.docs {
		tw := a.fonts.Width(a.fonts.Elide(d.Title(), a.fonts.UI, px, maxTitle), a.fonts.UI, px)
		widths[i] = max(minTab, tw+gtx.Dp(44))
		if i == a.active {
			activeL, activeR = total, total+widths[i]
		}
		total += widths[i] + gtx.Dp(1)
	}
	if activeR-grp.tabScroll > avail {
		grp.tabScroll = activeR - avail
	}
	if activeL < grp.tabScroll {
		grp.tabScroll = activeL
	}
	grp.tabScroll = min(max(grp.tabScroll, 0), max(0, total-avail))
	defer clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops).Pop()
	x := gtx.Dp(6) - grp.tabScroll
	for i, d := range a.docs {
		title := a.fonts.Elide(d.Title(), a.fonts.UI, px, maxTitle)
		tw := a.fonts.Width(title, a.fonts.UI, px)
		tabW := widths[i]
		r := image.Rect(x, h-tabH, x+tabW, h)
		bg := c.Tab
		fg := c.TabFg
		if i == a.active {
			bg = a.scheme.Bg
			fg = c.TabActiveFg
			if !focused {
				fg = c.TabFg
			}
		}
		tabFont := a.fonts.UI
		if d.preview {
			tabFont.Style = font.Italic
		}
		rr := clip.RRect{Rect: r, NW: gtx.Dp(6), NE: gtx.Dp(6)}
		paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
		a.fonts.Draw(gtx.Ops, title, x+(tabW-tw)/2-gtx.Dp(4), h-tabH/2+met.Ascent/2-gtx.Dp(1), fg, tabFont, px)
		// Close button / dirty dot.
		cx := x + tabW - gtx.Dp(18)
		cy := h - tabH/2
		cr := image.Rect(cx-gtx.Dp(7), cy-gtx.Dp(7), cx+gtx.Dp(7), cy+gtx.Dp(7))
		if d.Dirty() {
			fillRound(gtx.Ops, image.Rect(cx-gtx.Dp(3), cy-gtx.Dp(3), cx+gtx.Dp(4), cy+gtx.Dp(4)), gtx.Dp(4), fg)
		} else if i == a.active {
			a.fonts.Draw(gtx.Ops, "×", cx-gtx.Dp(4), cy+met.Ascent/2-gtx.Dp(1), fg, a.fonts.UI, px)
		}
		// Click areas.
		area := clip.Rect(r).Push(gtx.Ops)
		event.Op(gtx.Ops, &grp.tabTags[i%len(grp.tabTags)])
		area.Pop()
		carea := clip.Rect(cr).Push(gtx.Ops)
		event.Op(gtx.Ops, &grp.tabCloseTags[i%len(grp.tabCloseTags)])
		carea.Pop()
		if e, ok := clicked(gtx, &grp.tabCloseTags[i%len(grp.tabCloseTags)]); ok && e.Buttons == pointer.ButtonPrimary {
			idx := i
			if focused {
				defer a.closeDoc(idx, nil)
			} else {
				a.active = idx
				a.pressed = true
			}
		} else if e, ok := clicked(gtx, &grp.tabTags[i%len(grp.tabTags)]); ok {
			a.pressed = true
			if grp.tabClickIdx == i && gtx.Now.Sub(grp.tabClickAt) < 400*time.Millisecond {
				d.preview = false // double click keeps a preview tab
			}
			grp.tabClickIdx, grp.tabClickAt = i, gtx.Now
			if e.Buttons == pointer.ButtonTertiary {
				idx := i
				defer a.closeDoc(idx, nil)
			} else {
				a.active = i
				a.focusEditor = true
			}
		}
		x += tabW + gtx.Dp(1)
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// ---------------------------------------------------------------------------
// Side bar

type treeRow struct {
	path  string
	name  string
	isDir bool
	depth int
	root  bool
}

// Tree is the folder tree state.
type Tree struct {
	a        *App
	expanded map[string]bool
	cache    map[string][]os.DirEntry
	scroll   int
	rowTags  []rowTag

	lastClick   string
	lastClickAt time.Time
}

type rowTag struct{ i int }

func newTree(a *App) *Tree {
	return &Tree{a: a, expanded: map[string]bool{}, cache: map[string][]os.DirEntry{}}
}

func (t *Tree) entries(dir string) []os.DirEntry {
	if e, ok := t.cache[dir]; ok {
		return e
	}
	list, _ := os.ReadDir(dir)
	var out []os.DirEntry
	for _, e := range list {
		name := e.Name()
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir && matchAny(t.a.set.Strings("folder_exclude_patterns"), name) {
			continue
		}
		if !isDir && matchAny(t.a.set.Strings("file_exclude_patterns"), name) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := isDirEntry(dir, out[i]), isDirEntry(dir, out[j])
		if di != dj {
			return di
		}
		return naturalLess(out[i].Name(), out[j].Name())
	})
	t.cache[dir] = out
	return out
}

func isDirEntry(dir string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink != 0 {
		if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil {
			return st.IsDir()
		}
	}
	return false
}

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, name); ok {
			return true
		}
	}
	return false
}

func (t *Tree) rows() []treeRow {
	var out []treeRow
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, e := range t.entries(dir) {
			p := filepath.Join(dir, e.Name())
			isDir := isDirEntry(dir, e)
			out = append(out, treeRow{path: p, name: e.Name(), isDir: isDir, depth: depth})
			if isDir && t.expanded[p] {
				walk(p, depth+1)
			}
		}
	}
	for _, f := range t.a.folders {
		out = append(out, treeRow{path: f, name: filepath.Base(f), isDir: true, root: true})
		if t.expanded[f] {
			walk(f, 1)
		}
	}
	return out
}

// refresh drops cached listings so the tree re-reads the disk.
func (t *Tree) refresh() { t.cache = map[string][]os.DirEntry{} }

type sidebarScrollTag struct{}

type sideTabTag struct{ mode string }

var openFolderTag, sidebarCloseTag struct{ _ int }

var sideTabTags = [2]sideTabTag{{"folders"}, {"search"}}

func (a *App) layoutSidebar(gtx layout.Context) layout.Dimensions {
	c := a.chrome
	// Never take more than ~45% of the window, so the editor stays usable.
	w := min(gtx.Dp(unit.Dp(a.sidebarW)), gtx.Constraints.Max.X*45/100)
	h := gtx.Constraints.Max.Y
	gtx.Constraints = layout.Exact(image.Pt(w, h))
	fill(gtx.Ops, image.Rect(0, 0, w, h), c.Sidebar)
	fill(gtx.Ops, image.Rect(w-1, 0, w, h), c.Border)
	px := a.uiPx(gtx)
	met := a.fonts.metrics(a.fonts.UI, px)
	rowH := gtx.Dp(24)
	top := gtx.Dp(30)

	// View switcher: FOLDERS | SEARCH.
	x := gtx.Dp(12)
	for i, tab := range []struct{ label, mode, tip string }{
		{"FOLDERS", "folders", "Folders (⌘K ⌘B toggles the side bar)"},
		{"SEARCH", "search", "Find and replace in files (⇧⌘F)"},
	} {
		small := px - gtx.Dp(2)
		tw := a.fonts.Width(tab.label, a.fonts.UI, small)
		fg := c.SidebarDim
		if a.sideMode == tab.mode {
			fg = c.TabActiveFg
			fill(gtx.Ops, image.Rect(x, top-gtx.Dp(6), x+tw, top-gtx.Dp(4)), c.Accent)
		}
		a.fonts.Draw(gtx.Ops, tab.label, x, gtx.Dp(18), fg, a.fonts.UI, small)
		r := image.Rect(x-gtx.Dp(6), 0, x+tw+gtx.Dp(6), top)
		area := clip.Rect(r).Push(gtx.Ops)
		event.Op(gtx.Ops, &sideTabTags[i])
		area.Pop()
		if _, ok := clicked(gtx, &sideTabTags[i]); ok {
			if tab.mode == "search" {
				a.search.show()
			} else {
				a.sideMode = tab.mode
			}
		}
		x += tw + gtx.Dp(18)
	}
	fill(gtx.Ops, image.Rect(0, top-gtx.Dp(4), w-1, top-gtx.Dp(3)), c.Border)
	if a.closeButtonIn(gtx, &sidebarCloseTag, image.Rect(w-gtx.Dp(30), gtx.Dp(3), w-gtx.Dp(8), gtx.Dp(25)), "Hide the side bar (⌘K ⌘B)") {
		a.setSidebar(false)
		a.focusEditor = true
	}
	defer a.sidebarResizer(gtx, w, h) // on top of the rows
	if a.sideMode == "search" {
		g := gtx
		g.Constraints = layout.Exact(image.Pt(w-1, h))
		a.search.Layout(g, top)
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	if len(a.folders) == 0 {
		a.fonts.Draw(gtx.Ops, "No folders open", gtx.Dp(12), top+gtx.Dp(22), c.SidebarDim, a.fonts.UI, px)
		label := "Open Folder…"
		bw := a.fonts.Width(label, a.fonts.UI, px) + gtx.Dp(20)
		br := image.Rect(gtx.Dp(12), top+gtx.Dp(36), gtx.Dp(12)+bw, top+gtx.Dp(62))
		click, hover := a.hit(gtx, &openFolderTag, br, "Add a folder to the side bar (⌥⌘O)")
		bg := c.Accent
		if hover {
			bg = theme.Mix(bg, c.TabActiveFg, 0.15)
		}
		fillRound(gtx.Ops, br, gtx.Dp(5), bg)
		a.fonts.Draw(gtx.Ops, label, br.Min.X+gtx.Dp(10), br.Min.Y+gtx.Dp(18), theme.Hex("#ffffff"), a.fonts.UI, px)
		if click {
			a.run("prompt_open_folder", nil)
		}
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	t := a.tree
	rows := t.rows()
	if len(t.rowTags) < len(rows) {
		t.rowTags = make([]rowTag, len(rows)+64)
	}
	// Scrolling.
	area := clip.Rect{Min: image.Pt(0, top), Max: image.Pt(w, h)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &a.sidebarScroll)
	area.Pop()
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &a.sidebarScroll, Kinds: pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30}})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			t.scroll += int(e.Scroll.Y)
		}
	}
	maxScroll := max(0, len(rows)*rowH-(h-top))
	t.scroll = min(max(t.scroll, 0), maxScroll)
	clipRows := clip.Rect{Min: image.Pt(0, top), Max: image.Pt(w, h)}.Push(gtx.Ops)
	active := ""
	if d := a.doc(); d != nil {
		active = d.Path
	}
	for i, r := range rows {
		y := top + i*rowH - t.scroll
		if y+rowH < top || y > h {
			continue
		}
		rr := image.Rect(0, y, w-1, y+rowH)
		if r.path == active {
			fill(gtx.Ops, rr, c.SidebarSel)
		}
		x := gtx.Dp(10) + r.depth*gtx.Dp(14)
		base := y + rowH/2 + met.Ascent/2 - gtx.Dp(1)
		if r.isDir {
			arrow := "▸"
			if t.expanded[r.path] {
				arrow = "▾"
			}
			a.fonts.Draw(gtx.Ops, arrow, x, base, c.SidebarDim, a.fonts.UI, px)
		}
		name := a.fonts.Elide(r.name, a.fonts.UI, px, w-x-gtx.Dp(40))
		fg := c.SidebarFg
		if r.root {
			fg = c.TabActiveFg
		}
		badge := ""
		switch a.git.statusOf(r.path) {
		case 'M':
			fg, badge = theme.Mix(a.scheme.Modified, c.SidebarFg, 0.25), "M"
		case 'A':
			fg, badge = theme.Mix(a.scheme.Added, c.SidebarFg, 0.25), "A"
		case '?':
			fg, badge = theme.Mix(a.scheme.Added, c.SidebarFg, 0.25), "U"
		case 'D':
			fg, badge = theme.Mix(a.scheme.Deleted, c.SidebarFg, 0.25), "D"
		case 'C':
			fg, badge = a.scheme.Deleted, "!"
		}
		if r.isDir && badge != "" {
			badge = "•"
		}
		a.fonts.Draw(gtx.Ops, name, x+gtx.Dp(16), base, fg, a.fonts.UI, px)
		if badge != "" {
			bw := a.fonts.Width(badge, a.fonts.UI, px-gtx.Dp(2))
			a.fonts.Draw(gtx.Ops, badge, w-gtx.Dp(12)-bw, base, fg, a.fonts.UI, px-gtx.Dp(2))
		}
		hit := clip.Rect(rr).Push(gtx.Ops)
		tag := &t.rowTags[i]
		event.Op(gtx.Ops, tag)
		hit.Pop()
		if e, ok := clicked(gtx, tag); ok {
			if r.isDir {
				t.expanded[r.path] = !t.expanded[r.path]
			} else {
				// One click previews, a double click (or editing) keeps the tab.
				double := t.lastClick == r.path && gtx.Now.Sub(t.lastClickAt) < 400*time.Millisecond
				t.lastClick, t.lastClickAt = r.path, gtx.Now
				if double || e.Modifiers.Contain(key.ModCommand) {
					if d := a.openPath(r.path); d != nil {
						d.preview = false
					}
				} else {
					a.openPreview(r.path)
				}
			}
		}
	}
	clipRows.Pop()
	return layout.Dimensions{Size: image.Pt(w, h)}
}

type resizeTag struct{}

var sidebarResize resizeTag

// sidebarResizer lets the side bar's right edge be dragged.
func (a *App) sidebarResizer(gtx layout.Context, w, h int) {
	r := image.Rect(w-gtx.Dp(5), 0, w, h)
	area := clip.Rect(r).Push(gtx.Ops)
	pointer.CursorColResize.Add(gtx.Ops)
	event.Op(gtx.Ops, &sidebarResize)
	area.Pop()
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &sidebarResize, Kinds: pointer.Press | pointer.Drag | pointer.Release})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			a.resizing = true
		case pointer.Drag:
			if a.resizing {
				dp := int(e.Position.X / gtx.Metric.PxPerDp)
				a.sidebarW = min(max(dp, 150), 700)
			}
		case pointer.Release:
			a.resizing = false
		}
	}
}

// ---------------------------------------------------------------------------
// Status bar

type statusTag struct{ name string }

func (a *App) layoutStatus(gtx layout.Context) layout.Dimensions {
	c := a.chrome
	h := gtx.Dp(24)
	w := gtx.Constraints.Max.X
	fill(gtx.Ops, image.Rect(0, 0, w, h), c.Status)
	fill(gtx.Ops, image.Rect(0, 0, w, 1), c.Border)
	px := a.uiPx(gtx) - gtx.Dp(1)
	met := a.fonts.metrics(a.fonts.UI, px)
	base := h/2 + met.Ascent/2 - gtx.Dp(1)
	d := a.doc()
	if d == nil {
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	m := d.M
	var left string
	chars := 0
	for _, r := range m.Sels {
		chars += len([]rune(m.Buf.Slice(r.Start(), r.End())))
	}
	if len(m.Sels) > 1 {
		left = fmt.Sprintf("%d selection regions", len(m.Sels))
		if chars > 0 {
			left += fmt.Sprintf(", %d characters selected", chars)
		}
	} else {
		h := m.MainSel().Head
		col := 1
		line := m.Buf.Line(h.Line)
		col += visualCol(line, h.Col, m.TabSize)
		left = fmt.Sprintf("Line %d, Column %d", h.Line+1, col)
		if chars > 0 {
			left += fmt.Sprintf("; %d characters selected", chars)
		}
	}
	if ps := d.pluginStatus(); ps != "" {
		left += "    " + ps
	}
	if a.status != "" && gtx.Now.Before(a.statusUntil) {
		left += "    " + a.status
		gtx.Execute(op.InvalidateCmd{At: a.statusUntil})
	}
	a.fonts.Draw(gtx.Ops, a.fonts.Elide(left, a.fonts.UI, px, w/2+w/4), gtx.Dp(12), base, c.StatusFg, a.fonts.UI, px)
	// Right side: indentation and syntax (click to change).
	indent := fmt.Sprintf("Tab Size: %d", m.TabSize)
	if m.UseSpaces {
		indent = fmt.Sprintf("Spaces: %d", m.TabSize)
	}
	right := []struct{ text, cmd string }{{indent, "pick_indentation"}, {d.Lang, "pick_syntax"}}
	if br := a.gitBranchFor(d); br != "" {
		right = append([]struct{ text, cmd string }{{"⎇ " + br, ""}}, right...)
	}
	x := w - gtx.Dp(10)
	for i := len(right) - 1; i >= 0; i-- {
		tw := a.fonts.Width(right[i].text, a.fonts.UI, px)
		x -= tw + gtx.Dp(20)
		r := image.Rect(x, 0, x+tw+gtx.Dp(20), h)
		a.fonts.Draw(gtx.Ops, right[i].text, x+gtx.Dp(10), base, c.StatusFg, a.fonts.UI, px)
		area := clip.Rect(r).Push(gtx.Ops)
		tag := &a.statusTags[i]
		event.Op(gtx.Ops, tag)
		area.Pop()
		if _, ok := clicked(gtx, tag); ok && right[i].cmd != "" {
			a.run(right[i].cmd, nil)
		}
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

func visualCol(line string, col, tab int) int {
	v := 0
	for i, r := range line {
		if i >= col {
			break
		}
		if r == '\t' {
			v += tab - v%tab
		} else {
			v++
		}
	}
	return v
}

func naturalLess(a, b string) bool {
	return strings.ToLower(a) < strings.ToLower(b) || (strings.EqualFold(a, b) && a < b)
}

var _ = search.PathFilter
