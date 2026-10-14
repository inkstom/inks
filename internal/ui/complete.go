package ui

import (
	"encoding/json"
	"image"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gioui.org/layout"

	"inks/internal/buffer"
	"inks/internal/fuzzy"
)

// Autocomplete: words from open documents plus plugin completions, shown
// in a list under the caret. ↑/↓ choose, Tab or ↩ accept, ⎋ dismisses.

type completion struct {
	label, apply, detail string
	snippet              bool
	score                float64
}

type Completer struct {
	a       *App
	visible bool
	doc     *Doc
	prefix  string
	items   []completion
	plugin  []completion
	index   int
	ver     int
}

func newCompleter(a *App) *Completer { return &Completer{a: a} }

func isWordRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// prefixAt returns the word fragment before the main caret.
func prefixAt(d *Doc) string {
	h := d.M.MainSel().Head
	line := d.M.Buf.Line(h.Line)[:h.Col]
	i := len(line)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:i])
		if !isWordRune(r) {
			break
		}
		i -= size
	}
	return line[i:]
}

// afterType is called after text was typed: open, refresh or close the list.
func (c *Completer) afterType(d *Doc, typed string) {
	a := c.a
	if !a.set.Bool("auto_complete") || len(d.M.Sels) > 64 {
		c.hide()
		return
	}
	r, _ := utf8.DecodeLastRuneInString(typed)
	if typed == "" || !isWordRune(r) {
		c.hide()
		return
	}
	c.refresh(d)
}

func (c *Completer) refresh(d *Doc) {
	prefix := prefixAt(d)
	if utf8.RuneCountInString(prefix) < max(1, c.a.set.Int("auto_complete_min_chars")) {
		c.hide()
		return
	}
	if c.doc != d || !strings.HasPrefix(prefix, c.prefix) || c.prefix == "" {
		c.plugin = nil
		c.queryPlugins(d, prefix)
	}
	c.doc, c.prefix, c.ver = d, prefix, d.M.Buf.Version()
	c.rank()
}

// show opens the list on demand (⌃Space).
func (c *Completer) show(d *Doc) {
	c.doc, c.prefix = nil, ""
	c.refresh(d)
	if !c.visible {
		c.a.setStatus("No completions", 0)
	}
}

func (c *Completer) queryPlugins(d *Doc, prefix string) {
	a := c.a
	if a.py == nil || !a.py.ready || !a.py.events["on_query_completions"] {
		return
	}
	a.py.event("on_query_completions", d, []any{prefix, []any{d.pt(d.M.MainSel().Head)}}, func(raw json.RawMessage) {
		var r struct {
			Items []struct {
				Label, Apply, Detail string
				Snippet              bool
			}
		}
		if raw == nil || json.Unmarshal(raw, &r) != nil || c.doc != d {
			return
		}
		c.plugin = nil
		for _, it := range r.Items {
			c.plugin = append(c.plugin, completion{label: it.Label, apply: it.Apply, detail: it.Detail, snippet: it.Snippet})
		}
		if c.doc == d && c.ver == d.M.Buf.Version() {
			c.rank()
		}
	})
}

func (c *Completer) rank() {
	d := c.doc
	seen := map[string]bool{c.prefix: true}
	var out []completion
	add := func(it completion) {
		if seen[it.label] {
			return
		}
		r, ok := fuzzy.Match(c.prefix, it.label)
		if !ok {
			return
		}
		it.score = r.Score
		if strings.HasPrefix(it.label, c.prefix) {
			it.score += 50
		}
		seen[it.label] = true
		out = append(out, it)
	}
	for _, it := range c.plugin {
		it.score += 20
		add(it)
	}
	for _, s := range c.a.snippetsFor(d) {
		detail := s.desc
		if detail == "" {
			detail = "snippet"
		}
		add(completion{label: s.trigger, apply: s.content, detail: detail, snippet: true, score: 10})
	}
	first, _ := utf8.DecodeRuneInString(c.prefix)
	for _, x := range append([]*Doc{d}, c.a.allDocs()...) {
		for _, w := range wordsStarting(x, first, 2000) {
			add(completion{label: w, apply: w})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > 200 {
		out = out[:200]
	}
	c.items = out
	c.index = 0
	c.visible = len(out) > 0
}

// wordsStarting lists words of d beginning with r (any case).
func wordsStarting(d *Doc, r rune, limit int) []string {
	lr := unicode.ToLower(r)
	seen := map[string]bool{}
	var out []string
	for l := 0; l < d.M.Buf.LineCount() && len(out) < limit; l++ {
		line := d.M.Buf.Line(l)
		start := -1
		for i, ch := range line + " " {
			if isWordRune(ch) {
				if start < 0 {
					start = i
				}
				continue
			}
			if start >= 0 {
				w := line[start:i]
				start = -1
				f, _ := utf8.DecodeRuneInString(w)
				if len(w) >= 3 && unicode.ToLower(f) == lr && !seen[w] {
					seen[w] = true
					out = append(out, w)
				}
			}
		}
	}
	return out
}

func (c *Completer) hide() {
	c.visible = false
	c.items = nil
}

func (c *Completer) move(dir int) {
	if len(c.items) > 0 {
		c.index = (c.index + dir + len(c.items)) % len(c.items)
	}
}

var snippetField = regexp.MustCompile(`\$\{(\d+)(?::([^}]*))?\}|\$(\d+)`)

// accept replaces the prefix at every caret with the chosen completion.
func (c *Completer) accept() {
	d := c.doc
	if d == nil || c.index >= len(c.items) {
		c.hide()
		return
	}
	it := c.items[c.index]
	if it.snippet && strings.Contains(it.apply, "$") {
		c.hide()
		c.a.insertSnippet(d, it.apply, c.prefix)
		return
	}
	text, caret := it.apply, -1
	if it.snippet || strings.Contains(text, "${") {
		// Fields become their placeholder text; the caret goes to the first.
		var sb strings.Builder
		last := 0
		for _, m := range snippetField.FindAllStringSubmatchIndex(text, -1) {
			sb.WriteString(text[last:m[0]])
			if caret < 0 {
				caret = sb.Len()
			}
			if m[4] >= 0 {
				sb.WriteString(text[m[4]:m[5]])
			}
			last = m[1]
		}
		sb.WriteString(text[last:])
		text = strings.ReplaceAll(sb.String(), `\$`, "$")
	}
	m := d.M
	var edits []buffer.Edit
	for _, r := range m.Sels {
		h := r.Head
		line := m.Buf.Line(h.Line)
		from := h.Col
		if strings.HasSuffix(line[:h.Col], c.prefix) {
			from = h.Col - len(c.prefix)
		}
		edits = append(edits, buffer.Edit{Start: buffer.Pos{Line: h.Line, Col: from}, End: h, Text: text})
	}
	sels := make([]buffer.Range, len(m.Sels))
	for i, r := range m.Sels {
		sels[i] = buffer.Cursor(r.Head)
	}
	m.Buf.BreakUndoGroup()
	out := m.Buf.Apply(edits, sels, buffer.ApplyOptions{Kind: "complete"})
	if caret >= 0 {
		back := len(text) - caret
		for i := range out {
			p := out[i].Head
			if p.Col >= back && !strings.Contains(text[caret:], "\n") {
				out[i] = buffer.Cursor(buffer.Pos{Line: p.Line, Col: p.Col - back})
			}
		}
	}
	m.SetSels(out, m.Main)
	c.hide()
	c.a.afterEdit(d)
}

// Layout draws the list under the main caret (editor coordinates).
func (c *Completer) Layout(gtx layout.Context, d *Doc, g editorGeom) {
	if !c.visible || c.doc != d || len(c.items) == 0 {
		return
	}
	a := c.a
	if c.ver != d.M.Buf.Version() || prefixAt(d) != c.prefix {
		c.hide()
		return
	}
	px := a.uiPx(gtx)
	mpx := g.met.Px
	rowH := gtx.Dp(22)
	n := min(len(c.items), 8)
	first := max(0, min(c.index-n+1, len(c.items)-n))
	w := gtx.Dp(200)
	for _, it := range c.items[first : first+n] {
		w = max(w, a.fonts.Width(it.label, a.fonts.Mono, mpx)+a.fonts.Width(it.detail, a.fonts.UI, px-gtx.Dp(2))+gtx.Dp(40))
	}
	w = min(w, gtx.Dp(520))
	h := n*rowH + gtx.Dp(8)
	x := d.caretPx.X - int(float32(utf8.RuneCountInString(c.prefix))*g.met.Advance) - gtx.Dp(8)
	y := d.caretPx.Y + d.caretDescent + gtx.Dp(6)
	if y+h > g.rect.Dy() {
		y = d.caretPx.Y - d.caretAscent - h - gtx.Dp(6)
	}
	x = max(0, min(x, g.rect.Dx()-w-gtx.Dp(4)))
	box := image.Rect(x, y, x+w, y+h)
	fillRound(gtx.Ops, box.Add(image.Pt(0, gtx.Dp(2))), gtx.Dp(6), a.chrome.Shadow)
	fillRound(gtx.Ops, box, gtx.Dp(6), a.chrome.Overlay)
	stroke(gtx.Ops, box, gtx.Dp(6), 1, a.chrome.Border)
	for i := 0; i < n; i++ {
		it := c.items[first+i]
		ry := y + gtx.Dp(4) + i*rowH
		if first+i == c.index {
			fillRound(gtx.Ops, image.Rect(x+gtx.Dp(4), ry, x+w-gtx.Dp(4), ry+rowH), gtx.Dp(4), a.chrome.OverlaySel)
		}
		base := ry + rowH/2 + g.met.Ascent/2 - gtx.Dp(1)
		a.fonts.Draw(gtx.Ops, it.label, x+gtx.Dp(12), base, a.chrome.OverlayFg, a.fonts.Mono, mpx)
		if it.detail != "" {
			dw := a.fonts.Width(it.detail, a.fonts.UI, px-gtx.Dp(2))
			a.fonts.Draw(gtx.Ops, it.detail, x+w-dw-gtx.Dp(12), base, a.chrome.OverlayDim, a.fonts.UI, px-gtx.Dp(2))
		}
	}
}
