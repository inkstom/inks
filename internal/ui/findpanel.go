package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
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

// FindPanel is the find / replace widget for the active file, at the top
// right of the editor.
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
	blockTag struct{ _ int }
	origin   int // where the search started (search as you type)
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
		f.origin = d.M.Buf.Offset(r.Start())
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

// Layout draws the find widget at the top right of the editor area, whose
// size is gtx.Constraints.Max: a find field with its options, the match
// count, previous / next / all and close, and with the arrow at the left
// expanded, a replace field.
func (f *FindPanel) Layout(gtx layout.Context) {
	a := f.a
	c := a.chrome
	d := a.doc()
	if d == nil {
		return
	}
	px := a.uiPx(gtx)
	small := px - gtx.Dp(1)
	met := a.fonts.metrics(a.fonts.UI, px)
	area := gtx.Constraints.Max
	rowH := gtx.Dp(30)
	rows := 1
	if f.replace {
		rows = 2
	}
	w := min(gtx.Dp(540), area.X-gtx.Dp(28))
	w = max(w, min(gtx.Dp(380), area.X-gtx.Dp(8)))
	h := gtx.Dp(6) + rows*rowH + gtx.Dp(2)
	x0 := area.X - w - gtx.Dp(20)
	if x0 < gtx.Dp(4) {
		x0 = gtx.Dp(4)
	}
	defer op.Offset(image.Pt(x0, 0)).Push(gtx.Ops).Pop()
	box := image.Rect(0, 0, w, h)
	// A soft shadow, then the box; it takes the clicks it covers.
	for i := 1; i <= 3; i++ {
		fillRound(gtx.Ops, image.Rect(-i, 0, w+i, h+i+1), gtx.Dp(6)+i, theme.Alpha(theme.Hex("#000000"), 0.07))
	}
	fillRound(gtx.Ops, box, gtx.Dp(6), c.Panel)
	stroke(gtx.Ops, box, gtx.Dp(6), 1, c.Border)
	blocker := clip.Rect(box).Push(gtx.Ops)
	event.Op(gtx.Ops, &f.blockTag)
	blocker.Pop()
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &f.blockTag, Kinds: pointer.Press | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30}}); !ok {
			break
		}
	}

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
				return
			case ed == &f.repl && e.Modifiers.Contain(key.ModAlt):
				f.replaceAll()
			case ed == &f.repl:
				f.replaceNext()
			case e.Modifiers.Contain(key.ModAlt):
				f.findAll()
				return
			default:
				f.next(!e.Modifiers.Contain(key.ModShift))
			}
		}
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.ChangeEvent); ok && ed == &f.find {
				f.searchAsYouType()
			}
		}
	}

	pad := gtx.Dp(4)
	iconW := gtx.Dp(24)
	ctlH := gtx.Dp(24)
	rowY := func(row int) int { return gtx.Dp(4) + row*rowH + (rowH-ctlH)/2 }
	// iconButton draws a small button and reports a click.
	iconButton := func(tag event.Tag, r image.Rectangle, tip string, on, enabled bool, draw func(fg color.NRGBA)) bool {
		click, hover := a.hit(gtx, tag, r, tip)
		fg := c.TabFg
		switch {
		case on:
			fillRound(gtx.Ops, r, gtx.Dp(4), c.OverlaySel)
			stroke(gtx.Ops, r, gtx.Dp(4), 1, c.Accent)
			fg = c.TabActiveFg
		case hover && enabled:
			fillRound(gtx.Ops, r, gtx.Dp(4), c.SidebarHover)
			fg = c.TabActiveFg
		}
		if !enabled {
			fg = theme.Alpha(fg, 0.4)
		}
		draw(fg)
		return click && enabled
	}
	label := func(text string, fnt font.Font) func(r image.Rectangle) func(color.NRGBA) {
		return func(r image.Rectangle) func(color.NRGBA) {
			return func(fg color.NRGBA) {
				lw := a.fonts.Width(text, fnt, small)
				a.fonts.Draw(gtx.Ops, text, (r.Min.X+r.Max.X-lw)/2, r.Min.Y+(r.Dy()+met.Ascent)/2-gtx.Dp(2), fg, fnt, small)
			}
		}
	}

	// The arrow that shows or hides the replace row.
	ar := image.Rect(pad, rowY(0), pad+gtx.Dp(18), rowY(rows-1)+ctlH)
	if iconButton(&f.tags[5], ar, "Toggle Replace (⌥⌘F)", false, true, func(fg color.NRGBA) {
		dir := byte('r')
		if f.replace {
			dir = 'd'
		}
		chevron(gtx, (ar.Min.X+ar.Max.X)/2, rowY(0)+ctlH/2, dir, fg)
	}) {
		f.replace = !f.replace
		if f.replace {
			a.exec(key.FocusCmd{Tag: &f.repl})
		}
	}

	// Right of the find field: count, previous, next, all, close.
	ms := f.allMatches(d)
	count := ""
	if f.find.Text() != "" {
		switch {
		case !f.matcher().Valid():
			count = "Invalid"
		case len(ms) == 0:
			count = "No results"
		default:
			count = fmt.Sprintf("%d matches", len(ms))
			if i := f.current(d, ms); i >= 0 {
				count = fmt.Sprintf("%d of %d", i+1, len(ms))
			}
		}
	}
	countW := gtx.Dp(76)
	right := w - pad - iconW // close
	allX := right - gtx.Dp(32)
	nextX := allX - iconW
	prevX := nextX - iconW
	countX := prevX - countW
	fieldX := ar.Max.X + pad
	fieldR := countX - pad
	if a.closeButtonIn(gtx, &f.tags[9], image.Rect(right, rowY(0), right+iconW, rowY(0)+ctlH), "Close (Esc)") {
		f.hide()
		return
	}
	have := len(ms) > 0
	y0 := rowY(0)
	if iconButton(&f.tags[6], image.Rect(prevX, y0, prevX+iconW, y0+ctlH), "Previous match (⇧↩, ⇧⌘G)", false, have, func(fg color.NRGBA) {
		chevron(gtx, prevX+iconW/2, y0+ctlH/2, 'u', fg)
	}) {
		f.next(false)
	}
	if iconButton(&f.tags[7], image.Rect(nextX, y0, nextX+iconW, y0+ctlH), "Next match (↩, ⌘G)", false, have, func(fg color.NRGBA) {
		chevron(gtx, nextX+iconW/2, y0+ctlH/2, 'd', fg)
	}) {
		f.next(true)
	}
	allR := image.Rect(allX, y0, allX+gtx.Dp(30), y0+ctlH)
	if iconButton(&f.tags[8], allR, "Select all matches, then type to edit them all (⌥↩)", false, have, label("All", a.fonts.UI)(allR)) {
		f.findAll()
		return
	}
	cw := a.fonts.Width(count, a.fonts.UI, small)
	countFg := c.OverlayDim
	if count == "No results" || count == "Invalid" {
		countFg = a.scheme.Deleted
	}
	a.fonts.Draw(gtx.Ops, count, countX+(countW-cw)/2, y0+(ctlH+met.Ascent)/2-gtx.Dp(2), countFg, a.fonts.UI, small)

	// A text field with option toggles inside its right end.
	field := func(ed *widget.Editor, row int, hint string, toggles []int) {
		y := rowY(row)
		r := image.Rect(fieldX, y, fieldR, y+ctlH)
		fillRound(gtx.Ops, r, gtx.Dp(4), c.Input)
		border := c.Border
		if gtx.Focused(ed) {
			border = c.Accent
		}
		stroke(gtx.Ops, r, gtx.Dp(4), 1, border)
		tx := r.Max.X - gtx.Dp(2)
		for i := len(toggles) - 1; i >= 0; i-- {
			t := findToggles[toggles[i]]
			tw := max(gtx.Dp(22), a.fonts.Width(t.label, a.fonts.Mono, small)+gtx.Dp(8))
			tr := image.Rect(tx-tw, y+gtx.Dp(2), tx, y+ctlH-gtx.Dp(2))
			if iconButton(&f.tags[toggles[i]], tr, t.tip, f.opts[t.key], true, label(t.label, a.fonts.Mono)(tr)) {
				a.toggleFindOption(t.key)
			}
			tx -= tw + gtx.Dp(1)
		}
		textW := tx - r.Min.X - gtx.Dp(10)
		if ed.Len() == 0 && !gtx.Focused(ed) {
			a.fonts.Draw(gtx.Ops, hint, r.Min.X+gtx.Dp(7), y+(ctlH+met.Ascent)/2-gtx.Dp(2), c.OverlayDim, a.fonts.UI, small)
		}
		off := op.Offset(image.Pt(r.Min.X+gtx.Dp(7), y+(ctlH-met.Ascent-met.Descent)/2)).Push(gtx.Ops)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(max(textW, 1), met.Ascent+met.Descent+gtx.Dp(2)))
		ed.Layout(g, a.fonts.Shaper, a.fonts.Mono, unit.Sp(float32(a.fontSize())), colorMaterial(gtx.Ops, a.scheme.Fg), colorMaterial(gtx.Ops, a.scheme.Selection))
		off.Pop()
	}
	field(&f.find, 0, "Find", []int{1, 2, 0, 3}) // Aa, ab, .*, {…}
	if f.replace {
		field(&f.repl, 1, "Replace", []int{4}) // AB
		y := rowY(1)
		rb := func(text, tip string, x int, tag event.Tag, fn func()) int {
			bw := a.fonts.Width(text, a.fonts.UI, small) + gtx.Dp(14)
			r := image.Rect(x, y, x+bw, y+ctlH)
			if iconButton(tag, r, tip, false, have, label(text, a.fonts.UI)(r)) {
				fn()
			}
			return x + bw + gtx.Dp(3)
		}
		x := rb("Replace", "Replace this match and find the next (↩ in Replace)", countX, &f.tags[10], f.replaceNext)
		rb("All", "Replace every match (⌥↩ in Replace); ⌘Z undoes it", x, &f.tags[11], f.replaceAll)
	}
}

// chevron draws a small arrow centred on cx, cy pointing dir: 'u', 'd' or
// 'r'.
func chevron(gtx layout.Context, cx, cy int, dir byte, fg color.NRGBA) {
	arm := gtx.Dp(4)
	th := max(1, gtx.Dp(1.5))
	for i := -arm; i <= arm; i++ {
		k := arm/2 - abs(i)
		switch dir {
		case 'd':
			fill(gtx.Ops, image.Rect(cx+i, cy+k, cx+i+th, cy+k+th), fg)
		case 'u':
			fill(gtx.Ops, image.Rect(cx+i, cy-k, cx+i+th, cy-k+th), fg)
		case 'r':
			fill(gtx.Ops, image.Rect(cx+k, cy+i, cx+k+th, cy+i+th), fg)
		}
	}
}

// current returns the index of the match the main selection is on, or -1.
func (f *FindPanel) current(d *Doc, ms []search.Match) int {
	sel := d.M.MainSel()
	from, to := d.M.Buf.Offset(sel.Start()), d.M.Buf.Offset(sel.End())
	for i, m := range ms {
		if m.From == from && m.To == to {
			return i
		}
		if m.From > to {
			break
		}
	}
	return -1
}

// searchAsYouType selects the first match at or after where the search
// started, as the find text changes.
func (f *FindPanel) searchAsYouType() {
	d := f.a.doc()
	if d == nil || f.find.Text() == "" {
		return
	}
	ms := f.allMatches(d)
	if len(ms) == 0 {
		return
	}
	pick := 0
	for i, m := range ms {
		if m.From >= f.origin {
			pick = i
			break
		}
	}
	d.M.SetSels([]buffer.Range{f.toRange(d, ms[pick])}, 0)
	d.reveal = true
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
