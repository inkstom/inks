// Package buffer holds document text as lines and applies edits with undo.
//
// Positions are (line, byte column) pairs; columns always sit on UTF-8 rune
// boundaries. Edits replace a range with new text and are recorded with
// their inverses so they can be undone as a group.
package buffer

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Pos is a location in the buffer: zero-based line and byte column.
type Pos struct {
	Line, Col int
}

// Less reports whether p comes before q.
func (p Pos) Less(q Pos) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col)
}

// Cmp returns -1, 0 or 1.
func (p Pos) Cmp(q Pos) int {
	switch {
	case p.Less(q):
		return -1
	case q.Less(p):
		return 1
	}
	return 0
}

// Range is a span between two positions in either order.
type Range struct {
	Anchor, Head Pos
	// Goal is the preferred visual column for vertical movement (-1: none).
	Goal int
}

// Start and End return the ordered bounds of r.
func (r Range) Start() Pos {
	if r.Head.Less(r.Anchor) {
		return r.Head
	}
	return r.Anchor
}
func (r Range) End() Pos {
	if r.Head.Less(r.Anchor) {
		return r.Anchor
	}
	return r.Head
}

// Empty reports whether r is a caret.
func (r Range) Empty() bool { return r.Anchor == r.Head }

// Cursor returns an empty range at p.
func Cursor(p Pos) Range { return Range{Anchor: p, Head: p, Goal: -1} }

// Edit replaces [Start, End) with Text.
type Edit struct {
	Start, End Pos
	Text       string
}

type undoStep struct {
	inverse  []Edit // applied in order to undo
	forward  []Edit // applied in order to redo
	before   []Range
	after    []Range
	at       time.Time
	kind     string
	mergable bool
}

// Buffer is a document's text plus its undo history.
type Buffer struct {
	lines   []string
	undo    []*undoStep
	redo    []*undoStep
	version int // increments on every change
	// ChangeHooks run after every change (e.g. to invalidate highlighting).
	ChangeHooks []func(first, last int)
	// EditHooks see each replacement (old range e, new end) so positions
	// kept outside the buffer can follow edits with MapPos.
	EditHooks []func(e Edit, newEnd Pos)
}

// New creates a buffer from text ("\n" separated; "\r\n" is normalized).
func New(text string) *Buffer {
	b := &Buffer{}
	b.SetText(text)
	return b
}

// SetText replaces everything without recording undo.
func (b *Buffer) SetText(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	b.lines = strings.Split(text, "\n")
	b.version++
	b.notify(0, len(b.lines)-1)
}

// ClearHistory drops undo/redo.
func (b *Buffer) ClearHistory() {
	b.undo = nil
	b.redo = nil
}

func (b *Buffer) notify(first, last int) {
	for _, h := range b.ChangeHooks {
		h(first, last)
	}
}

// Version changes whenever the text changes.
func (b *Buffer) Version() int { return b.version }

// LineCount returns the number of lines (at least 1).
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line returns the text of line i without its newline.
func (b *Buffer) Line(i int) string {
	if i < 0 || i >= len(b.lines) {
		return ""
	}
	return b.lines[i]
}

// Text returns the whole document joined with sep.
func (b *Buffer) Text(sep string) string { return strings.Join(b.lines, sep) }

// Len returns the length in bytes counting newlines as one byte.
func (b *Buffer) Len() int {
	n := len(b.lines) - 1
	for _, l := range b.lines {
		n += len(l)
	}
	return n
}

// EndPos is the position after the last character.
func (b *Buffer) EndPos() Pos {
	last := len(b.lines) - 1
	return Pos{last, len(b.lines[last])}
}

// Clamp returns p moved inside the buffer and onto a rune boundary.
func (b *Buffer) Clamp(p Pos) Pos {
	if p.Line < 0 {
		return Pos{}
	}
	if p.Line >= len(b.lines) {
		return b.EndPos()
	}
	l := b.lines[p.Line]
	if p.Col < 0 {
		p.Col = 0
	}
	if p.Col > len(l) {
		p.Col = len(l)
	}
	for p.Col > 0 && p.Col < len(l) && !utf8.RuneStart(l[p.Col]) {
		p.Col--
	}
	return p
}

// Slice returns the text between two positions ("\n" between lines).
func (b *Buffer) Slice(from, to Pos) string {
	if to.Less(from) {
		from, to = to, from
	}
	from, to = b.Clamp(from), b.Clamp(to)
	if from.Line == to.Line {
		return b.lines[from.Line][from.Col:to.Col]
	}
	var sb strings.Builder
	sb.WriteString(b.lines[from.Line][from.Col:])
	for i := from.Line + 1; i < to.Line; i++ {
		sb.WriteByte('\n')
		sb.WriteString(b.lines[i])
	}
	sb.WriteByte('\n')
	sb.WriteString(b.lines[to.Line][:to.Col])
	return sb.String()
}

// Offset converts a position to a byte offset (newlines count as 1).
func (b *Buffer) Offset(p Pos) int {
	p = b.Clamp(p)
	n := 0
	for i := 0; i < p.Line; i++ {
		n += len(b.lines[i]) + 1
	}
	return n + p.Col
}

// PosAt converts a byte offset back into a position.
func (b *Buffer) PosAt(off int) Pos {
	if off < 0 {
		return Pos{}
	}
	for i, l := range b.lines {
		if off <= len(l) {
			return b.Clamp(Pos{i, off})
		}
		off -= len(l) + 1
	}
	return b.EndPos()
}

// EndOf returns where text ends if inserted at p.
func EndOf(p Pos, text string) Pos {
	nl := strings.Count(text, "\n")
	if nl == 0 {
		return Pos{p.Line, p.Col + len(text)}
	}
	return Pos{p.Line + nl, len(text) - strings.LastIndexByte(text, '\n') - 1}
}

// MapPos adjusts p for an edit that replaced [e.Start, e.End) with text
// ending at newEnd. Positions inside the replaced range move to its end
// when assoc > 0, its start otherwise.
func MapPos(p Pos, e Edit, newEnd Pos, assoc int) Pos {
	if p.Less(e.Start) || (p == e.Start && assoc <= 0 && e.Start != e.End) {
		return p
	}
	if p == e.Start && e.Start == e.End {
		if assoc > 0 {
			return newEnd
		}
		return p
	}
	if p.Less(e.End) {
		if assoc > 0 {
			return newEnd
		}
		return e.Start
	}
	// After the edit.
	if p.Line == e.End.Line {
		return Pos{newEnd.Line, newEnd.Col + (p.Col - e.End.Col)}
	}
	return Pos{p.Line + (newEnd.Line - e.End.Line), p.Col}
}

// replace applies one edit and returns the inverse.
func (b *Buffer) replace(e Edit) (Edit, Pos) {
	e.Start, e.End = b.Clamp(e.Start), b.Clamp(e.End)
	if e.End.Less(e.Start) {
		e.Start, e.End = e.End, e.Start
	}
	removed := b.Slice(e.Start, e.End)
	head := b.lines[e.Start.Line][:e.Start.Col]
	tail := b.lines[e.End.Line][e.End.Col:]
	newLines := strings.Split(e.Text, "\n")
	newLines[0] = head + newLines[0]
	newLines[len(newLines)-1] += tail
	out := make([]string, 0, len(b.lines)-(e.End.Line-e.Start.Line)+len(newLines)-1)
	out = append(out, b.lines[:e.Start.Line]...)
	out = append(out, newLines...)
	out = append(out, b.lines[e.End.Line+1:]...)
	b.lines = out
	newEnd := EndOf(e.Start, e.Text)
	b.version++
	last := e.Start.Line + len(newLines) - 1
	if e.End.Line > last {
		last = e.End.Line
	}
	b.notify(e.Start.Line, last)
	for _, h := range b.EditHooks {
		h(e, newEnd)
	}
	return Edit{Start: e.Start, End: newEnd, Text: removed}, newEnd
}

// ApplyOptions control undo grouping.
type ApplyOptions struct {
	Kind  string // e.g. "type", "delete", "paste"; consecutive "type" edits merge
	Merge bool   // allow merging with the previous step of the same kind
	// Assoc < 0 keeps positions at an edit's start where the text is
	// inserted; by default they move after the inserted text.
	Assoc int
}

// Apply performs edits (non-overlapping, any order) and maps sels through
// them. It returns the mapped selections. Recorded as one undo step.
func (b *Buffer) Apply(edits []Edit, sels []Range, opts ApplyOptions) []Range {
	if len(edits) == 0 {
		return sels
	}
	before := append([]Range(nil), sels...)
	// Apply from the end so earlier positions stay valid.
	order := make([]int, len(edits))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, c int) bool { return edits[order[c]].Start.Less(edits[order[a]].Start) })
	step := &undoStep{before: before, at: time.Now(), kind: opts.Kind, mergable: opts.Merge}
	out := append([]Range(nil), sels...)
	assoc := 1
	if opts.Assoc < 0 {
		assoc = -1
	}
	for _, i := range order {
		e := edits[i]
		e.Start, e.End = b.Clamp(e.Start), b.Clamp(e.End)
		if e.End.Less(e.Start) {
			e.Start, e.End = e.End, e.Start
		}
		inv, newEnd := b.replace(e)
		step.forward = append(step.forward, e)
		step.inverse = append([]Edit{inv}, step.inverse...)
		for k := range out {
			out[k].Anchor = MapPos(out[k].Anchor, e, newEnd, assoc)
			out[k].Head = MapPos(out[k].Head, e, newEnd, assoc)
		}
	}
	step.after = append([]Range(nil), out...)
	b.redo = nil
	if n := len(b.undo); n > 0 && opts.Merge {
		prev := b.undo[n-1]
		if prev.mergable && prev.kind == opts.Kind && time.Since(prev.at) < time.Second {
			prev.forward = append(prev.forward, step.forward...)
			prev.inverse = append(step.inverse, prev.inverse...)
			prev.after = step.after
			prev.at = step.at
			return out
		}
	}
	b.undo = append(b.undo, step)
	if len(b.undo) > 2000 {
		b.undo = b.undo[len(b.undo)-2000:]
	}
	return out
}

// BreakUndoGroup stops the next edit from merging with the previous one.
func (b *Buffer) BreakUndoGroup() {
	if n := len(b.undo); n > 0 {
		b.undo[n-1].mergable = false
	}
}

// CanUndo / CanRedo report history availability.
func (b *Buffer) CanUndo() bool { return len(b.undo) > 0 }
func (b *Buffer) CanRedo() bool { return len(b.redo) > 0 }

// Undo reverts the last step and returns the selections to restore.
func (b *Buffer) Undo() ([]Range, bool) {
	n := len(b.undo)
	if n == 0 {
		return nil, false
	}
	step := b.undo[n-1]
	b.undo = b.undo[:n-1]
	for _, e := range step.inverse {
		b.replace(e)
	}
	b.redo = append(b.redo, step)
	return step.before, true
}

// Redo re-applies the last undone step.
func (b *Buffer) Redo() ([]Range, bool) {
	n := len(b.redo)
	if n == 0 {
		return nil, false
	}
	step := b.redo[n-1]
	b.redo = b.redo[:n-1]
	for _, e := range step.forward {
		b.replace(e)
	}
	b.undo = append(b.undo, step)
	return step.after, true
}
