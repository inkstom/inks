package ui

import (
	"image"
	"image/color"
	"image/draw"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"inks/internal/editor"
	"inks/internal/theme"
)

// The minimap: a tiny rendering of the document at the right edge, drawn on
// the CPU into an image that is cached until the text, scroll window or
// colors change. Click or drag it to scroll.

type minimapCache struct {
	key   [6]int
	theme string
	img   *image.RGBA
	op    paint.ImageOp
}

type minimapTag struct{ _ int }

func (a *App) layoutMinimap(gtx layout.Context, d *Doc, g editorGeom, firstRow, viewRows int, maxScroll float32) {
	s := a.scheme
	size := g.rect.Size()
	x0 := g.textRight()
	w := g.miniW
	rowPx := max(2, gtx.Dp(2))
	charPx := max(1, gtx.Dp(1))
	total := g.rows.total()
	mapRows := size.Y / rowPx

	// Which rows the minimap shows: it scrolls proportionally when the
	// document is taller than the minimap.
	mFirst := 0
	if total > mapRows && maxScroll > 0 {
		mFirst = int(float32(total-mapRows) * (d.scrollY / maxScroll))
		mFirst = min(max(mFirst, 0), total-mapRows)
	}
	fill(gtx.Ops, image.Rect(x0, 0, size.X, size.Y), theme.Mix(s.Bg, s.Fg, 0.02))

	// Pointer: click or drag to scroll.
	miniTag := &a.groups[a.drawGroup].miniTag
	area := clip.Rect(image.Rect(x0, 0, size.X, size.Y)).Push(gtx.Ops)
	pointer.CursorDefault.Add(gtx.Ops)
	event.Op(gtx.Ops, miniTag)
	area.Pop()
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: miniTag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30}})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Scroll:
			d.scrollY += e.Scroll.Y
		case pointer.Press, pointer.Drag:
			row := mFirst + int(e.Position.Y)/rowPx
			d.scrollY = float32((row - viewRows/2) * g.lineH)
			d.scrollY = min(max(d.scrollY, 0), maxScroll)
		}
		a.win.Invalidate()
	}

	// Render (or reuse) the image.
	n := min(mapRows, total-mFirst)
	key := [6]int{d.M.Buf.Version(), d.hl.Version(), mFirst, w, n, g.rows.width}
	c := d.mini
	if c == nil || c.key != key || c.theme != s.Name || c.img == nil || c.img.Rect.Dy() != size.Y {
		if c == nil {
			c = &minimapCache{}
			d.mini = c
		}
		c.key, c.theme = key, s.Name
		c.img = a.renderMinimap(d, g, mFirst, n, w, size.Y, rowPx, charPx)
		c.op = paint.NewImageOp(c.img)
	}
	off := op.Offset(image.Pt(x0, 0)).Push(gtx.Ops)
	c.op.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	off.Pop()

	// The visible region.
	vy := (firstRow - mFirst) * rowPx
	vh := max(viewRows*rowPx, gtx.Dp(8))
	fill(gtx.Ops, image.Rect(x0, vy, size.X, vy+vh), theme.Alpha(s.Fg, 0.08))
	fill(gtx.Ops, image.Rect(x0, 0, x0+1, size.Y), theme.Alpha(s.Fg, 0.06))
}

func (a *App) renderMinimap(d *Doc, g editorGeom, first, n, w, h, rowPx, charPx int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.Transparent, image.Point{}, draw.Src)
	s := a.scheme
	tab := d.M.TabSize
	b := d.M.Buf
	vis := g.rows.visible(b, first, n, rowPx, 0, tab)
	maxCols := w/charPx - 2
	blend := func(c color.NRGBA) color.RGBA {
		c = theme.Mix(s.Bg, c, 0.7)
		return color.RGBA{c.R, c.G, c.B, 255}
	}
	for _, r := range vis {
		line := b.Line(r.line)
		if r.from >= r.to {
			continue
		}
		spans := d.hl.Line(r.line)
		si := 0
		y := r.y + 1
		hpx := max(1, rowPx-1)
		v := editor.VisualCol(line, r.from, tab)
		for i := r.from; i < r.to; {
			ch := line[i]
			size := 1
			if ch >= 0x80 {
				for size = 1; i+size < r.to && line[i+size]&0xC0 == 0x80; size++ {
				}
			}
			col := v - r.vstart
			if ch == '\t' {
				v += tab - v%tab
			} else {
				v++
			}
			if col >= maxCols {
				break
			}
			if ch != ' ' && ch != '\t' {
				for si < len(spans) && spans[si].End <= i {
					si++
				}
				fg := s.Fg
				if si < len(spans) && spans[si].Start <= i {
					fg = s.Token(spans[si].Role)
				}
				px := blend(fg)
				x := (col + 1) * charPx
				for yy := y; yy < y+hpx && yy < h; yy++ {
					for xx := x; xx < x+charPx && xx < w; xx++ {
						img.SetRGBA(xx, yy, px)
					}
				}
			}
			i += size
		}
	}
	return img
}
