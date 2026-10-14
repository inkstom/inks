package ui

import (
	"sort"
	"strings"

	"inks/internal/buffer"
)

// Code folding: a fold hides whole lines (first..last). Folds follow edits
// and open when an edit touches them or a caret lands inside.

type fold struct{ a, b buffer.Pos } // start of first hidden line, end of last

func (d *Doc) hiddenLines() map[int]bool {
	if len(d.folds) == 0 {
		return nil
	}
	h := map[int]bool{}
	for _, f := range d.folds {
		for l := f.a.Line; l <= f.b.Line; l++ {
			h[l] = true
		}
	}
	return h
}

func (d *Doc) addFold(first, last int) bool {
	if first > last || first <= 0 || last >= d.M.Buf.LineCount() {
		return false
	}
	// Merge with overlapping folds.
	nf := fold{a: buffer.Pos{Line: first}, b: buffer.Pos{Line: last, Col: len(d.M.Buf.Line(last))}}
	var keep []fold
	for _, f := range d.folds {
		if f.b.Line < first || f.a.Line > last {
			keep = append(keep, f)
			continue
		}
		if f.a.Line < nf.a.Line {
			nf.a = f.a
		}
		if nf.b.Less(f.b) {
			nf.b = f.b
		}
	}
	d.folds = append(keep, nf)
	sort.Slice(d.folds, func(i, j int) bool { return d.folds[i].a.Less(d.folds[j].a) })
	d.foldVer++
	return true
}

// unfoldLines opens folds that hide any line in [first, last].
func (d *Doc) unfoldLines(first, last int) bool {
	var keep []fold
	changed := false
	for _, f := range d.folds {
		if f.b.Line < first || f.a.Line > last {
			keep = append(keep, f)
		} else {
			changed = true
		}
	}
	if changed {
		d.folds = keep
		d.foldVer++
	}
	return changed
}

func (d *Doc) unfoldAll() {
	if len(d.folds) > 0 {
		d.folds = nil
		d.foldVer++
	}
}

func indentWidth(line string, tab int) int {
	return visualCol(line, len(line)-len(strings.TrimLeft(line, " \t")), tab)
}

// blockAt finds the lines to hide for the block around line l: the inside
// of the innermost bracket pair that opens on or before l and closes after
// it, or the more-indented lines that follow l.
func (d *Doc) blockAt(l int) (int, int, bool) {
	b := d.M.Buf
	// Lines following an opener at the end of l (or the caret line itself).
	line := strings.TrimRight(b.Line(l), " \t")
	if strings.HasSuffix(line, "{") || strings.HasSuffix(line, "[") || strings.HasSuffix(line, "(") {
		if end, ok := d.matchClose(l, len(line)-1); ok && end > l+1 {
			return l + 1, end - 1, true
		}
	}
	// Indentation: the run of deeper lines after l.
	tab := d.M.TabSize
	if strings.TrimSpace(line) != "" {
		base := indentWidth(b.Line(l), tab)
		last := l
		for k := l + 1; k < b.LineCount(); k++ {
			t := b.Line(k)
			if strings.TrimSpace(t) == "" {
				continue
			}
			if indentWidth(t, tab) <= base {
				break
			}
			last = k
		}
		if last > l {
			return l + 1, last, true
		}
	}
	// Otherwise fold the enclosing block: walk up to its header.
	base := indentWidth(b.Line(l), tab)
	for k := l - 1; k >= 0; k-- {
		t := b.Line(k)
		if strings.TrimSpace(t) == "" {
			continue
		}
		if indentWidth(t, tab) < base {
			return d.blockAt(k)
		}
	}
	return 0, 0, false
}

// matchClose finds the line of the bracket closing the opener at (l, col).
func (d *Doc) matchClose(l, col int) (int, bool) {
	b := d.M.Buf
	open := b.Line(l)[col]
	close := map[byte]byte{'{': '}', '[': ']', '(': ')'}[open]
	depth := 0
	inStr := byte(0)
	for k := l; k < b.LineCount() && k < l+20000; k++ {
		t := b.Line(k)
		start := 0
		if k == l {
			start = col
		}
		for i := start; i < len(t); i++ {
			c := t[i]
			switch {
			case inStr != 0:
				if c == '\\' {
					i++
				} else if c == inStr {
					inStr = 0
				}
			case c == '"' || c == '\'' || c == '`':
				inStr = c
			case c == open:
				depth++
			case c == close:
				depth--
				if depth == 0 {
					return k, true
				}
			}
		}
		if inStr != '`' {
			inStr = 0
		}
	}
	return 0, false
}

// foldSelections folds multi-line selections, or the block at each caret.
func (d *Doc) foldSelections() bool {
	done := false
	for _, r := range d.M.Sels {
		s, e := r.Start(), r.End()
		if e.Line > s.Line {
			done = d.addFold(s.Line+1, e.Line) || done
			continue
		}
		if first, last, ok := d.blockAt(s.Line); ok {
			done = d.addFold(first, last) || done
		}
	}
	if done {
		// Carets inside the fold move to its header line.
		h := d.hiddenLines()
		sels := append([]buffer.Range(nil), d.M.Sels...)
		for i, r := range sels {
			l := r.Head.Line
			for l > 0 && h[l] {
				l--
			}
			if l != r.Head.Line {
				sels[i] = buffer.Cursor(buffer.Pos{Line: l, Col: len(d.M.Buf.Line(l))})
			}
		}
		d.M.SetSels(sels, d.M.Main)
	}
	return done
}

// foldByLevel folds every block whose header is at indentation level n-1.
func (d *Doc) foldByLevel(n int) {
	d.unfoldAll()
	b := d.M.Buf
	tab := max(1, d.M.TabSize)
	for l := 0; l < b.LineCount(); l++ {
		t := b.Line(l)
		if strings.TrimSpace(t) == "" || indentWidth(t, tab)/tab != n-1 {
			continue
		}
		if first, last, ok := d.blockAt(l); ok && first == l+1 {
			d.addFold(first, last)
			l = last
		}
	}
}

// openFoldsAtCarets unfolds folds that contain a caret.
func (d *Doc) openFoldsAtCarets() {
	if len(d.folds) == 0 {
		return
	}
	h := d.hiddenLines()
	for _, r := range d.M.Sels {
		if h[r.Head.Line] {
			d.unfoldLines(r.Head.Line, r.Head.Line)
		}
	}
}
