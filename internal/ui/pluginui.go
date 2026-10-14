package ui

import (
	"image"
	"strings"
	"sync/atomic"

	"gioui.org/font"
	"gioui.org/layout"

	"inks/internal/buffer"
	"inks/internal/theme"
)

// Editor features used by plugins: per-view settings, regions (highlights,
// underlines, outlines), phantoms (inline notes), popups and status text.

var docIDs atomic.Int64

func nextDocID() int { return int(docIDs.Add(1)) }

type regionSet struct {
	ranges []buffer.Range
	scope  string
	flags  int
}

type phantom struct {
	pos  buffer.Pos
	text string
}

type popup struct {
	doc  *Doc
	pos  buffer.Pos
	text string
}

// trackEdits keeps plugin regions and phantoms in place as text changes.
func (d *Doc) trackEdits() {
	d.M.Buf.EditHooks = append(d.M.Buf.EditHooks, func(e buffer.Edit, newEnd buffer.Pos) {
		for _, rs := range d.regions {
			for i := range rs.ranges {
				rs.ranges[i].Anchor = buffer.MapPos(rs.ranges[i].Anchor, e, newEnd, -1)
				rs.ranges[i].Head = buffer.MapPos(rs.ranges[i].Head, e, newEnd, 1)
			}
		}
		if len(d.folds) > 0 {
			// Edits inside a fold open it; others shift it.
			var keep []fold
			for _, f := range d.folds {
				if !(e.End.Less(f.a) || f.b.Less(e.Start)) {
					d.foldVer++
					continue
				}
				f.a = buffer.MapPos(f.a, e, newEnd, -1)
				f.b = buffer.MapPos(f.b, e, newEnd, 1)
				keep = append(keep, f)
			}
			d.foldVer++
			d.folds = keep
		}
		for i := range d.bookmarks {
			d.bookmarks[i] = buffer.MapPos(d.bookmarks[i], e, newEnd, -1)
		}
		for i := range d.marks {
			d.marks[i] = buffer.MapPos(d.marks[i], e, newEnd, -1)
		}
		for _, list := range d.phantoms {
			for i := range list {
				list[i].pos = buffer.MapPos(list[i].pos, e, newEnd, -1)
			}
		}
	})
}

// setting returns a view setting: an override, a document property, or the
// global value.
func (d *Doc) setting(a *App, key string) any {
	if v, ok := d.overrides[key]; ok {
		return v
	}
	switch key {
	case "tab_size":
		return d.M.TabSize
	case "translate_tabs_to_spaces":
		return d.M.UseSpaces
	case "word_wrap":
		return d.wordWrap(a)
	case "syntax":
		return "Packages/" + d.Lang + "/" + d.Lang + ".inks-syntax"
	case "is_widget":
		return false
	}
	return a.set.Get(key)
}

func (d *Doc) setOverride(a *App, key string, v any) {
	if d.overrides == nil {
		d.overrides = map[string]any{}
	}
	d.overrides[key] = v
	switch key {
	case "tab_size":
		if n, ok := v.(float64); ok && n > 0 {
			d.M.TabSize = int(n)
		}
	case "translate_tabs_to_spaces":
		if b, ok := v.(bool); ok {
			d.M.UseSpaces = b
		}
	case "word_wrap":
		if b, ok := v.(bool); ok {
			d.wrapOverride = &b
		}
	case "auto_indent":
		if b, ok := v.(bool); ok {
			d.M.AutoIndent = b
		}
	}
}

// pluginStatus joins the status values plugins set on a view.
func (d *Doc) pluginStatus() string {
	if len(d.status) == 0 {
		return ""
	}
	keys := make([]string, 0, len(d.status))
	for k := range d.status {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var parts []string
	for _, k := range keys {
		if v := d.status[k]; v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "  ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// drawRegions paints plugin regions on the visible rows.
func (a *App) drawRegions(gtx layout.Context, d *Doc, rangeOnRows func(st, en buffer.Pos, paint func(image.Rectangle, bool))) {
	if len(d.regions) == 0 {
		return
	}
	const (
		drawEmpty      = 1
		noFill         = 32
		hidden         = 128
		noOutline      = 256
		solidUnderline = 512
		stippled       = 1024
		squiggly       = 2048
		anyUnderline   = solidUnderline | stippled | squiggly
	)
	for _, rs := range d.regions {
		if rs.flags&hidden != 0 {
			continue
		}
		c := a.scheme.Token(roleForScope(rs.scope))
		switch {
		case strings.Contains(rs.scope, "error") || strings.Contains(rs.scope, "invalid"):
			c = a.scheme.Deleted
		case strings.Contains(rs.scope, "warning"):
			c = a.scheme.Modified
		case roleForScope(rs.scope) == "":
			c = a.scheme.FindBg
		}
		for _, r := range rs.ranges {
			st, en := r.Start(), r.End()
			if st == en {
				if rs.flags&drawEmpty == 0 {
					continue
				}
				en.Col++
			}
			rangeOnRows(st, en, func(rr image.Rectangle, _ bool) {
				switch {
				case rs.flags&anyUnderline != 0:
					y := rr.Max.Y - gtx.Dp(3)
					if rs.flags&squiggly != 0 {
						step := gtx.Dp(2)
						for x := rr.Min.X; x < rr.Max.X; x += step {
							dy := 0
							if (x-rr.Min.X)/step%2 == 1 {
								dy = step / 2
							}
							fill(gtx.Ops, image.Rect(x, y+dy, min(x+step, rr.Max.X), y+dy+max(1, gtx.Dp(1))), c)
						}
					} else if rs.flags&stippled != 0 {
						for x := rr.Min.X; x < rr.Max.X; x += gtx.Dp(4) {
							fill(gtx.Ops, image.Rect(x, y, min(x+gtx.Dp(2), rr.Max.X), y+max(1, gtx.Dp(1))), c)
						}
					} else {
						fill(gtx.Ops, image.Rect(rr.Min.X, y, rr.Max.X, y+max(1, gtx.Dp(1))), c)
					}
				default:
					if rs.flags&noFill == 0 {
						fillRound(gtx.Ops, rr, gtx.Dp(2), theme.Alpha(c, 0.25))
					}
					if rs.flags&noOutline == 0 && rs.flags&noFill != 0 {
						stroke(gtx.Ops, rr, gtx.Dp(2), 1, c)
					}
				}
			})
		}
	}
}

// drawPhantoms shows phantom text after the end of its line, dimmed.
func (a *App) drawPhantoms(gtx layout.Context, d *Doc, vis []visRow, tx, baseline int, g editorGeom) {
	if len(d.phantoms) == 0 {
		return
	}
	fnt := a.fonts.UI
	fnt.Style = font.Italic
	px := a.uiPx(gtx) - gtx.Dp(1)
	for _, list := range d.phantoms {
		for _, ph := range list {
			for _, r := range vis {
				if r.line != ph.pos.Line || !r.lastOfLine {
					continue
				}
				line := d.M.Buf.Line(r.line)
				x := g.xAt(tx, line, len(line), r, d.M.TabSize) + gtx.Dp(24)
				text := strings.ReplaceAll(ph.text, "\n", "  ")
				w := a.fonts.Width(text, fnt, px) + gtx.Dp(12)
				fillRound(gtx.Ops, image.Rect(x-gtx.Dp(6), r.y+gtx.Dp(2), x+w-gtx.Dp(6), r.y+g.lineH-gtx.Dp(2)), gtx.Dp(4), theme.Alpha(a.scheme.Fg, 0.08))
				a.fonts.Draw(gtx.Ops, text, x, r.y+baseline, theme.Mix(a.scheme.Fg, a.scheme.Bg, 0.35), fnt, px)
			}
		}
	}
}

// drawPopup shows a plugin popup below its position.
func (a *App) drawPopup(gtx layout.Context, d *Doc, g editorGeom, vis []visRow, tx int) {
	p := a.popup
	if p == nil || p.doc != d {
		return
	}
	row, _ := g.rows.rowOf(p.pos)
	if len(vis) == 0 {
		return
	}
	i := row - (int(d.scrollY) / max(1, g.lineH))
	if i < 0 || i >= len(vis) {
		return
	}
	r := vis[i]
	x := g.xAt(tx, d.M.Buf.Line(p.pos.Line), p.pos.Col, r, d.M.TabSize)
	lines := strings.Split(p.text, "\n")
	if len(lines) > 12 {
		lines = lines[:12]
	}
	px := a.uiPx(gtx)
	lh := gtx.Dp(18)
	w := 0
	for _, l := range lines {
		w = max(w, a.fonts.Width(l, a.fonts.UI, px))
	}
	w = min(w+gtx.Dp(20), gtx.Dp(520))
	h := len(lines)*lh + gtx.Dp(12)
	y := r.y + g.lineH + gtx.Dp(2)
	if y+h > g.rect.Dy() {
		y = r.y - h - gtx.Dp(2)
	}
	x = min(x, g.rect.Dx()-w-gtx.Dp(8))
	box := image.Rect(x, y, x+w, y+h)
	fillRound(gtx.Ops, box.Add(image.Pt(0, gtx.Dp(2))), gtx.Dp(6), a.chrome.Shadow)
	fillRound(gtx.Ops, box, gtx.Dp(6), a.chrome.Overlay)
	stroke(gtx.Ops, box, gtx.Dp(6), 1, a.chrome.Border)
	for k, l := range lines {
		a.fonts.Draw(gtx.Ops, a.fonts.Elide(l, a.fonts.UI, px, w-gtx.Dp(20)), x+gtx.Dp(10), y+gtx.Dp(6)+k*lh+lh-gtx.Dp(5), a.chrome.OverlayFg, a.fonts.UI, px)
	}
}
