package ui

import (
	"sort"
	"unicode/utf8"

	"inks/internal/buffer"
	"inks/internal/editor"
)

// rowLayout maps buffer lines to visual rows (more than one per line when
// word wrap is on). Rows are recomputed when the text, width or tab size
// changes; wrap points are memoized per line text.
type rowLayout struct {
	ver, width, tab int
	foldVer         int
	wrap            bool
	hidden          map[int]bool
	start           []int         // start[l] = first row of line l; start[n] = total rows
	segs            map[int][]int // byte offsets where wrapped rows begin (after 0), by line
	memo            map[string][]int
}

func (d *Doc) layoutRows(width, tab int, wrap bool) *rowLayout {
	rl := d.rows
	if rl == nil {
		rl = &rowLayout{}
		d.rows = rl
	}
	b := d.M.Buf
	if rl.start != nil && rl.ver == b.Version() && rl.width == width && rl.tab == tab && rl.wrap == wrap && rl.foldVer == d.foldVer {
		return rl
	}
	rl.foldVer = d.foldVer
	rl.hidden = d.hiddenLines()
	if rl.width != width || rl.tab != tab || rl.memo == nil {
		rl.memo = map[string][]int{}
	}
	rl.ver, rl.width, rl.tab, rl.wrap = b.Version(), width, tab, wrap
	n := b.LineCount()
	if cap(rl.start) >= n+1 {
		rl.start = rl.start[:n+1]
	} else {
		rl.start = make([]int, n+1)
	}
	rl.segs = map[int][]int{}
	row := 0
	for l := 0; l < n; l++ {
		rl.start[l] = row
		if rl.hidden[l] {
			continue
		}
		row++
		if !wrap {
			continue
		}
		line := b.Line(l)
		if len(line) <= width && !containsTab(line) {
			continue
		}
		br, ok := rl.memo[line]
		if !ok {
			br = wrapLine(line, width, tab)
			rl.memo[line] = br
		}
		if len(br) > 0 {
			rl.segs[l] = br
			row += len(br)
		}
	}
	rl.start[n] = row
	return rl
}

func containsTab(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			return true
		}
	}
	return false
}

// wrapLine returns the byte offsets where continuation rows start, breaking
// after spaces when possible.
func wrapLine(line string, width, tab int) []int {
	if width < 8 {
		width = 8
	}
	var out []int
	segStart, segV := 0, 0
	v := 0
	lastBreak := -1 // byte offset just after a space in the current row
	lastBreakV := 0
	for i, r := range line {
		w := 1
		if r == '\t' {
			w = tab - v%tab
		}
		if v+w-segV > width && i > segStart {
			at, atV := i, v
			if lastBreak > segStart {
				at, atV = lastBreak, lastBreakV
			}
			out = append(out, at)
			segStart, segV = at, atV
			lastBreak = -1
		}
		v += w
		if r == ' ' || r == '\t' {
			lastBreak, lastBreakV = i+1, v
		}
	}
	return out
}

func (rl *rowLayout) total() int { return rl.start[len(rl.start)-1] }

// segments returns the row start offsets of line l (always beginning with 0).
func (rl *rowLayout) segments(l int) []int {
	if s, ok := rl.segs[l]; ok {
		return append([]int{0}, s...)
	}
	return []int{0}
}

// lineOfRow returns the line and segment index shown on a row.
func (rl *rowLayout) lineOfRow(row int) (int, int) {
	n := len(rl.start) - 1
	if row <= 0 {
		return 0, 0
	}
	if row >= rl.total() {
		return n - 1, len(rl.segments(n-1)) - 1
	}
	l := sort.Search(n, func(i int) bool { return rl.start[i+1] > row })
	return l, row - rl.start[l]
}

// rowOf returns the row that shows position p and the byte offset where
// that row starts. A position at a wrap point belongs to the following row.
func (rl *rowLayout) rowOf(p buffer.Pos) (int, int) {
	if p.Line >= len(rl.start)-1 {
		return rl.total() - 1, 0
	}
	segs := rl.segments(p.Line)
	k := sort.Search(len(segs), func(i int) bool { return segs[i] > p.Col }) - 1
	if k < 0 {
		k = 0
	}
	return rl.start[p.Line] + k, segs[k]
}

// rowSpan returns the byte range [from, to) of segment k of a line.
func (rl *rowLayout) rowSpan(line string, l, k int) (int, int) {
	segs := rl.segments(l)
	from := segs[k]
	to := len(line)
	if k+1 < len(segs) {
		to = segs[k+1]
	}
	return from, to
}

// visRow is one row on screen.
type visRow struct {
	line, seg  int
	from, to   int // byte range of the line shown on this row
	vstart     int // visual column of from
	lastOfLine bool
	y          int // top of the row
}

func (rl *rowLayout) visible(b *buffer.Buffer, firstRow, count, lineH, offY, tab int) []visRow {
	var out []visRow
	if b.LineCount() == 0 {
		return nil
	}
	l, k := rl.lineOfRow(firstRow)
	for i := 0; i < count && l < b.LineCount(); i++ {
		line := b.Line(l)
		segs := rl.segments(l)
		from, to := rl.rowSpan(line, l, k)
		out = append(out, visRow{
			line: l, seg: k, from: from, to: to,
			vstart:     editor.VisualCol(line, from, tab),
			lastOfLine: k == len(segs)-1,
			y:          i*lineH + offY,
		})
		k++
		if k >= len(segs) {
			l, k = l+1, 0
			for l < b.LineCount() && rl.hidden[l] {
				l++
			}
		}
	}
	return out
}

// moveRows moves a position up or down by visual rows, keeping the goal
// x (in columns relative to the row start).
func (rl *rowLayout) moveRows(b *buffer.Buffer, p buffer.Pos, goal, dir, tab int) buffer.Pos {
	row, _ := rl.rowOf(p)
	row += dir
	if row < 0 {
		return buffer.Pos{}
	}
	if row >= rl.total() {
		return b.EndPos()
	}
	l, k := rl.lineOfRow(row)
	line := b.Line(l)
	from, to := rl.rowSpan(line, l, k)
	v0 := editor.VisualCol(line, from, tab)
	col := editor.ColForVisual(line, v0+goal, tab)
	segs := rl.segments(l)
	if k < len(segs)-1 && col >= to {
		// Stay on this row: before the last character of a wrapped row.
		col = to
		for col > from {
			_, size := utf8.DecodeLastRuneInString(line[:col])
			col -= size
			break
		}
	}
	col = min(max(col, from), to)
	return buffer.Pos{Line: l, Col: col}
}
