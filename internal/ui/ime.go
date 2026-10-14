package ui

import (
	"fmt"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"

	"inks/internal/buffer"
)

// Input methods (dead keys like ⌥E E → é, Japanese/Chinese IMEs) talk to
// the editor through Gio's selection/snippet protocol. Positions are given
// to Gio as line*imeLine + rune column: cheap to compute and stable while a
// composition is in progress on one line.

const imeLine = 1 << 20

func imeCoord(b *buffer.Buffer, p buffer.Pos) int {
	line := b.Line(p.Line)
	return p.Line*imeLine + utf8.RuneCountInString(line[:min(p.Col, len(line))])
}

func imePos(b *buffer.Buffer, c int) buffer.Pos {
	l := min(max(c/imeLine, 0), b.LineCount()-1)
	r := c - l*imeLine
	line := b.Line(l)
	col := 0
	for i := 0; i < r && col < len(line); i++ {
		_, size := utf8.DecodeRuneInString(line[col:])
		col += size
	}
	return buffer.Pos{Line: l, Col: col}
}

// updateIME tells the input method where the selection and caret are.
func (a *App) updateIME(gtx layout.Context, d *Doc) {
	m := d.M
	r := m.MainSel()
	sel := key.Range{Start: imeCoord(m.Buf, r.Anchor), End: imeCoord(m.Buf, r.Head)}
	state := fmt.Sprint(m.Buf.Version(), sel, d.caretPx, a.editorTag)
	if state == d.imeState {
		return
	}
	d.imeState = state
	line := r.Head.Line
	gtx.Execute(key.SnippetCmd{Tag: a.editorTag, Snippet: key.Snippet{
		Range: key.Range{Start: line * imeLine, End: line*imeLine + utf8.RuneCountInString(m.Buf.Line(line))},
		Text:  m.Buf.Line(line),
	}})
	gtx.Execute(key.SelectionCmd{Tag: a.editorTag, Range: sel, Caret: key.Caret{
		Pos:     f32.Pt(float32(d.caretPx.X), float32(d.caretPx.Y)),
		Ascent:  float32(d.caretAscent),
		Descent: float32(d.caretDescent),
	}})
}

// imeEdit applies text from the keyboard or an input method.
func (a *App) imeEdit(d *Doc, e key.EditEvent) {
	m := d.M
	r := m.MainSel()
	head := imeCoord(m.Buf, r.Head)
	sel := key.Range{Start: imeCoord(m.Buf, r.Start()), End: imeCoord(m.Buf, r.End())}
	rng := e.Range
	if rng.Start > rng.End {
		rng.Start, rng.End = rng.End, rng.Start
	}
	// Plain typing replaces the selection: let the model handle every caret
	// (auto-pairing, multiple cursors).
	marked := rng.Start != rng.End && (rng.End == head || d.composing)
	if rng == sel || !marked || rng.Start/imeLine != r.Head.Line || rng.End/imeLine != r.Head.Line {
		if e.Text != "" || !r.Empty() {
			m.Insert(e.Text)
		}
		return
	}
	// An input method replacing text near the caret (marked text): apply the
	// same relative range at every caret.
	rs, re := rng.Start-head, rng.End-head
	var edits []buffer.Edit
	for _, s := range m.Sels {
		h := s.Head
		col := imeCoord(m.Buf, h) - h.Line*imeLine
		from := imePos(m.Buf, h.Line*imeLine+max(col+rs, 0))
		to := imePos(m.Buf, h.Line*imeLine+max(col+re, 0))
		if from.Line != h.Line || to.Line != h.Line {
			continue
		}
		edits = append(edits, buffer.Edit{Start: from, End: to, Text: e.Text})
	}
	if len(edits) == 0 {
		return
	}
	sels := make([]buffer.Range, len(m.Sels))
	for i, s := range m.Sels {
		sels[i] = buffer.Cursor(s.Head)
	}
	out := m.Buf.Apply(edits, sels, buffer.ApplyOptions{Kind: "type", Merge: true})
	m.SetSels(out, m.Main)
}

// imeSelect handles an input method moving the selection.
func (a *App) imeSelect(d *Doc, r key.Range) {
	b := d.M.Buf
	d.M.SetSels([]buffer.Range{{Anchor: imePos(b, r.Start), Head: imePos(b, r.End), Goal: -1}}, 0)
}
