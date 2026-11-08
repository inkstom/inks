package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"inks/internal/buffer"
	"inks/internal/search"
)

// Selection matches: while text is selected, the other places with the same
// text are marked, filled when the case matches too and outlined when only
// the case differs. A selected whole word matches whole words only.

// maxOccurrences caps the marks, so a huge file stays fast.
const maxOccurrences = 5000

type occurrence struct {
	r     buffer.Range
	exact bool
}

// occCache holds the matches of one selection in one version of a document.
type occCache struct {
	key   string
	ver   int
	list  []occurrence
	exact int
}

// selectionMatches returns the matches of the main selection in d, or nil
// when nothing should be marked: the setting is off, the find widget is open
// (it marks its own matches), or the selection is empty, spans lines or is
// only whitespace.
func (a *App) selectionMatches(d *Doc) *occCache {
	if !a.set.Bool("highlight_selection_matches") || a.find.visible {
		return nil
	}
	sel := d.M.MainSel()
	if sel.Empty() || sel.Start().Line != sel.End().Line || d.M.Buf.Len() > 16<<20 {
		return nil
	}
	q := d.M.Buf.Slice(sel.Start(), sel.End())
	if strings.TrimSpace(q) == "" || len(q) > 500 {
		return nil
	}
	word := isWholeWord(d.M.Buf.Line(sel.Start().Line), sel.Start().Col, sel.End().Col)
	anyCase := a.set.Bool("highlight_case_insensitive_matches")
	key := fmt.Sprint(q, word, anyCase)
	if c := d.occ; c != nil && c.key == key && c.ver == d.M.Buf.Version() {
		return c
	}
	m := search.New(q, search.Options{CaseSensitive: !anyCase, WholeWord: word, MaxMatches: maxOccurrences})
	c := &occCache{key: key, ver: d.M.Buf.Version()}
	for _, mt := range m.FindAll(d.M.Buf.Text("\n"), 0, 0) {
		o := occurrence{r: buffer.Range{Anchor: d.M.Buf.PosAt(mt.From), Head: d.M.Buf.PosAt(mt.To), Goal: -1}, exact: mt.Text == q}
		if o.exact {
			c.exact++
		}
		c.list = append(c.list, o)
	}
	d.occ = c
	return c
}

// isWholeWord reports whether line[from:to] is a word on its own: word
// characters only, with no word character just before or after it.
func isWholeWord(line string, from, to int) bool {
	if from < 0 || to > len(line) || from >= to {
		return false
	}
	for _, r := range line[from:to] {
		if !isWordRune(r) {
			return false
		}
	}
	if r, _ := utf8.DecodeLastRuneInString(line[:from]); from > 0 && isWordRune(r) {
		return false
	}
	if r, _ := utf8.DecodeRuneInString(line[to:]); to < len(line) && isWordRune(r) {
		return false
	}
	return true
}

// status describes the selection's matches for the status bar.
func (c *occCache) status() string {
	n := len(c.list)
	if n < 2 {
		return ""
	}
	more := ""
	if n >= maxOccurrences {
		more = "+"
	}
	if c.exact == n {
		return fmt.Sprintf("%d%s matches", n, more)
	}
	return fmt.Sprintf("%d%s matches (%d exact)", n, more, c.exact)
}
