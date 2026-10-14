package ui

import (
	"image"
	"image/color"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"inks/internal/buffer"
	"inks/internal/editor"
	"inks/internal/linediff"
	"inks/internal/syntax"
	"inks/internal/theme"
)

func ioCopy(w io.Writer, r io.Reader) (int64, error) { return io.Copy(w, r) }

func syntaxDetect(path, first string) string { return syntax.Detect(path, first) }

func systemClose() system.Action { return system.ActionClose }

var _ = app.Title

// editorGeom is the layout of the editor for the current frame.
type editorGeom struct {
	met     Metrics
	lineH   int
	gutterW int
	padL    int
	miniW   int // minimap width (0 = hidden)
	rect    image.Rectangle
	rows    *rowLayout
	wrap    bool
}

func (a *App) geom(gtx layout.Context, d *Doc) editorGeom {
	px := gtx.Sp(unit.Sp(float32(a.fontSize())))
	met := a.fonts.metrics(a.fonts.Mono, px)
	lh := a.set.Float("line_height")
	if lh < 1 {
		lh = 1.45
	}
	g := editorGeom{met: met, lineH: int(math.Round(float64(px) * lh)), padL: gtx.Dp(8)}
	if a.set.Bool("line_numbers") && !a.distractionFree {
		digits := len(strconv.Itoa(d.M.Buf.LineCount()))
		if digits < 3 {
			digits = 3
		}
		g.gutterW = int(float32(digits+2)*met.Advance) + gtx.Dp(12)
	}
	size := gtx.Constraints.Max
	if a.editorRect.Dx() > 0 && size.X == 0 {
		size = a.editorRect.Size()
	}
	if a.set.Bool("draw_minimap") && size.X > gtx.Dp(500) && !a.distractionFree {
		g.miniW = gtx.Dp(100)
	}
	if a.distractionFree {
		// Center a column of text about 100 characters wide.
		g.padL = max(gtx.Dp(24), (size.X-int(100*met.Advance))/2)
	}
	g.rect = image.Rectangle{Max: size}
	g.wrap = d.wordWrap(a)
	cols := 0
	if g.wrap {
		cols = int(float32(size.X-g.textX()-g.miniW-gtx.Dp(14)) / met.Advance)
		if w := a.set.Int("wrap_width"); w > 0 && w < cols {
			cols = w
		}
		cols = max(cols, 20)
	}
	g.rows = d.layoutRows(cols, d.M.TabSize, g.wrap)
	return g
}

func (a *App) fontSize() int {
	if s := a.set.Int("font_size"); s > 0 {
		return s
	}
	return 13
}

func (g editorGeom) textX() int { return g.gutterW + g.padL }

// textRight is where the text area ends (the minimap starts).
func (g editorGeom) textRight() int { return g.rect.Dx() - g.miniW }

// wordWrap resolves the word_wrap setting ("auto" wraps prose syntaxes).
func (d *Doc) wordWrap(a *App) bool {
	if d.wrapOverride != nil {
		return *d.wrapOverride
	}
	switch v := a.set.Get("word_wrap").(type) {
	case bool:
		return v
	case string:
		if v == "auto" {
			switch strings.ToLower(d.Lang) {
			case "markdown", "plain text", "plaintext", "restructuredtext", "org", "tex", "latex", "asciidoc":
				return true
			}
		}
	}
	return false
}

// xAt is the x position of byte column col on row r.
func (g editorGeom) xAt(tx int, line string, col int, r visRow, tab int) int {
	return tx + int(float32(editor.VisualCol(line, col, tab)-r.vstart)*g.met.Advance)
}

func (a *App) layoutEditor(gtx layout.Context, d *Doc) layout.Dimensions {
	size := gtx.Constraints.Max
	if a.drawGroup == a.focus {
		a.editorRect = image.Rectangle{Max: size}
	}
	d.openFoldsAtCarets()
	g := a.geom(gtx, d)
	d.Highlight()
	a.updateGitMarks(d)
	m := d.M
	s := a.scheme
	tab := m.TabSize

	// Input: pointer and focus are attached to this area.
	area := clip.Rect(image.Rect(0, 0, g.textRight(), size.Y)).Push(gtx.Ops)
	event.Op(gtx.Ops, a.editorTag)
	key.InputHintOp{Tag: a.editorTag, Hint: key.HintAny}.Add(gtx.Ops)
	pointer.CursorText.Add(gtx.Ops)
	a.editorPointer(gtx, d, g)
	area.Pop()

	// Scrolling limits and revealing the caret.
	rows := g.rows.total()
	viewRows := size.Y / g.lineH
	maxScroll := float32((rows - 1) * g.lineH)
	if !a.set.Bool("scroll_past_end") {
		maxScroll = float32(rows*g.lineH - size.Y)
	}
	if d.reveal {
		d.reveal = false
		defer func() { d.centerOnReveal = false }()
		h := m.MainSel().Head
		if d.revealAt != nil {
			h = m.Buf.Clamp(*d.revealAt)
			d.revealAt = nil
		}
		row, segFrom := g.rows.rowOf(h)
		top := float32(row * g.lineH)
		if d.centerAlways {
			d.centerAlways = false
			d.scrollY = top - float32(size.Y)/2 + float32(g.lineH)/2
		} else if d.centerOnReveal && (top < d.scrollY || top+float32(g.lineH) > d.scrollY+float32(size.Y)) {
			d.scrollY = top - float32(size.Y)/3
		} else if top < d.scrollY {
			d.scrollY = top
		} else if top+float32(g.lineH) > d.scrollY+float32(size.Y) {
			d.scrollY = top + float32(g.lineH) - float32(size.Y)
		}
		if !g.wrap {
			line := m.Buf.Line(h.Line)
			x := float32(editor.VisualCol(line, h.Col, tab)-editor.VisualCol(line, segFrom, tab)) * g.met.Advance
			visW := float32(g.textRight() - g.textX() - gtx.Dp(20))
			if x < d.scrollX {
				d.scrollX = x
			} else if x > d.scrollX+visW {
				d.scrollX = x - visW
			}
		}
	}
	d.scrollY = min(d.scrollY, maxScroll)
	d.scrollY = max(d.scrollY, 0)
	if g.wrap || d.scrollX < 0 {
		d.scrollX = 0
	}

	// Everything below is clipped to the editor area.
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	firstRow := int(d.scrollY) / g.lineH
	offY := -(int(d.scrollY) % g.lineH)
	vis := g.rows.visible(m.Buf, firstRow, viewRows+2, g.lineH, offY, tab)
	baseline := (g.lineH-(g.met.Ascent+g.met.Descent))/2 + g.met.Ascent
	firstLine, lastLine := 0, -1
	if len(vis) > 0 {
		firstLine, lastLine = vis[0].line, vis[len(vis)-1].line
	}

	// Text area (clipped, scrolled horizontally).
	textClip := clip.Rect{Min: image.Pt(g.gutterW, 0), Max: image.Pt(g.textRight(), size.Y)}.Push(gtx.Ops)
	tx := g.textX() - int(d.scrollX)
	if a.set.Bool("highlight_line") {
		h := m.MainSel().Head
		for _, r := range vis {
			if r.line == h.Line {
				fill(gtx.Ops, image.Rect(g.gutterW, r.y, g.textRight(), r.y+g.lineH), s.LineHighlight)
			}
		}
	}
	for _, col := range a.set.Ints("rulers") {
		x := tx + int(float32(col)*g.met.Advance)
		fill(gtx.Ops, image.Rect(x, 0, x+1, size.Y), s.Guide)
	}
	if a.set.Bool("draw_indent_guides") {
		a.drawIndentGuides(gtx, d, g, vis, tx)
	}
	// rangeOnRows paints [st, en) on each visible row it covers.
	rangeOnRows := func(st, en buffer.Pos, paint func(rr image.Rectangle, eol bool)) {
		if en.Line < firstLine || st.Line > lastLine {
			return
		}
		for _, r := range vis {
			if r.line < st.Line || r.line > en.Line {
				continue
			}
			c0, c1 := r.from, r.to
			if r.line == st.Line {
				c0 = max(c0, st.Col)
			}
			if r.line == en.Line {
				c1 = min(c1, en.Col)
			}
			eol := r.lastOfLine && r.line < en.Line
			if c0 > c1 || (c0 == c1 && !eol && !(c0 == r.to && !r.lastOfLine && r.line < en.Line)) {
				continue
			}
			line := m.Buf.Line(r.line)
			x0 := g.xAt(tx, line, c0, r, tab)
			x1 := g.xAt(tx, line, c1, r, tab)
			paint(image.Rect(x0, r.y, x1, r.y+g.lineH), eol)
		}
	}
	// Selections.
	for _, sel := range m.Sels {
		if sel.Empty() {
			continue
		}
		rangeOnRows(sel.Start(), sel.End(), func(rr image.Rectangle, eol bool) {
			if eol {
				rr.Max.X += int(g.met.Advance * 0.6) // the newline
			}
			fillRound(gtx.Ops, rr, gtx.Dp(2), s.Selection)
		})
	}
	a.drawRegions(gtx, d, rangeOnRows)
	// Find highlights.
	if a.find.visible {
		for _, fr := range a.find.visibleMatches(d, firstLine, lastLine) {
			rangeOnRows(fr.Start(), fr.End(), func(rr image.Rectangle, _ bool) {
				rr.Min.Y++
				rr.Max.Y--
				fillRound(gtx.Ops, rr, gtx.Dp(2), theme.Alpha(s.FindBg, 0.35))
				stroke(gtx.Ops, rr, gtx.Dp(2), 1, s.FindBg)
			})
		}
	}
	// Text.
	for _, r := range vis {
		a.drawRow(gtx, d, r, tx, r.y+baseline, g)
	}
	a.drawPhantoms(gtx, d, vis, tx, baseline, g)
	// Fold markers after the header line of each fold.
	if len(d.folds) > 0 {
		d.foldMarks = d.foldMarks[:0]
		for _, f := range d.folds {
			for _, r := range vis {
				if r.line != f.a.Line-1 || !r.lastOfLine {
					continue
				}
				line := m.Buf.Line(r.line)
				x := g.xAt(tx, line, len(line), r, tab) + gtx.Dp(6)
				w := int(g.met.Advance*3) + gtx.Dp(6)
				box := image.Rect(x, r.y+gtx.Dp(3), x+w, r.y+g.lineH-gtx.Dp(3))
				fillRound(gtx.Ops, box, gtx.Dp(4), theme.Alpha(s.Fg, 0.12))
				a.fonts.Draw(gtx.Ops, "⋯", x+gtx.Dp(3)+int(g.met.Advance*0.6), r.y+baseline, theme.Mix(s.Fg, s.Bg, 0.3), a.fonts.Mono, g.met.Px)
				d.foldMarks = append(d.foldMarks, foldMark{rect: box, line: f.a.Line})
			}
		}
	}
	// The main caret's position, for input method windows.
	if h := m.MainSel().Head; h.Line >= firstLine && h.Line <= lastLine {
		if row, _ := g.rows.rowOf(h); row-firstRow >= 0 && row-firstRow < len(vis) {
			r := vis[row-firstRow]
			d.caretPx = image.Pt(g.xAt(tx, m.Buf.Line(h.Line), h.Col, r, tab), r.y+baseline)
			d.caretAscent, d.caretDescent = g.met.Ascent, g.met.Descent
		}
	}
	// Text being composed by an input method is underlined.
	if d.composing {
		st, en := imePos(m.Buf, d.compose.Start), imePos(m.Buf, d.compose.End)
		if en.Less(st) {
			st, en = en, st
		}
		rangeOnRows(st, en, func(rr image.Rectangle, _ bool) {
			fill(gtx.Ops, image.Rect(rr.Min.X, rr.Max.Y-gtx.Dp(3), rr.Max.X, rr.Max.Y-gtx.Dp(2)), s.Fg)
		})
	}
	// Carets (blinking).
	if a.caretVisible(gtx, d) {
		cw := gtx.Dp(2)
		for _, sel := range m.Sels {
			h := sel.Head
			if h.Line < firstLine || h.Line > lastLine {
				continue
			}
			row, _ := g.rows.rowOf(h)
			i := row - firstRow
			if i < 0 || i >= len(vis) {
				continue
			}
			r := vis[i]
			x := g.xAt(tx, m.Buf.Line(h.Line), h.Col, r, tab)
			fill(gtx.Ops, image.Rect(x, r.y+gtx.Dp(2), x+cw, r.y+g.lineH-gtx.Dp(2)), s.Caret)
		}
	}
	textClip.Pop()

	// Gutter: line numbers and git change marks.
	if g.gutterW > 0 {
		fill(gtx.Ops, image.Rect(0, 0, g.gutterW, size.Y), s.Bg)
		selLines := map[int]bool{}
		for _, r := range m.Sels {
			selLines[r.Head.Line] = true
		}
		marks := d.gitMarks
		if len(marks) != m.Buf.LineCount() {
			marks = nil
		}
		for _, r := range vis {
			if len(marks) > r.line {
				a.drawGitMark(gtx, marks[r.line], r, g)
			}
			if r.seg == 0 && d.hasBookmark(r.line) {
				// A bookmark tab at the left edge of the gutter.
				y := r.y + g.lineH/2
				for i := 0; i < gtx.Dp(5); i++ {
					fill(gtx.Ops, image.Rect(gtx.Dp(6), y-gtx.Dp(5)+i, gtx.Dp(6)+gtx.Dp(5)-i, y-gtx.Dp(4)+i), s.Caret)
					fill(gtx.Ops, image.Rect(gtx.Dp(6), y+gtx.Dp(4)-i, gtx.Dp(6)+gtx.Dp(5)-i, y+gtx.Dp(5)-i), s.Caret)
				}
			}
			if r.lastOfLine && d.foldedAfter(r.line) {
				a.fonts.Draw(gtx.Ops, "▸", g.gutterW-gtx.Dp(10), r.y+baseline, s.GutterActive, a.fonts.UI, a.uiPx(gtx)-gtx.Dp(2))
			}
			if r.seg > 0 {
				continue
			}
			n := strconv.Itoa(r.line + 1)
			w := int(float32(len(n)) * g.met.Advance)
			c := s.Gutter
			if selLines[r.line] {
				c = s.GutterActive
			}
			a.fonts.Draw(gtx.Ops, n, g.gutterW-gtx.Dp(12)-w, r.y+baseline, c, a.fonts.Mono, g.met.Px)
		}
	}
	if a.drawGroup == a.focus {
		a.updateIME(gtx, d)
		a.drawPopup(gtx, d, g, vis, tx)
		a.complete.Layout(gtx, d, g)
	}
	// Minimap, or a scrollbar indicator without one.
	if g.miniW > 0 {
		a.layoutMinimap(gtx, d, g, firstRow, viewRows, maxScroll)
	} else if total := float32(rows*g.lineH) + float32(size.Y); rows > viewRows {
		h := float32(size.Y) * float32(size.Y) / total
		h = max(h, float32(gtx.Dp(24)))
		top := (d.scrollY / (maxScroll + float32(size.Y))) * float32(size.Y)
		r := image.Rect(size.X-gtx.Dp(7), int(top), size.X-gtx.Dp(2), int(top+h))
		fillRound(gtx.Ops, r, gtx.Dp(3), theme.Alpha(s.Fg, 0.15))
	}
	return layout.Dimensions{Size: size}
}

func (a *App) drawGitMark(gtx layout.Context, mk linediff.Mark, r visRow, g editorGeom) {
	s := a.scheme
	x := gtx.Dp(2)
	switch mk {
	case linediff.Added:
		fill(gtx.Ops, image.Rect(x, r.y, x+gtx.Dp(3), r.y+g.lineH), s.Added)
	case linediff.Modified:
		fill(gtx.Ops, image.Rect(x, r.y, x+gtx.Dp(3), r.y+g.lineH), s.Modified)
	case linediff.Deleted:
		if r.seg == 0 {
			// A small wedge at the top edge: lines were removed above.
			for i := 0; i < gtx.Dp(4); i++ {
				fill(gtx.Ops, image.Rect(x, r.y-gtx.Dp(2)+i, x+gtx.Dp(5)-i, r.y-gtx.Dp(1)+i), s.Deleted)
			}
		}
	}
}

// drawIndentGuides draws faint vertical lines at each indent level.
func (a *App) drawIndentGuides(gtx layout.Context, d *Doc, g editorGeom, vis []visRow, tx int) {
	tab := max(1, d.M.TabSize)
	c := theme.Alpha(a.scheme.Guide, 0.8)
	indentOf := func(l int) int {
		line := d.M.Buf.Line(l)
		v := 0
		for _, ch := range line {
			switch ch {
			case ' ':
				v++
			case '\t':
				v += tab - v%tab
			default:
				return v
			}
		}
		return -1 // blank
	}
	for _, r := range vis {
		if r.seg > 0 {
			continue
		}
		ind := indentOf(r.line)
		if ind < 0 {
			// Blank lines continue the guides of the surrounding code.
			prev, next := -1, -1
			for l := r.line - 1; l >= 0 && l > r.line-50; l-- {
				if prev = indentOf(l); prev >= 0 {
					break
				}
			}
			for l := r.line + 1; l < d.M.Buf.LineCount() && l < r.line+50; l++ {
				if next = indentOf(l); next >= 0 {
					break
				}
			}
			ind = min(prev, next)
		}
		for v := tab; v < ind; v += tab {
			x := tx + int(float32(v)*g.met.Advance)
			fill(gtx.Ops, image.Rect(x, r.y, x+1, r.y+g.lineH), c)
		}
	}
}

func (a *App) caretVisible(gtx layout.Context, d *Doc) bool {
	if !a.set.Bool("caret_blink") {
		return true
	}
	const period = 1060 * time.Millisecond
	el := gtx.Now.Sub(d.blink)
	phase := el % period
	next := gtx.Now.Add(period/2 - phase%(period/2))
	gtx.Execute(op.InvalidateCmd{At: next})
	return phase < period/2 || el < 600*time.Millisecond
}

// drawRow paints one row's text with syntax colors.
func (a *App) drawRow(gtx layout.Context, d *Doc, r visRow, x0, y int, g editorGeom) {
	line := d.M.Buf.Line(r.line)
	if r.from >= r.to {
		return
	}
	spans := d.hl.Line(r.line)
	s := a.scheme
	tab := d.M.TabSize
	maxX := g.textRight()
	// Paint segments between span boundaries, splitting at tabs.
	draw := func(from, to int, col color.NRGBA) {
		from, to = max(from, r.from), min(to, r.to)
		for from < to {
			seg := line[from:to]
			if i := strings.IndexByte(seg, '\t'); i >= 0 {
				seg = seg[:i]
			}
			if seg != "" {
				x := x0 + int(float32(editor.VisualCol(line, from, tab)-r.vstart)*g.met.Advance)
				if x < maxX {
					w := int(float32(utf8.RuneCountInString(seg)) * g.met.Advance)
					if x+w > g.gutterW {
						a.fonts.Draw(gtx.Ops, seg, x, y, col, a.fonts.Mono, g.met.Px)
					}
				}
			}
			from += len(seg)
			if from < to && line[from] == '\t' {
				from++
			}
		}
	}
	pos := r.from
	for _, sp := range spans {
		if sp.Start >= r.to {
			break
		}
		if sp.End <= pos {
			continue
		}
		if sp.Start > pos {
			draw(pos, sp.Start, s.Fg)
		}
		end := min(sp.End, r.to)
		draw(max(sp.Start, pos), end, s.Token(sp.Role))
		pos = max(pos, end)
	}
	if pos < r.to {
		draw(pos, r.to, s.Fg)
	}
}

// ---------------------------------------------------------------------------
// Pointer input

func (a *App) posAt(d *Doc, g editorGeom, p f32.Point) buffer.Pos {
	row := int((p.Y + d.scrollY) / float32(g.lineH))
	if row < 0 {
		return buffer.Pos{}
	}
	if row >= g.rows.total() {
		return d.M.Buf.EndPos()
	}
	l, k := g.rows.lineOfRow(row)
	line := d.M.Buf.Line(l)
	from, to := g.rows.rowSpan(line, l, k)
	tab := d.M.TabSize
	x := p.X - float32(g.textX()) + d.scrollX
	vcol := editor.VisualCol(line, from, tab) + int(math.Round(float64(x/g.met.Advance)))
	col := editor.ColForVisual(line, max(vcol, 0), tab)
	if k < len(g.rows.segments(l))-1 && col >= to {
		_, size := utf8.DecodeLastRuneInString(line[:to])
		col = to - size
	}
	return buffer.Pos{Line: l, Col: min(max(col, from), to)}
}

func (a *App) editorPointer(gtx layout.Context, d *Doc, g editorGeom) {
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target:  a.editorTag,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll,
			ScrollY: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30},
			ScrollX: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30},
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		m := d.M
		switch e.Kind {
		case pointer.Scroll:
			d.scrollY += e.Scroll.Y
			d.scrollX += e.Scroll.X
			if e.Modifiers.Contain(key.ModShift) && e.Scroll.X == 0 {
				d.scrollX += e.Scroll.Y
				d.scrollY -= e.Scroll.Y
			}
		case pointer.Press:
			a.pressed = true
			a.complete.hide()
			a.popup = nil
			gtx.Execute(key.FocusCmd{Tag: a.editorTag})
			a.quick.hide()
			p := a.posAt(d, g, e.Position)
			now := gtx.Now
			if now.Sub(d.lastClick) < 400*time.Millisecond {
				d.clicks++
			} else {
				d.clicks = 1
			}
			d.lastClick = now
			d.blink = now
			switch {
			case d.clickFold(e.Position):
				d.clicks = 0
				continue
			case e.Modifiers.Contain(key.ModCommand):
				// ⌘-click adds a caret.
				m.SetSels(append(append([]buffer.Range(nil), m.Sels...), buffer.Cursor(p)), len(m.Sels))
			case e.Modifiers.Contain(key.ModShift):
				r := m.MainSel()
				m.SetSels([]buffer.Range{{Anchor: r.Anchor, Head: p, Goal: -1}}, 0)
			case d.clicks == 2:
				if s, en, ok := m.WordAt(p); ok {
					m.SetSels([]buffer.Range{{Anchor: s, Head: en, Goal: -1}}, 0)
				}
			case d.clicks >= 3:
				end := buffer.Pos{Line: p.Line + 1}
				if p.Line+1 >= m.Buf.LineCount() {
					end = buffer.Pos{Line: p.Line, Col: len(m.Buf.Line(p.Line))}
				}
				m.SetSels([]buffer.Range{{Anchor: buffer.Pos{Line: p.Line}, Head: end, Goal: -1}}, 0)
			default:
				m.SetSels([]buffer.Range{buffer.Cursor(p)}, 0)
			}
			d.dragging = true
			d.dragFrom = m.MainSel().Anchor
			m.Buf.BreakUndoGroup()
		case pointer.Drag:
			if !d.dragging {
				continue
			}
			p := a.posAt(d, g, e.Position)
			idx := m.Main
			sels := append([]buffer.Range(nil), m.Sels...)
			if idx < len(sels) {
				if e.Modifiers.Contain(key.ModAlt) {
					// ⌥-drag: column selection.
					a.columnSelect(d, d.dragFrom, p)
					break
				}
				sels[idx] = buffer.Range{Anchor: d.dragFrom, Head: p, Goal: -1}
				m.Sels, m.Main = sels, idx
			}
			// Auto-scroll when dragging past the edges.
			if e.Position.Y < 0 {
				d.scrollY -= float32(g.lineH)
			} else if e.Position.Y > float32(g.rect.Dy()) {
				d.scrollY += float32(g.lineH)
			}
		case pointer.Release:
			d.dragging = false
			m.SetSels(m.Sels, m.Main)
		}
		a.win.Invalidate()
	}
}

func (a *App) columnSelect(d *Doc, from, to buffer.Pos) {
	m := d.M
	v0 := editor.VisualCol(m.Buf.Line(from.Line), from.Col, m.TabSize)
	v1 := editor.VisualCol(m.Buf.Line(to.Line), to.Col, m.TabSize)
	l0, l1 := from.Line, to.Line
	step := 1
	if l1 < l0 {
		step = -1
	}
	var sels []buffer.Range
	for l := l0; ; l += step {
		line := m.Buf.Line(l)
		sels = append(sels, buffer.Range{Anchor: buffer.Pos{Line: l, Col: editor.ColForVisual(line, v0, m.TabSize)}, Head: buffer.Pos{Line: l, Col: editor.ColForVisual(line, v1, m.TabSize)}, Goal: -1})
		if l == l1 {
			break
		}
	}
	m.Sels, m.Main = sels, len(sels)-1
}

// ---------------------------------------------------------------------------
// Editor keys (the ones without a key binding)

func (a *App) editorKey(d *Doc, e key.Event) {
	a.recordKey(e)
	m := d.M
	shift := e.Modifiers.Contain(key.ModShift)
	alt := e.Modifiers.Contain(key.ModAlt)
	cmd := e.Modifiers.Contain(key.ModCommand)
	ctrl := e.Modifiers.Contain(key.ModCtrl)
	if c := a.complete; c.visible && c.doc == d && !e.Modifiers.Contain(key.ModCommand) {
		switch e.Name {
		case key.NameUpArrow:
			c.move(-1)
			return
		case key.NameDownArrow:
			c.move(1)
			return
		case key.NameTab, key.NameReturn, key.NameEnter:
			if !shift {
				c.accept()
				return
			}
		case key.NameLeftArrow, key.NameRightArrow, key.NameHome, key.NameEnd, key.NamePageUp, key.NamePageDown:
			c.hide()
		}
	}
	page := a.editorRect.Dy() / max(1, a.geom(a.gtx, d).lineH)
	if g := a.geom(a.gtx, d); g.wrap {
		page = max(1, page*d.M.Buf.LineCount()/max(1, g.rows.total()))
	}
	move := func(mot editor.Motion) { m.Move(mot, shift, page) }
	switch e.Name {
	case key.NameLeftArrow:
		switch {
		case cmd:
			move(editor.LineStart)
		case alt:
			move(editor.WordLeft)
		case ctrl:
			move(editor.SubwordLeft)
		default:
			move(editor.CharLeft)
		}
	case key.NameRightArrow:
		switch {
		case cmd:
			move(editor.LineEnd)
		case alt:
			move(editor.WordRight)
		case ctrl:
			move(editor.SubwordRight)
		default:
			move(editor.CharRight)
		}
	case key.NameUpArrow:
		switch {
		case cmd:
			move(editor.DocStart)
		case alt || ctrl:
			move(editor.LineUp)
		default:
			a.moveRows(d, -1, shift)
		}
	case key.NameDownArrow:
		switch {
		case cmd:
			move(editor.DocEnd)
		case alt || ctrl:
			move(editor.LineDown)
		default:
			a.moveRows(d, 1, shift)
		}
	case key.NameHome:
		if cmd {
			move(editor.DocStart)
		} else {
			move(editor.LineStart)
		}
	case key.NameEnd:
		if cmd {
			move(editor.DocEnd)
		} else {
			move(editor.LineEnd)
		}
	case key.NamePageUp:
		move(editor.PageUp)
		d.scrollY -= float32(page * a.geom(a.gtx, d).lineH)
	case key.NamePageDown:
		move(editor.PageDown)
		d.scrollY += float32(page * a.geom(a.gtx, d).lineH)
	case key.NameReturn, key.NameEnter:
		m.Newline()
	case key.NameDeleteBackward:
		defer func() {
			if a.complete.visible {
				a.complete.refresh(d)
			}
		}()
		switch {
		case alt:
			m.Backspace(editor.WordLeft)
		case ctrl:
			m.Backspace(editor.SubwordLeft)
		default:
			m.Backspace(editor.CharLeft)
		}
	case key.NameDeleteForward:
		if alt {
			m.DeleteForward(editor.WordRight)
		} else {
			m.DeleteForward(editor.CharRight)
		}
	case key.NameTab:
		if !shift && allCarets(m) && a.expandSnippetAtCaret(d) {
			return
		}
		if shift {
			m.Unindent()
		} else {
			m.Tab()
		}
	default:
		return
	}
	a.afterEdit(d)
}

// moveRows moves carets by visual rows (wrapped lines count as several).
func (a *App) moveRows(d *Doc, dir int, extend bool) {
	g := a.geom(a.gtx, d)
	m := d.M
	if !g.wrap {
		if dir < 0 {
			m.Move(editor.LineUp, extend, 0)
		} else {
			m.Move(editor.LineDown, extend, 0)
		}
		return
	}
	tab := m.TabSize
	sels := make([]buffer.Range, len(m.Sels))
	for i, r := range m.Sels {
		if !extend && !r.Empty() {
			// Collapse to the edge in the direction of travel first.
			p := r.Start()
			if dir > 0 {
				p = r.End()
			}
			r = buffer.Cursor(p)
		}
		goal := r.Goal
		if goal < 0 {
			_, from := g.rows.rowOf(r.Head)
			line := m.Buf.Line(r.Head.Line)
			goal = editor.VisualCol(line, r.Head.Col, tab) - editor.VisualCol(line, from, tab)
		}
		h := g.rows.moveRows(m.Buf, r.Head, goal, dir, tab)
		anchor := h
		if extend {
			anchor = r.Anchor
		}
		sels[i] = buffer.Range{Anchor: anchor, Head: h, Goal: goal}
	}
	m.SetSels(sels, m.Main)
}

// copySelection copies the selections (whole lines when empty).
func (a *App) copySelection(d *Doc, cut bool) {
	m := d.M
	var parts []string
	allEmpty := true
	for _, r := range m.Sels {
		if !r.Empty() {
			allEmpty = false
		}
	}
	if allEmpty {
		for _, r := range m.Sels {
			parts = append(parts, m.Buf.Line(r.Head.Line)+"\n")
		}
		a.clipboardLines = true
	} else {
		for _, r := range m.Sels {
			parts = append(parts, m.Buf.Slice(r.Start(), r.End()))
		}
		a.clipboardLines = false
	}
	text := strings.Join(parts, "\n")
	if allEmpty {
		text = strings.Join(parts, "")
	}
	a.clipboardParts = parts
	a.clipboardText = text
	a.rememberCopy(text)
	a.exec(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
	if cut {
		if allEmpty {
			m.DeleteLine()
		} else {
			m.Backspace(editor.CharLeft)
		}
		a.afterEdit(d)
	}
}

// pasteText inserts clipboard text; one piece per caret when counts match.
func (a *App) pasteText(d *Doc, text string) {
	m := d.M
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if a.pasteIndent {
		// Paste and Indent: match the indentation of the line it lands on.
		a.pasteIndent = false
		line := m.Buf.Line(m.MainSel().Head.Line)
		text = reindent(text, line[:len(line)-len(strings.TrimLeft(line, " \t"))])
		m.Insert(text)
		m.Buf.BreakUndoGroup()
		a.afterEdit(d)
		return
	}
	if text == a.clipboardText && len(a.clipboardParts) == len(m.Sels) && len(m.Sels) > 1 {
		parts := a.clipboardParts
		i := 0
		edits := make([]buffer.Edit, len(m.Sels))
		for k, r := range m.Sels {
			t := strings.TrimSuffix(parts[i], "\n")
			if !a.clipboardLines {
				t = parts[i]
			}
			edits[k] = buffer.Edit{Start: r.Start(), End: r.End(), Text: t}
			i++
		}
		sels := m.Buf.Apply(edits, m.Sels, buffer.ApplyOptions{Kind: "paste"})
		for k := range sels {
			sels[k] = buffer.Cursor(sels[k].End())
		}
		m.SetSels(sels, m.Main)
	} else if text == a.clipboardText && a.clipboardLines && allCarets(m) {
		// Whole-line paste goes above the current line.
		var edits []buffer.Edit
		for _, r := range m.Sels {
			at := buffer.Pos{Line: r.Head.Line}
			edits = append(edits, buffer.Edit{Start: at, End: at, Text: text})
		}
		sels := m.Buf.Apply(edits, m.Sels, buffer.ApplyOptions{Kind: "paste"})
		m.SetSels(sels, m.Main)
	} else {
		m.Insert(text)
	}
	m.Buf.BreakUndoGroup()
	a.afterEdit(d)
}

func allCarets(m *editor.Model) bool {
	for _, r := range m.Sels {
		if !r.Empty() {
			return false
		}
	}
	return true
}

func (a *App) requestPaste(d *Doc) {
	a.pendingPaste = d
	a.exec(clipboard.ReadCmd{Tag: a.editorTag})
}

type foldMark struct {
	rect image.Rectangle
	line int
}

func (d *Doc) foldedAfter(l int) bool {
	for _, f := range d.folds {
		if f.a.Line == l+1 {
			return true
		}
	}
	return false
}

// clickFold opens a fold when its marker is clicked.
func (d *Doc) clickFold(p f32.Point) bool {
	pt := image.Pt(int(p.X), int(p.Y))
	for _, fm := range d.foldMarks {
		if pt.In(fm.rect) {
			d.unfoldLines(fm.line, fm.line)
			return true
		}
	}
	return false
}
