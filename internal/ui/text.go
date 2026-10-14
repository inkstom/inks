package ui

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"
)

// Fonts shapes and paints text. System fonts (e.g. Menlo) are available by
// name; the Go fonts are the fallback.
type Fonts struct {
	Shaper *text.Shaper
	Mono   font.Font
	UI     font.Font
	glyphs []text.Glyph
}

func newFonts(mono string) *Fonts {
	f := &Fonts{Shaper: text.NewShaper(text.WithCollection(gofont.Collection()))}
	f.SetMono(mono)
	f.UI = font.Font{Typeface: "Helvetica Neue, Go"}
	return f
}

// SetMono picks the editor typeface ("Menlo" by default, Go Mono as fallback).
func (f *Fonts) SetMono(name string) {
	if name == "" {
		name = "Menlo"
	}
	f.Mono = font.Font{Typeface: font.Typeface(name + ", Go Mono")}
}

// Metrics describes a monospace font at a pixel size.
type Metrics struct {
	Px      int
	Advance float32 // width of one column
	Ascent  int
	Descent int
}

func (f *Fonts) metrics(fnt font.Font, px int) Metrics {
	f.Shaper.LayoutString(text.Parameters{Font: fnt, PxPerEm: fixed.I(px), MaxLines: 1, MaxWidth: 1 << 20}, "MMMMMMMMMM")
	m := Metrics{Px: px}
	var adv fixed.Int26_6
	n := 0
	for {
		g, ok := f.Shaper.NextGlyph()
		if !ok {
			break
		}
		adv += g.Advance
		n++
		if m.Ascent == 0 {
			m.Ascent = g.Ascent.Ceil()
			m.Descent = g.Descent.Ceil()
		}
	}
	if n > 0 {
		m.Advance = float32(adv) / 64 / float32(n)
	} else {
		m.Advance = float32(px) * 0.6
	}
	if m.Ascent == 0 {
		m.Ascent = px * 8 / 10
		m.Descent = px * 2 / 10
	}
	return m
}

// shape lays out s on one line and returns its glyphs and width.
func (f *Fonts) shape(s string, fnt font.Font, px int) ([]text.Glyph, int) {
	f.Shaper.LayoutString(text.Parameters{Font: fnt, PxPerEm: fixed.I(px), MaxLines: 1, MaxWidth: 1 << 24}, s)
	f.glyphs = f.glyphs[:0]
	var w fixed.Int26_6
	for {
		g, ok := f.Shaper.NextGlyph()
		if !ok {
			break
		}
		if g.Flags&text.FlagParagraphBreak != 0 {
			continue
		}
		f.glyphs = append(f.glyphs, g)
		w += g.Advance
	}
	return f.glyphs, w.Ceil()
}

// Draw paints s with its baseline at (x, y) and returns its width.
func (f *Fonts) Draw(ops *op.Ops, s string, x, y int, col color.NRGBA, fnt font.Font, px int) int {
	if s == "" {
		return 0
	}
	glyphs, w := f.shape(s, fnt, px)
	if len(glyphs) == 0 {
		return w
	}
	off := op.Offset(image.Pt(x, y)).Push(ops)
	path := f.Shaper.Shape(glyphs)
	outline := clip.Outline{Path: path}.Op().Push(ops)
	paint.ColorOp{Color: col}.Add(ops)
	paint.PaintOp{}.Add(ops)
	outline.Pop()
	// Emoji and other bitmap glyphs.
	f.Shaper.Bitmaps(glyphs).Add(ops)
	off.Pop()
	return w
}

// Width measures s.
func (f *Fonts) Width(s string, fnt font.Font, px int) int {
	_, w := f.shape(s, fnt, px)
	return w
}

// Elide shortens s with an ellipsis to fit max pixels.
func (f *Fonts) Elide(s string, fnt font.Font, px, max int) string {
	if f.Width(s, fnt, px) <= max {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if f.Width(string(r)+"…", fnt, px) <= max {
			return string(r) + "…"
		}
	}
	return "…"
}

// fill paints a rectangle.
func fill(ops *op.Ops, r image.Rectangle, c color.NRGBA) {
	paint.FillShape(ops, c, clip.Rect(r).Op())
}

// fillRound paints a rounded rectangle.
func fillRound(ops *op.Ops, r image.Rectangle, radius int, c color.NRGBA) {
	paint.FillShape(ops, c, clip.UniformRRect(r, radius).Op(ops))
}

// stroke paints a rectangle outline.
func stroke(ops *op.Ops, r image.Rectangle, radius int, width float32, c color.NRGBA) {
	paint.FillShape(ops, c, clip.Stroke{Path: clip.UniformRRect(r, radius).Path(ops), Width: width}.Op())
}

func colorMaterial(ops *op.Ops, c color.NRGBA) op.CallOp {
	m := op.Record(ops)
	paint.ColorOp{Color: c}.Add(ops)
	return m.Stop()
}
