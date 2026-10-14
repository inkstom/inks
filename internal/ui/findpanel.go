package ui

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"inks/internal/buffer"
	"inks/internal/search"
	"inks/internal/theme"
)

type toggleDef struct {
	key, label, tip string
}

var findToggles = []toggleDef{
	{"regex", ".*", "Regular expression (⌥⌘R)"},
	{"case", "Aa", "Case sensitive (⌥⌘C)"},
	{"word", "ab", "Whole word (⌥⌘W)"},
	{"structural", "{…}", "Structural: :[x] matches balanced {…} […] (…) (⌥⌘B)"},
	{"preserve", "AB", "Preserve case when replacing (⌥⌘P)"},
}

// FindPanel is the bottom find / replace panel for the active file.
type FindPanel struct {
	a        *App
	visible  bool
	replace  bool
	find     widget.Editor
	repl     widget.Editor
	opts     map[string]bool
	cache    *search.Matcher
	cacheKey string
	matches  []search.Match
	matchVer int
	matchDoc *Doc
	tags     [16]struct{ i int }
	hover    string
}

func newFindPanel(a *App) *FindPanel {
	f := &FindPanel{a: a, opts: map[string]bool{}}
	f.find.SingleLine = true
	f.find.Submit = true
	f.repl.SingleLine = true
	f.repl.Submit = true
	return f
}

func (f *FindPanel) show(replace bool) {
	f.visible = true
	f.replace = replace
	f.a.input.visible = false
	if d := f.a.doc(); d != nil {
		r := d.M.MainSel()
		if !r.Empty() && r.Start().Line == r.End().Line {
			f.find.SetText(d.M.Buf.Slice(r.Start(), r.End()))
		}
	}
	n := len([]rune(f.find.Text()))
	f.find.SetCaret(n, 0)
	f.a.exec(key.FocusCmd{Tag: &f.find})
}

func (f *FindPanel) hide() {
	f.visible = false
	f.a.focusEditor = true
}

func (f *FindPanel) matcher() *search.Matcher {
	q := f.find.Text()
	k := fmt.Sprint(q, f.opts["regex"], f.opts["case"], f.opts["word"], f.opts["structural"])
	if f.cache == nil || f.cacheKey != k {
		f.cache = search.New(q, search.Options{Regex: f.opts["regex"], CaseSensitive: f.opts["case"], WholeWord: f.opts["word"], Structural: f.opts["structural"]})
		f.cacheKey = k
		f.matchVer = -1
	}
	return f.cache
}

// allMatches returns matches in d as buffer ranges (cached per version).
func (f *FindPanel) allMatches(d *Doc) []search.Match {
	m := f.matcher()
	if !m.Valid() {
		return nil
	}
	if f.matchDoc != d || f.matchVer != d.M.Buf.Version() {
		f.matches = m.FindAll(d.M.Buf.Text("\n"), 0, 0)
		f.matchDoc = d
		f.matchVer = d.M.Buf.Version()
	}
	return f.matches
}

func (f *FindPanel) toRange(d *Doc, m search.Match) buffer.Range {
	return buffer.Range{Anchor: d.M.Buf.PosAt(m.From), Head: d.M.Buf.PosAt(m.To), Goal: -1}
}

func (f *FindPanel) visibleMatches(d *Doc, first, last int) []buffer.Range {
	var out []buffer.Range
	for _, m := range f.allMatches(d) {
		r := f.toRange(d, m)
		if r.End().Line < first {
			continue
		}
		if r.Start().Line > last {
			break
		}
		out = append(out, r)
	}
	return out
}

// next selects the next (or previous) match after the main selection.
func (f *FindPanel) next(forward bool) {
	d := f.a.doc()
	if d == nil {
		return
	}
	ms := f.allMatches(d)
	if len(ms) == 0 {
		f.a.setStatus("Unable to find "+f.find.Text(), 0)
		return
	}
	sel := d.M.MainSel()
	from, to := d.M.Buf.Offset(sel.Start()), d.M.Buf.Offset(sel.End())
	pick := -1
	if forward {
		for i, m := range ms {
			if m.From >= to && !(m.From == from && m.To == to) {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = 0
		}
	} else {
		for i := len(ms) - 1; i >= 0; i-- {
			if ms[i].To <= from && ms[i].From < from {
				pick = i
				break
			}
		}
		if pick < 0 {
			pick = len(ms) - 1
		}
	}
	d.M.SetSels([]buffer.Range{f.toRange(d, ms[pick])}, 0)
	d.reveal = true
}

func (f *FindPanel) findAll() {
	d := f.a.doc()
	if d == nil {
		return
	}
	ms := f.allMatches(d)
	if len(ms) == 0 {
		f.a.setStatus("Unable to find "+f.find.Text(), 0)
		return
	}
	sels := make([]buffer.Range, len(ms))
	for i, m := range ms {
		sels[i] = f.toRange(d, m)
	}
	d.M.SetSels(sels, len(sels)-1)
	f.a.setStatus(fmt.Sprintf("%d matches selected — type to edit them all", len(ms)), 0)
	f.hide()
}

func (f *FindPanel) replaceNext() {
	d := f.a.doc()
	if d == nil {
		return
	}
	m := f.matcher()
	sel := d.M.MainSel()
	from, to := d.M.Buf.Offset(sel.Start()), d.M.Buf.Offset(sel.End())
	for _, mt := range f.allMatches(d) {
		if mt.From == from && mt.To == to {
			rep := m.Replacement(mt, f.repl.Text(), f.opts["preserve"])
			sels := d.M.Buf.Apply([]buffer.Edit{{Start: sel.Start(), End: sel.End(), Text: rep}}, []buffer.Range{buffer.Cursor(sel.Start())}, buffer.ApplyOptions{Kind: "replace"})
			_ = sels
			end := buffer.EndOf(sel.Start(), rep)
			d.M.SetSels([]buffer.Range{buffer.Cursor(end)}, 0)
			break
		}
	}
	f.next(true)
}

func (f *FindPanel) replaceAll() {
	d := f.a.doc()
	if d == nil {
		return
	}
	m := f.matcher()
	ms := f.allMatches(d)
	if len(ms) == 0 {
		f.a.setStatus("Unable to find "+f.find.Text(), 0)
		return
	}
	edits := make([]buffer.Edit, len(ms))
	for i, mt := range ms {
		r := f.toRange(d, mt)
		edits[i] = buffer.Edit{Start: r.Start(), End: r.End(), Text: m.Replacement(mt, f.repl.Text(), f.opts["preserve"])}
	}
	sels := d.M.Buf.Apply(edits, d.M.Sels, buffer.ApplyOptions{Kind: "replace"})
	d.M.SetSels(sels, d.M.Main)
	f.a.setStatus(fmt.Sprintf("Replaced %d occurrences — ⌘Z to undo", len(ms)), 0)
}

// Layout draws the panel.
func (f *FindPanel) Layout(gtx layout.Context) layout.Dimensions {
	a := f.a
	c := a.chrome
	px := a.uiPx(gtx)
	met := a.fonts.metrics(a.fonts.UI, px)
	rowH := gtx.Dp(30)
	rows := 1
	if f.replace {
		rows = 2
	}
	w := gtx.Constraints.Max.X
	h := gtx.Dp(10) + rows*rowH + gtx.Dp(4)
	fill(gtx.Ops, image.Rect(0, 0, w, h), c.Panel)
	fill(gtx.Ops, image.Rect(0, 0, w, 1), c.Border)

	// Keys inside the fields.
	for _, ed := range []*widget.Editor{&f.find, &f.repl} {
		for {
			ev, ok := gtx.Event(
				key.Filter{Focus: ed, Name: key.NameEscape},
				key.Filter{Focus: ed, Name: key.NameReturn, Optional: key.ModShift | key.ModAlt},
			)
			if !ok {
				break
			}
			e, ok := ev.(key.Event)
			if !ok || e.State != key.Press {
				continue
			}
			switch {
			case e.Name == key.NameEscape:
				f.hide()
				return layout.Dimensions{}
			case ed == &f.repl && e.Modifiers.Contain(key.ModAlt):
				f.replaceAll()
			case ed == &f.repl:
				f.replaceNext()
			case e.Modifiers.Contain(key.ModAlt):
				f.findAll()
				return layout.Dimensions{}
			default:
				f.next(!e.Modifiers.Contain(key.ModShift))
			}
		}
		for {
			if _, ok := ed.Update(gtx); !ok {
				break
			}
		}
	}

	// Toggles.
	x := gtx.Dp(10)
	y := gtx.Dp(8)
	for i, t := range findToggles {
		tw := max(gtx.Dp(28), a.fonts.Width(t.label, a.fonts.Mono, px-gtx.Dp(2))+gtx.Dp(12))
		r := image.Rect(x, y, x+tw, y+gtx.Dp(24))
		click, hover := a.hit(gtx, &f.tags[i], r, t.tip)
		if click {
			f.opts[t.key] = !f.opts[t.key]
			if t.key == "regex" && f.opts["regex"] {
				f.opts["structural"] = false
			}
			if t.key == "structural" && f.opts["structural"] {
				f.opts["regex"] = false
			}
		}
		fg := c.TabFg
		switch {
		case f.opts[t.key]:
			fillRound(gtx.Ops, r, gtx.Dp(4), c.OverlaySel)
			stroke(gtx.Ops, r, gtx.Dp(4), 1, c.Accent)
			fg = c.TabActiveFg
		case hover:
			fillRound(gtx.Ops, r, gtx.Dp(4), c.SidebarHover)
			fg = c.TabActiveFg
		}
		a.fonts.Draw(gtx.Ops, t.label, x+(tw-a.fonts.Width(t.label, a.fonts.Mono, px-gtx.Dp(2)))/2, y+gtx.Dp(17), fg, a.fonts.Mono, px-gtx.Dp(2))
		x += tw + gtx.Dp(2)
	}
	labelX := x + gtx.Dp(10)
	fieldX := labelX + gtx.Dp(62)
	btnW := gtx.Dp(84)
	closeW := gtx.Dp(40) // room for the close button at the right edge
	fieldW := w - fieldX - 3*btnW - 2*gtx.Dp(4) - gtx.Dp(10) - closeW

	field := func(ed *widget.Editor, label string, row int) {
		ry := y + row*rowH
		a.fonts.Draw(gtx.Ops, label, labelX, ry+gtx.Dp(17), c.TabFg, a.fonts.UI, px-gtx.Dp(1))
		r := image.Rect(fieldX, ry, fieldX+fieldW, ry+gtx.Dp(24))
		fillRound(gtx.Ops, r, gtx.Dp(4), c.Input)
		border := c.Border
		if gtx.Focused(ed) {
			border = c.Accent
		}
		stroke(gtx.Ops, r, gtx.Dp(4), 1, border)
		off := op.Offset(image.Pt(r.Min.X+gtx.Dp(7), ry+(gtx.Dp(24)-met.Ascent-met.Descent)/2)).Push(gtx.Ops)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(r.Dx()-gtx.Dp(14), met.Ascent+met.Descent+gtx.Dp(2)))
		ed.Layout(g, a.fonts.Shaper, a.fonts.Mono, unit.Sp(float32(a.fontSize())), colorMaterial(gtx.Ops, a.scheme.Fg), colorMaterial(gtx.Ops, a.scheme.Selection))
		off.Pop()
	}
	field(&f.find, "Find:", 0)
	if f.replace {
		field(&f.repl, "Replace:", 1)
	}
	// Match count.
	if d := a.doc(); d != nil {
		m := f.matcher()
		label := ""
		if f.find.Text() != "" {
			if !m.Valid() {
				label = "Invalid"
			} else {
				label = fmt.Sprintf("%d matches", len(f.allMatches(d)))
			}
		}
		lw := a.fonts.Width(label, a.fonts.UI, px-gtx.Dp(2))
		a.fonts.Draw(gtx.Ops, label, fieldX+fieldW-lw-gtx.Dp(8), y+gtx.Dp(17), c.OverlayDim, a.fonts.UI, px-gtx.Dp(2))
	}
	// Buttons.
	button := func(label, tip string, i, row, col int, fn func()) {
		bx := fieldX + fieldW + gtx.Dp(10) + col*(btnW+gtx.Dp(4))
		r := image.Rect(bx, y+row*rowH, bx+btnW, y+row*rowH+gtx.Dp(24))
		click, hover := a.hit(gtx, &f.tags[i], r, tip)
		bg := c.OverlaySel
		if i == 8 {
			bg = c.Accent
		}
		if hover {
			bg = theme.Mix(bg, c.TabActiveFg, 0.12)
		}
		fillRound(gtx.Ops, r, gtx.Dp(4), bg)
		lw := a.fonts.Width(label, a.fonts.UI, px-gtx.Dp(1))
		a.fonts.Draw(gtx.Ops, label, bx+(btnW-lw)/2, r.Min.Y+gtx.Dp(17), c.TabActiveFg, a.fonts.UI, px-gtx.Dp(1))
		if click {
			fn()
		}
	}
	button("Find", "Find the next match (↩, ⌘G)", 8, 0, 0, func() { f.next(true) })
	button("Find Prev", "Find the previous match (⇧↩, ⇧⌘G)", 9, 0, 1, func() { f.next(false) })
	button("Find All", "Select every match as a cursor, then type to edit them all (⌥↩)", 10, 0, 2, f.findAll)
	if f.replace {
		button("Replace", "Replace this match and find the next (↩ in the Replace field)", 11, 1, 0, f.replaceNext)
		button("Replace All", "Replace every match (⌥↩ in the Replace field); ⌘Z undoes it", 12, 1, 1, f.replaceAll)
	}
	// Close button.
	if a.closeButton(gtx, &f.tags[14], w, "Close the find panel (Esc)") {
		f.hide()
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// ---------------------------------------------------------------------------
// Input panel: a one-line prompt at the bottom (Filter Through Command…).

type InputPanel struct {
	a        *App
	visible  bool
	label    string
	ed       widget.Editor
	done     func(string)
	onChange func(string)
	onCancel func()
	history  map[string]string
	closeTag struct{ _ int }
}

func newInputPanel(a *App) *InputPanel {
	p := &InputPanel{a: a, history: map[string]string{}}
	p.ed.SingleLine = true
	p.ed.Submit = true
	return p
}

func (p *InputPanel) show(label, initial string, done func(string)) {
	p.visible = true
	p.a.find.visible = false
	p.label = label
	if last, ok := p.history[label]; ok {
		initial = last
	}
	p.ed.SetText(initial)
	p.ed.SetCaret(len([]rune(initial)), 0)
	p.done = done
	p.onChange, p.onCancel = nil, nil
	p.a.exec(key.FocusCmd{Tag: &p.ed})
}

func (p *InputPanel) Layout(gtx layout.Context) layout.Dimensions {
	a := p.a
	c := a.chrome
	for {
		ev, ok := gtx.Event(key.Filter{Focus: &p.ed, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			p.visible = false
			a.focusEditor = true
			if p.onCancel != nil {
				p.onCancel()
			}
			return layout.Dimensions{}
		}
	}
	for {
		ev, ok := p.ed.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.ChangeEvent); ok && p.onChange != nil {
			p.onChange(p.ed.Text())
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			text := p.ed.Text()
			p.history[p.label] = text
			p.visible = false
			a.focusEditor = true
			if p.done != nil {
				p.done(text)
			}
			return layout.Dimensions{}
		}
	}
	px := a.uiPx(gtx)
	met := a.fonts.metrics(a.fonts.UI, px)
	w := gtx.Constraints.Max.X
	h := gtx.Dp(40)
	fill(gtx.Ops, image.Rect(0, 0, w, h), c.Panel)
	fill(gtx.Ops, image.Rect(0, 0, w, 1), c.Border)
	lw := a.fonts.Draw(gtx.Ops, p.label, gtx.Dp(12), gtx.Dp(25), c.TabFg, a.fonts.UI, px-gtx.Dp(1))
	if a.closeButton(gtx, &p.closeTag, w, "Cancel (Esc)") {
		p.visible = false
		a.focusEditor = true
		if p.onCancel != nil {
			p.onCancel()
		}
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	r := image.Rect(gtx.Dp(22)+lw, gtx.Dp(8), w-gtx.Dp(48), gtx.Dp(32))
	fillRound(gtx.Ops, r, gtx.Dp(4), c.Input)
	stroke(gtx.Ops, r, gtx.Dp(4), 1, c.Accent)
	off := op.Offset(image.Pt(r.Min.X+gtx.Dp(7), r.Min.Y+(r.Dy()-met.Ascent-met.Descent)/2)).Push(gtx.Ops)
	g := gtx
	g.Constraints = layout.Exact(image.Pt(r.Dx()-gtx.Dp(14), met.Ascent+met.Descent+gtx.Dp(2)))
	p.ed.Layout(g, a.fonts.Shaper, a.fonts.Mono, unit.Sp(float32(a.fontSize())), colorMaterial(gtx.Ops, a.scheme.Fg), colorMaterial(gtx.Ops, a.scheme.Selection))
	off.Pop()
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// toggleFindOption flips a find option on the visible find panel or Search
// side bar; regex and structural search exclude each other.
func (a *App) toggleFindOption(opt string) {
	opts := a.find.opts
	inSearch := !a.find.visible && a.sideMode == "search" && a.sidebarVisible()
	if inSearch {
		opts = a.search.opts
	}
	opts[opt] = !opts[opt]
	if opt == "regex" && opts[opt] {
		opts["structural"] = false
	}
	if opt == "structural" && opts[opt] {
		opts["regex"] = false
	}
	if inSearch && opt != "preserve" {
		a.search.start()
	}
	for _, t := range findToggles {
		if t.key == opt {
			state := "off"
			if opts[opt] {
				state = "on"
			}
			a.setStatus(strings.Split(t.tip, " (")[0]+": "+state, 2*time.Second)
		}
	}
}
