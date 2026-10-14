// Package editor implements editing behaviour (selections, movement and
// text commands) independently of drawing.
package editor

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"inks/internal/buffer"
)

type Pos = buffer.Pos
type Range = buffer.Range

// Model is a buffer plus the selections of one view.
type Model struct {
	Buf          *buffer.Buffer
	Sels         []Range // sorted, non-overlapping; Main is the last added
	Main         int
	TabSize      int
	UseSpaces    bool
	AutoIndent   bool
	AutoPair     bool
	LineComment  string
	BlockComment [2]string
	ReadOnly     bool
	// selection history for soft undo (⌘U)
	selHistory [][]Range
	selFuture  [][]Range
}

// NewModel wraps a buffer with a single caret at the start.
func NewModel(b *buffer.Buffer) *Model {
	return &Model{Buf: b, Sels: []Range{buffer.Cursor(Pos{})}, TabSize: 4, AutoIndent: true, AutoPair: true}
}

// MainSel returns the primary selection.
func (m *Model) MainSel() Range {
	if m.Main < 0 || m.Main >= len(m.Sels) {
		m.Main = len(m.Sels) - 1
	}
	return m.Sels[m.Main]
}

// SetSels replaces the selections (normalizing them) and records history.
func (m *Model) SetSels(sels []Range, main int) {
	if len(sels) == 0 {
		sels = []Range{buffer.Cursor(Pos{})}
	}
	if !sameSels(sels, m.Sels) {
		m.selHistory = append(m.selHistory, append([]Range(nil), m.Sels...))
		if len(m.selHistory) > 200 {
			m.selHistory = m.selHistory[1:]
		}
		m.selFuture = nil
	}
	m.Sels, m.Main = m.normalize(sels, main)
}

func sameSels(a, b []Range) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Anchor != b[i].Anchor || a[i].Head != b[i].Head {
			return false
		}
	}
	return true
}

// normalize clamps, sorts and merges overlapping selections.
func (m *Model) normalize(sels []Range, main int) ([]Range, int) {
	type item struct {
		r    Range
		main bool
	}
	items := make([]item, len(sels))
	for i, r := range sels {
		r.Anchor = m.Buf.Clamp(r.Anchor)
		r.Head = m.Buf.Clamp(r.Head)
		items[i] = item{r, i == main}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].r.Start().Less(items[j].r.Start()) })
	out := make([]Range, 0, len(items))
	newMain := 0
	for _, it := range items {
		if n := len(out); n > 0 {
			prev := out[n-1]
			// Merge overlapping (or touching carets at the same spot).
			if it.r.Start().Less(prev.End()) || (it.r.Start() == prev.End() && (it.r.Empty() || prev.Empty())) {
				start, end := prev.Start(), prev.End()
				if end.Less(it.r.End()) {
					end = it.r.End()
				}
				merged := Range{Anchor: start, Head: end, Goal: -1}
				if prev.Head.Less(prev.Anchor) {
					merged = Range{Anchor: end, Head: start, Goal: -1}
				}
				out[n-1] = merged
				if it.main {
					newMain = n - 1
				}
				continue
			}
		}
		if it.main {
			newMain = len(out)
		}
		out = append(out, it.r)
	}
	return out, newMain
}

// SoftUndo / SoftRedo step through selection history (⌘U / ⇧⌘U).
func (m *Model) SoftUndo() bool {
	n := len(m.selHistory)
	if n == 0 {
		return false
	}
	m.selFuture = append(m.selFuture, m.Sels)
	m.Sels, m.Main = m.normalize(m.selHistory[n-1], len(m.selHistory[n-1])-1)
	m.selHistory = m.selHistory[:n-1]
	return true
}
func (m *Model) SoftRedo() bool {
	n := len(m.selFuture)
	if n == 0 {
		return false
	}
	m.selHistory = append(m.selHistory, m.Sels)
	m.Sels, m.Main = m.normalize(m.selFuture[n-1], len(m.selFuture[n-1])-1)
	m.selFuture = m.selFuture[:n-1]
	return true
}

// ---------------------------------------------------------------------------
// Visual columns (tabs expand to the next tab stop)

// VisualCol is the display column of byte column col in line.
func VisualCol(line string, col, tab int) int {
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

// ColForVisual is the byte column nearest to display column vcol.
func ColForVisual(line string, vcol, tab int) int {
	v := 0
	for i, r := range line {
		w := 1
		if r == '\t' {
			w = tab - v%tab
		}
		if v+w > vcol {
			if vcol-v > w/2 {
				return i + utf8.RuneLen(r)
			}
			return i
		}
		v += w
	}
	return len(line)
}

// ---------------------------------------------------------------------------
// Character classes

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func class(r rune) int {
	switch {
	case isWord(r):
		return 1
	case unicode.IsSpace(r):
		return 0
	default:
		return 2
	}
}

func prevRune(s string, col int) (rune, int) {
	if col <= 0 {
		return 0, 0
	}
	r, n := utf8.DecodeLastRuneInString(s[:col])
	return r, n
}

func nextRune(s string, col int) (rune, int) {
	if col >= len(s) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(s[col:])
}

// WordAt returns the word range around p (empty range if none).
func (m *Model) WordAt(p Pos) (Pos, Pos, bool) {
	line := m.Buf.Line(p.Line)
	start, end := p.Col, p.Col
	for start > 0 {
		r, n := prevRune(line, start)
		if !isWord(r) {
			break
		}
		start -= n
	}
	for end < len(line) {
		r, n := nextRune(line, end)
		if !isWord(r) {
			break
		}
		end += n
	}
	if start == end {
		return p, p, false
	}
	return Pos{Line: p.Line, Col: start}, Pos{Line: p.Line, Col: end}, true
}

// ---------------------------------------------------------------------------
// Movement

// Motion identifies a caret movement.
type Motion int

const (
	CharLeft Motion = iota
	CharRight
	WordLeft
	WordRight
	SubwordLeft
	SubwordRight
	LineUp
	LineDown
	LineStart // smart home: first non-blank, then column 0
	LineEnd
	DocStart
	DocEnd
	PageUp
	PageDown
)

func (m *Model) moveOne(r Range, mot Motion, extend bool, page int) Range {
	h := r.Head
	// Collapsing a selection with left/right goes to its edge.
	if !extend && !r.Empty() && (mot == CharLeft || mot == CharRight) {
		if mot == CharLeft {
			return buffer.Cursor(r.Start())
		}
		return buffer.Cursor(r.End())
	}
	goal := -1
	b := m.Buf
	line := b.Line(h.Line)
	switch mot {
	case CharLeft:
		if h.Col > 0 {
			_, n := prevRune(line, h.Col)
			h.Col -= n
		} else if h.Line > 0 {
			h = Pos{Line: h.Line - 1, Col: len(b.Line(h.Line - 1))}
		}
	case CharRight:
		if h.Col < len(line) {
			_, n := nextRune(line, h.Col)
			h.Col += n
		} else if h.Line < b.LineCount()-1 {
			h = Pos{Line: h.Line + 1, Col: 0}
		}
	case WordLeft, SubwordLeft:
		if h.Col == 0 {
			if h.Line > 0 {
				h = Pos{Line: h.Line - 1, Col: len(b.Line(h.Line - 1))}
			}
			break
		}
		c := h.Col
		for c > 0 {
			r, n := prevRune(line, c)
			if !unicode.IsSpace(r) {
				break
			}
			c -= n
		}
		if c > 0 {
			r0, _ := prevRune(line, c)
			cl := class(r0)
			for c > 0 {
				r, n := prevRune(line, c)
				if class(r) != cl {
					break
				}
				if mot == SubwordLeft && cl == 1 {
					// stop at camelCase humps and underscores
					if r == '_' && c != h.Col {
						break
					}
					p, _ := prevRune(line, c-n)
					if unicode.IsUpper(r) && c-n > 0 && unicode.IsLower(p) {
						c -= n
						break
					}
				}
				c -= n
			}
		}
		h.Col = c
	case WordRight, SubwordRight:
		if h.Col >= len(line) {
			if h.Line < b.LineCount()-1 {
				h = Pos{Line: h.Line + 1, Col: 0}
			}
			break
		}
		c := h.Col
		for c < len(line) {
			r, n := nextRune(line, c)
			if !unicode.IsSpace(r) {
				break
			}
			c += n
		}
		if c < len(line) {
			r0, _ := nextRune(line, c)
			cl := class(r0)
			first := true
			for c < len(line) {
				r, n := nextRune(line, c)
				if class(r) != cl {
					break
				}
				if mot == SubwordRight && cl == 1 && !first {
					if r == '_' || unicode.IsUpper(r) {
						break
					}
				}
				first = false
				c += n
			}
		}
		h.Col = c
	case LineUp, LineDown, PageUp, PageDown:
		goal = r.Goal
		if goal < 0 {
			goal = VisualCol(line, h.Col, m.TabSize)
		}
		delta := map[Motion]int{LineUp: -1, LineDown: 1, PageUp: -page, PageDown: page}[mot]
		nl := h.Line + delta
		if nl < 0 {
			h = Pos{Line: 0, Col: 0}
			goal = 0
		} else if nl >= b.LineCount() {
			h = b.EndPos()
			goal = VisualCol(b.Line(h.Line), h.Col, m.TabSize)
		} else {
			h = Pos{Line: nl, Col: ColForVisual(b.Line(nl), goal, m.TabSize)}
		}
	case LineStart:
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if h.Col == indent {
			h.Col = 0
		} else {
			h.Col = indent
		}
	case LineEnd:
		h.Col = len(line)
	case DocStart:
		h = Pos{}
	case DocEnd:
		h = b.EndPos()
	}
	if extend {
		return Range{Anchor: r.Anchor, Head: h, Goal: goal}
	}
	return Range{Anchor: h, Head: h, Goal: goal}
}

// Move applies a motion to every selection.
func (m *Model) Move(mot Motion, extend bool, page int) {
	out := make([]Range, len(m.Sels))
	for i, r := range m.Sels {
		out[i] = m.moveOne(r, mot, extend, page)
	}
	m.SetSels(out, m.Main)
}

// ---------------------------------------------------------------------------
// Editing

// edit applies one replacement per selection, computed by fn.
func (m *Model) edit(kind string, merge bool, fn func(r Range) (buffer.Edit, Range)) {
	if m.ReadOnly {
		return
	}
	edits := make([]buffer.Edit, 0, len(m.Sels))
	cursors := make([]Range, 0, len(m.Sels))
	for _, r := range m.Sels {
		e, c := fn(r)
		edits = append(edits, e)
		cursors = append(cursors, c)
	}
	// Map through the edits; result carets are given relative to each edit, so
	// apply and then place carets by mapping the desired positions.
	mapped := m.Buf.Apply(edits, markersFor(edits), buffer.ApplyOptions{Kind: kind, Merge: merge, Assoc: -1})
	out := make([]Range, len(cursors))
	for i, c := range cursors {
		// mapped[i] is where the i-th edit's text starts after all edits were
		// applied; c is expressed relative to that point.
		base := mapped[i].Anchor
		out[i] = Range{Anchor: offsetFrom(base, c.Anchor), Head: offsetFrom(base, c.Head), Goal: -1}
	}
	m.Sels, m.Main = m.normalize(out, m.Main)
}

// markersFor returns zero-width markers at each edit's start; applied with
// Assoc -1 they end up at the start of each edit's inserted text.
func markersFor(edits []buffer.Edit) []Range {
	out := make([]Range, len(edits))
	for i, e := range edits {
		s := e.Start
		if e.End.Less(s) {
			s = e.End
		}
		out[i] = Range{Anchor: s, Head: s}
	}
	return out
}

// offsetFrom interprets rel as a position relative to base, where rel.Line
// counts lines after base and rel.Col is absolute on later lines.
func offsetFrom(base, rel Pos) Pos {
	if rel.Line == 0 {
		return Pos{Line: base.Line, Col: base.Col + rel.Col}
	}
	return Pos{Line: base.Line + rel.Line, Col: rel.Col}
}

// rel converts text inserted at a point into the caret offset after it.
func relEnd(text string) Pos {
	return buffer.EndOf(Pos{}, text)
}

var pairs = map[string]string{"(": ")", "[": "]", "{": "}", "\"": "\"", "'": "'", "`": "`"}

// Insert types text at every selection (replacing selected text).
func (m *Model) Insert(text string) {
	if m.AutoPair && len(m.Sels) > 0 {
		if m.tryAutoPair(text) {
			return
		}
	}
	m.edit("type", text != "\n", func(r Range) (buffer.Edit, Range) {
		return buffer.Edit{Start: r.Start(), End: r.End(), Text: text}, buffer.Cursor(relEnd(text))
	})
}

func (m *Model) tryAutoPair(text string) bool {
	closeFor, isOpen := pairs[text]
	isClose := text == ")" || text == "]" || text == "}" || text == "\"" || text == "'" || text == "`"
	if !isOpen && !isClose {
		return false
	}
	// Typing a closer right before the same closer just steps over it.
	if isClose {
		allSkip := true
		for _, r := range m.Sels {
			nr, _ := nextRune(m.Buf.Line(r.Head.Line), r.Head.Col)
			if !r.Empty() || string(nr) != text {
				allSkip = false
				break
			}
		}
		if allSkip {
			m.Move(CharRight, false, 0)
			return true
		}
	}
	if !isOpen {
		return false
	}
	// Wrap selections in the pair.
	if !m.Sels[0].Empty() {
		m.edit("type", false, func(r Range) (buffer.Edit, Range) {
			inner := m.Buf.Slice(r.Start(), r.End())
			t := text + inner + closeFor
			start := relEnd(text)
			end := relEnd(text + inner)
			return buffer.Edit{Start: r.Start(), End: r.End(), Text: t}, Range{Anchor: start, Head: end, Goal: -1}
		})
		return true
	}
	// Insert the pair only before whitespace/closers/end of line.
	for _, r := range m.Sels {
		nr, _ := nextRune(m.Buf.Line(r.Head.Line), r.Head.Col)
		if nr != 0 && !unicode.IsSpace(nr) && !strings.ContainsRune(")]};,", nr) {
			return false
		}
		if text == "\"" || text == "'" || text == "`" {
			pr, _ := prevRune(m.Buf.Line(r.Head.Line), r.Head.Col)
			if isWord(pr) {
				return false
			}
		}
	}
	m.edit("type", true, func(r Range) (buffer.Edit, Range) {
		return buffer.Edit{Start: r.Head, End: r.Head, Text: text + closeFor}, buffer.Cursor(relEnd(text))
	})
	return true
}

// Newline inserts a line break keeping (and increasing) indentation.
func (m *Model) Newline() {
	m.edit("newline", false, func(r Range) (buffer.Edit, Range) {
		line := m.Buf.Line(r.Start().Line)
		indent := ""
		if m.AutoIndent {
			indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			before := strings.TrimRight(line[:r.Start().Col], " \t")
			after := strings.TrimLeft(m.Buf.Line(r.End().Line)[r.End().Col:], " \t")
			if before != "" && strings.ContainsAny(before[len(before)-1:], "{[(:") {
				inner := indent + m.indentUnit()
				// Between a pair: put the closer on its own line.
				if after != "" && strings.ContainsAny(after[:1], "}])") {
					t := "\n" + inner + "\n" + indent
					return buffer.Edit{Start: r.Start(), End: r.End(), Text: t}, buffer.Cursor(relEnd("\n" + inner))
				}
				indent = inner
			}
		}
		t := "\n" + indent
		return buffer.Edit{Start: r.Start(), End: r.End(), Text: t}, buffer.Cursor(relEnd(t))
	})
}

func (m *Model) indentUnit() string {
	if m.UseSpaces {
		return strings.Repeat(" ", m.TabSize)
	}
	return "\t"
}

// Backspace deletes the selection or the character before each caret.
// In leading whitespace with spaces it deletes back to the previous tab stop.
func (m *Model) Backspace(by Motion) {
	m.edit("delete", true, func(r Range) (buffer.Edit, Range) {
		if !r.Empty() {
			return buffer.Edit{Start: r.Start(), End: r.End()}, buffer.Cursor(Pos{})
		}
		h := r.Head
		line := m.Buf.Line(h.Line)
		start := m.moveOne(r, by, false, 0).Head
		if by == CharLeft && h.Col > 0 {
			// Delete an auto-inserted pair together.
			pr, _ := prevRune(line, h.Col)
			nr, nn := nextRune(line, h.Col)
			if c, ok := pairs[string(pr)]; ok && c == string(nr) {
				return buffer.Edit{Start: start, End: Pos{Line: h.Line, Col: h.Col + nn}}, buffer.Cursor(Pos{})
			}
			// Soft tab stops.
			if m.UseSpaces && strings.TrimLeft(line[:h.Col], " ") == "" && h.Col > 0 {
				v := VisualCol(line, h.Col, m.TabSize)
				target := (v - 1) / m.TabSize * m.TabSize
				start = Pos{Line: h.Line, Col: ColForVisual(line, target, m.TabSize)}
			}
		}
		return buffer.Edit{Start: start, End: h}, buffer.Cursor(Pos{})
	})
}

// DeleteForward deletes the selection or the character after each caret.
func (m *Model) DeleteForward(by Motion) {
	m.edit("delete", true, func(r Range) (buffer.Edit, Range) {
		if !r.Empty() {
			return buffer.Edit{Start: r.Start(), End: r.End()}, buffer.Cursor(Pos{})
		}
		end := m.moveOne(r, by, false, 0).Head
		return buffer.Edit{Start: r.Head, End: end}, buffer.Cursor(Pos{})
	})
}

// DeleteToLineEnd (⌃K) and DeleteToLineStart (⌘⌫).
func (m *Model) DeleteToLineEnd() {
	m.edit("delete", false, func(r Range) (buffer.Edit, Range) {
		h := r.Head
		end := Pos{Line: h.Line, Col: len(m.Buf.Line(h.Line))}
		if h == end && h.Line < m.Buf.LineCount()-1 {
			end = Pos{Line: h.Line + 1, Col: 0}
		}
		return buffer.Edit{Start: h, End: end}, buffer.Cursor(Pos{})
	})
}
func (m *Model) DeleteToLineStart() {
	m.edit("delete", false, func(r Range) (buffer.Edit, Range) {
		h := r.Head
		return buffer.Edit{Start: Pos{Line: h.Line, Col: 0}, End: h}, buffer.Cursor(Pos{})
	})
}

// Tab indents selected lines, or inserts a (soft) tab at carets.
func (m *Model) Tab() {
	for _, r := range m.Sels {
		if r.Start().Line != r.End().Line {
			m.Indent()
			return
		}
	}
	m.edit("type", true, func(r Range) (buffer.Edit, Range) {
		if !m.UseSpaces {
			return buffer.Edit{Start: r.Start(), End: r.End(), Text: "\t"}, buffer.Cursor(Pos{Line: 0, Col: 1})
		}
		line := m.Buf.Line(r.Start().Line)
		v := VisualCol(line, r.Start().Col, m.TabSize)
		n := m.TabSize - v%m.TabSize
		return buffer.Edit{Start: r.Start(), End: r.End(), Text: strings.Repeat(" ", n)}, buffer.Cursor(Pos{Line: 0, Col: n})
	})
}

// selectedLines returns the distinct line numbers covered by selections.
func (m *Model) selectedLines() []int {
	seen := map[int]bool{}
	var out []int
	for _, r := range m.Sels {
		s, e := r.Start(), r.End()
		last := e.Line
		if e.Col == 0 && e.Line > s.Line {
			last--
		}
		for l := s.Line; l <= last; l++ {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	sort.Ints(out)
	return out
}

// lineEdits applies per-line edits while keeping selections in place.
func (m *Model) lineEdits(kind string, edits []buffer.Edit) {
	if len(edits) == 0 || m.ReadOnly {
		return
	}
	sels := m.Buf.Apply(edits, m.Sels, buffer.ApplyOptions{Kind: kind})
	m.Sels, m.Main = m.normalize(sels, m.Main)
}

// Indent / Unindent the selected lines (⌘] / ⌘[).
func (m *Model) Indent() {
	var edits []buffer.Edit
	for _, l := range m.selectedLines() {
		if m.Buf.Line(l) == "" && len(m.selectedLines()) > 1 {
			continue
		}
		edits = append(edits, buffer.Edit{Start: Pos{Line: l, Col: 0}, End: Pos{Line: l, Col: 0}, Text: m.indentUnit()})
	}
	// Selections starting at column 0 should grow to include the indent.
	before := m.Sels
	m.lineEdits("indent", edits)
	for i := range m.Sels {
		if i < len(before) {
			if before[i].Anchor.Col == 0 && before[i].Anchor.Line == m.Sels[i].Anchor.Line && !before[i].Empty() {
				m.Sels[i].Anchor.Col = 0
			}
		}
	}
}
func (m *Model) Unindent() {
	var edits []buffer.Edit
	for _, l := range m.selectedLines() {
		line := m.Buf.Line(l)
		n := 0
		if strings.HasPrefix(line, "\t") {
			n = 1
		} else {
			for n < len(line) && n < m.TabSize && line[n] == ' ' {
				n++
			}
		}
		if n > 0 {
			edits = append(edits, buffer.Edit{Start: Pos{Line: l, Col: 0}, End: Pos{Line: l, Col: n}})
		}
	}
	m.lineEdits("indent", edits)
}

// ToggleComment comments or uncomments the selected lines.
func (m *Model) ToggleComment() {
	tok := m.LineComment
	if tok == "" {
		if m.BlockComment[0] != "" {
			m.toggleBlockComment()
		}
		return
	}
	lines := m.selectedLines()
	all := true
	minIndent := -1
	for _, l := range lines {
		line := m.Buf.Line(l)
		t := strings.TrimLeft(line, " \t")
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, tok) {
			all = false
		}
		ind := len(line) - len(t)
		if minIndent < 0 || ind < minIndent {
			minIndent = ind
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	var edits []buffer.Edit
	for _, l := range lines {
		line := m.Buf.Line(l)
		t := strings.TrimLeft(line, " \t")
		ind := len(line) - len(t)
		if all {
			if t == "" {
				continue
			}
			n := len(tok)
			if strings.HasPrefix(t[len(tok):], " ") {
				n++
			}
			edits = append(edits, buffer.Edit{Start: Pos{Line: l, Col: ind}, End: Pos{Line: l, Col: ind + n}})
		} else {
			if t == "" && len(lines) > 1 {
				continue
			}
			at := minIndent
			if at > len(line) {
				at = len(line)
			}
			edits = append(edits, buffer.Edit{Start: Pos{Line: l, Col: at}, End: Pos{Line: l, Col: at}, Text: tok + " "})
		}
	}
	m.lineEdits("comment", edits)
}

func (m *Model) toggleBlockComment() {
	open, close := m.BlockComment[0], m.BlockComment[1]
	m.edit("comment", false, func(r Range) (buffer.Edit, Range) {
		s, e := r.Start(), r.End()
		if r.Empty() {
			line := m.Buf.Line(s.Line)
			s, e = Pos{Line: s.Line, Col: 0}, Pos{Line: s.Line, Col: len(line)}
		}
		text := m.Buf.Slice(s, e)
		if strings.HasPrefix(text, open) && strings.HasSuffix(text, close) && len(text) >= len(open)+len(close) {
			inner := text[len(open) : len(text)-len(close)]
			inner = strings.TrimPrefix(strings.TrimSuffix(inner, " "), " ")
			return buffer.Edit{Start: s, End: e, Text: inner}, Range{Anchor: Pos{}, Head: relEnd(inner), Goal: -1}
		}
		t := open + " " + text + " " + close
		return buffer.Edit{Start: s, End: e, Text: t}, Range{Anchor: Pos{}, Head: relEnd(t), Goal: -1}
	})
}

// DuplicateLine (⇧⌘D): duplicates lines, or the selected text.
func (m *Model) DuplicateLine() {
	m.edit("duplicate", false, func(r Range) (buffer.Edit, Range) {
		if r.Empty() {
			line := m.Buf.Line(r.Head.Line)
			at := Pos{Line: r.Head.Line, Col: len(line)}
			return buffer.Edit{Start: at, End: at, Text: "\n" + line}, buffer.Cursor(Pos{Line: 1, Col: r.Head.Col})
		}
		text := m.Buf.Slice(r.Start(), r.End())
		return buffer.Edit{Start: r.End(), End: r.End(), Text: text}, Range{Anchor: Pos{}, Head: relEnd(text), Goal: -1}
	})
}

// DeleteLine (⌃⇧K).
func (m *Model) DeleteLine() {
	lines := m.selectedLines()
	if len(lines) == 0 {
		return
	}
	var edits []buffer.Edit
	// Group consecutive lines into blocks.
	for i := 0; i < len(lines); {
		j := i
		for j+1 < len(lines) && lines[j+1] == lines[j]+1 {
			j++
		}
		first, last := lines[i], lines[j]
		start, end := Pos{Line: first, Col: 0}, Pos{Line: last + 1, Col: 0}
		if last+1 >= m.Buf.LineCount() {
			end = Pos{Line: last, Col: len(m.Buf.Line(last))}
			if first > 0 {
				start = Pos{Line: first - 1, Col: len(m.Buf.Line(first - 1))}
			}
		}
		edits = append(edits, buffer.Edit{Start: start, End: end})
		i = j + 1
	}
	sels := m.Buf.Apply(edits, m.Sels, buffer.ApplyOptions{Kind: "delete"})
	for i := range sels {
		sels[i] = buffer.Cursor(sels[i].Head)
	}
	m.Sels, m.Main = m.normalize(sels, m.Main)
}

// MoveLines swaps the selected lines with the one above (dir -1) or below (+1).
func (m *Model) MoveLines(dir int) {
	lines := m.selectedLines()
	if len(lines) == 0 {
		return
	}
	first, last := lines[0], lines[len(lines)-1]
	if (dir < 0 && first == 0) || (dir > 0 && last >= m.Buf.LineCount()-1) {
		return
	}
	block := make([]string, 0, last-first+1)
	for l := first; l <= last; l++ {
		block = append(block, m.Buf.Line(l))
	}
	var start, end Pos
	var text string
	if dir < 0 {
		above := m.Buf.Line(first - 1)
		start, end = Pos{Line: first - 1, Col: 0}, Pos{Line: last, Col: len(m.Buf.Line(last))}
		text = strings.Join(block, "\n") + "\n" + above
	} else {
		below := m.Buf.Line(last + 1)
		start, end = Pos{Line: first, Col: 0}, Pos{Line: last + 1, Col: len(below)}
		text = below + "\n" + strings.Join(block, "\n")
	}
	old := append([]Range(nil), m.Sels...)
	m.Buf.Apply([]buffer.Edit{{Start: start, End: end, Text: text}}, nil, buffer.ApplyOptions{Kind: "move"})
	for i := range old {
		old[i].Anchor.Line += dir
		old[i].Head.Line += dir
	}
	m.Sels, m.Main = m.normalize(old, m.Main)
}

// JoinLines (⌘J).
func (m *Model) JoinLines() {
	var edits []buffer.Edit
	seen := map[int]bool{}
	for _, r := range m.Sels {
		first, last := r.Start().Line, r.End().Line
		if last == first {
			last = first + 1
		}
		for l := first; l < last && l < m.Buf.LineCount()-1; l++ {
			if seen[l] {
				continue
			}
			seen[l] = true
			line, next := m.Buf.Line(l), m.Buf.Line(l+1)
			trail := len(line) - len(strings.TrimRight(line, " \t"))
			lead := len(next) - len(strings.TrimLeft(next, " \t"))
			sep := " "
			if strings.TrimSpace(line) == "" || strings.TrimSpace(next) == "" {
				sep = ""
			}
			edits = append(edits, buffer.Edit{Start: Pos{Line: l, Col: len(line) - trail}, End: Pos{Line: l + 1, Col: lead}, Text: sep})
		}
	}
	m.lineEdits("join", edits)
}

// InsertLineAfter (⌘↩) and InsertLineBefore (⇧⌘↩).
func (m *Model) InsertLineAfter() {
	m.edit("newline", false, func(r Range) (buffer.Edit, Range) {
		line := m.Buf.Line(r.Head.Line)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		at := Pos{Line: r.Head.Line, Col: len(line)}
		return buffer.Edit{Start: at, End: at, Text: "\n" + indent}, buffer.Cursor(Pos{Line: 1, Col: len(indent)})
	})
}
func (m *Model) InsertLineBefore() {
	m.edit("newline", false, func(r Range) (buffer.Edit, Range) {
		line := m.Buf.Line(r.Head.Line)
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		at := Pos{Line: r.Head.Line, Col: 0}
		return buffer.Edit{Start: at, End: at, Text: indent + "\n"}, buffer.Cursor(Pos{Line: 0, Col: len(indent)})
	})
}

// TransformSelections replaces each selection's text (or the word at an
// empty caret) with fn(text).
func (m *Model) TransformSelections(fn func(string) string) {
	m.edit("transform", false, func(r Range) (buffer.Edit, Range) {
		s, e := r.Start(), r.End()
		if r.Empty() {
			if ws, we, ok := m.WordAt(r.Head); ok {
				s, e = ws, we
			}
		}
		t := fn(m.Buf.Slice(s, e))
		if r.Empty() {
			return buffer.Edit{Start: s, End: e, Text: t}, buffer.Cursor(Pos{Line: 0, Col: r.Head.Col - s.Col})
		}
		return buffer.Edit{Start: s, End: e, Text: t}, Range{Anchor: Pos{}, Head: relEnd(t), Goal: -1}
	})
}

// ReplaceLinesOrAll replaces the selected lines (whole lines) or, with no
// selection, the whole buffer, with fn(text).
func (m *Model) ReplaceLinesOrAll(fn func(string) (string, error)) error {
	var targets [][2]Pos
	for _, r := range m.Sels {
		if r.Empty() {
			continue
		}
		s, e := r.Start(), r.End()
		if e.Col == 0 && e.Line > s.Line {
			e = Pos{Line: e.Line - 1, Col: len(m.Buf.Line(e.Line - 1))}
		} else {
			e = Pos{Line: e.Line, Col: len(m.Buf.Line(e.Line))}
		}
		targets = append(targets, [2]Pos{{Line: s.Line}, e})
	}
	if len(targets) == 0 {
		targets = [][2]Pos{{{}, m.Buf.EndPos()}}
	}
	var edits []buffer.Edit
	for _, t := range targets {
		in := m.Buf.Slice(t[0], t[1])
		out, err := fn(in)
		if err != nil {
			return err
		}
		if out != in {
			edits = append(edits, buffer.Edit{Start: t[0], End: t[1], Text: out})
		}
	}
	m.lineEdits("transform", edits)
	return nil
}
