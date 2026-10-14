// Package theme defines color schemes (text) and the chrome colors derived
// from them.
package theme

import (
	"image/color"
	"sort"
	"strconv"
	"strings"
)

// Scheme holds text colors. Token colors are keyed by role names used by
// the syntax highlighter.
type Scheme struct {
	Name                     string
	Dark                     bool
	Bg, Fg, Caret            color.NRGBA
	Selection                color.NRGBA
	LineHighlight            color.NRGBA
	Gutter, GutterActive     color.NRGBA
	Guide                    color.NRGBA
	FindBg                   color.NRGBA
	Added, Modified, Deleted color.NRGBA
	Tokens                   map[string]color.NRGBA
	ItalicComments           bool
}

// Hex parses "#rrggbb" or "#rrggbbaa".
func Hex(s string) color.NRGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	v, _ := strconv.ParseUint(s, 16, 32)
	if len(s) == 8 {
		return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}
	}
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

// Mix blends a toward b by t (0..1).
func Mix(a, b color.NRGBA, t float32) color.NRGBA {
	f := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), f(a.A, b.A)}
}

// Alpha returns c with alpha a (0..1).
func Alpha(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(a * 255)
	return c
}

type spec struct {
	dark                                        bool
	bg, fg, caret, sel, line, gutter, gutterAct string
	guide, find, added, modified, deleted       string
	tokens                                      map[string]string
	italic                                      bool
}

var specs = map[string]spec{
	"Mariner": {true, "#303841", "#d8dee9", "#f9ae58", "#4f5b66", "#3a434c", "#7d8692", "#d8dee9", "#434c56", "#f9ae58", "#99c794", "#6699cc", "#ec5f66", map[string]string{
		"comment": "#a6acb9", "keyword": "#c695c6", "storage": "#c695c6", "string": "#99c794", "number": "#f9ae58",
		"constant": "#ec5f66", "function": "#5fb4b4", "call": "#6699cc", "type": "#f9ae58", "tag": "#ec5f66",
		"attribute": "#c695c6", "operator": "#f97b58", "builtin": "#5fb4b4", "punctuation": "#d8dee9", "heading": "#6699cc",
		"variable": "#d8dee9", "escape": "#5fb4b4", "invalid": "#ec5f66"}, false},
	"Monokai": {true, "#272822", "#f8f8f2", "#f8f8f0", "#49483e", "#3e3d32", "#90908a", "#f8f8f2", "#3b3a32", "#ffe792", "#a6e22e", "#e6db74", "#f92672", map[string]string{
		"comment": "#75715e", "keyword": "#f92672", "storage": "#66d9ef", "string": "#e6db74", "number": "#ae81ff",
		"constant": "#ae81ff", "function": "#a6e22e", "call": "#66d9ef", "type": "#66d9ef", "tag": "#f92672",
		"attribute": "#a6e22e", "operator": "#f92672", "builtin": "#66d9ef", "punctuation": "#f8f8f2", "heading": "#a6e22e",
		"variable": "#f8f8f2", "escape": "#ae81ff", "invalid": "#f92672"}, false},
	"Sixteen Ocean": {true, "#2b303b", "#c0c5ce", "#c0c5ce", "#4f5b66", "#343d46", "#65737e", "#c0c5ce", "#3b4252", "#ebcb8b", "#a3be8c", "#8fa1b3", "#bf616a", map[string]string{
		"comment": "#65737e", "keyword": "#b48ead", "storage": "#b48ead", "string": "#a3be8c", "number": "#d08770",
		"constant": "#d08770", "function": "#8fa1b3", "call": "#8fa1b3", "type": "#ebcb8b", "tag": "#bf616a",
		"attribute": "#d08770", "operator": "#c0c5ce", "builtin": "#96b5b4", "punctuation": "#c0c5ce", "heading": "#8fa1b3",
		"variable": "#c0c5ce", "escape": "#96b5b4", "invalid": "#bf616a"}, false},
	"Solarized Dark": {true, "#002b36", "#93a1a1", "#d30102", "#0d3e4b", "#073642", "#4f6a72", "#93a1a1", "#0e3f4b", "#b58900", "#859900", "#268bd2", "#dc322f", map[string]string{
		"comment": "#586e75", "keyword": "#859900", "storage": "#93a1a1", "string": "#2aa198", "number": "#d33682",
		"constant": "#b58900", "function": "#268bd2", "call": "#268bd2", "type": "#cb4b16", "tag": "#268bd2",
		"attribute": "#93a1a1", "operator": "#859900", "builtin": "#b58900", "punctuation": "#93a1a1", "heading": "#268bd2",
		"variable": "#268bd2", "escape": "#dc322f", "invalid": "#dc322f"}, false},
	"Breakwater": {false, "#fafbfc", "#333b44", "#e07a2c", "#dbe3ec", "#eef1f5", "#9aa3ad", "#333b44", "#e2e6eb", "#ffd56b", "#4f9a4a", "#3d78b8", "#d0464f", map[string]string{
		"comment": "#9399a1", "keyword": "#9b5fb5", "storage": "#9b5fb5", "string": "#4f9a4a", "number": "#d3762a",
		"constant": "#d0464f", "function": "#2c8e8e", "call": "#3d78b8", "type": "#c8820f", "tag": "#d0464f",
		"attribute": "#9b5fb5", "operator": "#d65c3a", "builtin": "#2c8e8e", "punctuation": "#333b44", "heading": "#3d78b8",
		"variable": "#333b44", "escape": "#2c8e8e", "invalid": "#d0464f"}, false},
	"Celestine": {false, "#ffffff", "#383a42", "#526fff", "#d9dcf4", "#f3f4f8", "#a0a1a7", "#383a42", "#eaeaee", "#ffe48a", "#50a14f", "#4078f2", "#e45649", map[string]string{
		"comment": "#a0a1a7", "keyword": "#a626a4", "storage": "#a626a4", "string": "#50a14f", "number": "#986801",
		"constant": "#0184bc", "function": "#4078f2", "call": "#4078f2", "type": "#c18401", "tag": "#e45649",
		"attribute": "#986801", "operator": "#0184bc", "builtin": "#0184bc", "punctuation": "#383a42", "heading": "#e45649",
		"variable": "#383a42", "escape": "#0184bc", "invalid": "#e45649"}, true},
	"Solarized Light": {false, "#fdf6e3", "#586e75", "#d30102", "#eee8d5", "#f5efdc", "#a8a8a0", "#586e75", "#ece6d2", "#f3d97a", "#859900", "#268bd2", "#dc322f", map[string]string{
		"comment": "#93a1a1", "keyword": "#859900", "storage": "#586e75", "string": "#2aa198", "number": "#d33682",
		"constant": "#b58900", "function": "#268bd2", "call": "#268bd2", "type": "#cb4b16", "tag": "#268bd2",
		"attribute": "#586e75", "operator": "#859900", "builtin": "#b58900", "punctuation": "#586e75", "heading": "#268bd2",
		"variable": "#268bd2", "escape": "#dc322f", "invalid": "#dc322f"}, false},
}

// Names lists the built-in schemes.
func Names() []string {
	var out []string
	for k := range specs {
		out = append(out, k)
	}
	for _, k := range externalNames() {
		if _, dup := specs[k]; !dup {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Get returns a scheme by name (Mariner if unknown).
func Get(name string) *Scheme {
	name = SchemeName(name)
	if s := lookupExternal(name); s != nil {
		return s
	}
	sp, ok := specs[name]
	if !ok {
		name, sp = "Mariner", specs["Mariner"]
	}
	s := &Scheme{
		Name: name, Dark: sp.dark, Bg: Hex(sp.bg), Fg: Hex(sp.fg), Caret: Hex(sp.caret),
		Selection: Hex(sp.sel), LineHighlight: Hex(sp.line), Gutter: Hex(sp.gutter), GutterActive: Hex(sp.gutterAct),
		Guide: Hex(sp.guide), FindBg: Hex(sp.find), Added: Hex(sp.added), Modified: Hex(sp.modified), Deleted: Hex(sp.deleted),
		Tokens: map[string]color.NRGBA{}, ItalicComments: sp.italic,
	}
	for k, v := range sp.tokens {
		s.Tokens[k] = Hex(v)
	}
	return s
}

// Token returns the color for a role, falling back to the foreground.
func (s *Scheme) Token(role string) color.NRGBA {
	if c, ok := s.Tokens[role]; ok {
		return c
	}
	return s.Fg
}

// Chrome holds the UI colors derived from a scheme ("Adaptive" theme).
type Chrome struct {
	Sidebar, SidebarFg, SidebarDim, SidebarSel, SidebarHover color.NRGBA
	TabBar, Tab, TabHover, TabFg, TabActiveFg                color.NRGBA
	Status, StatusFg                                         color.NRGBA
	Panel, Input, Border, Accent                             color.NRGBA
	Overlay, OverlaySel, OverlayFg, OverlayDim, Match        color.NRGBA
	Shadow                                                   color.NRGBA
}

// ChromeFor derives chrome colors from a scheme.
func ChromeFor(s *Scheme) Chrome {
	black := color.NRGBA{0, 0, 0, 255}
	white := color.NRGBA{255, 255, 255, 255}
	k := func(dark, light float32) float32 {
		if s.Dark {
			return dark
		}
		return light
	}
	sidebar := Mix(s.Bg, black, k(0.22, 0.06))
	c := Chrome{
		Sidebar:     sidebar,
		SidebarFg:   Mix(s.Fg, s.Bg, 0.18),
		TabBar:      Mix(s.Bg, black, k(0.3, 0.1)),
		Tab:         Mix(s.Bg, black, k(0.14, 0.05)),
		TabFg:       Mix(s.Fg, s.Bg, 0.4),
		TabActiveFg: s.Fg,
		Status:      Mix(s.Bg, black, k(0.3, 0.1)),
		StatusFg:    Mix(s.Fg, s.Bg, 0.25),
		Panel:       Mix(s.Bg, black, k(0.12, 0.04)),
		Border:      Mix(s.Bg, black, k(0.45, 0.18)),
		Accent:      s.Token("call"),
		OverlayFg:   s.Fg,
		Match:       s.Caret,
	}
	if s.Dark {
		c.Input = Mix(s.Bg, black, 0.25)
		c.Overlay = Mix(s.Bg, white, 0.04)
		c.OverlaySel = Mix(s.Bg, white, 0.12)
		c.OverlayDim = Alpha(white, 0.5)
		c.Shadow = color.NRGBA{0, 0, 0, 130}
	} else {
		c.Input = white
		c.Overlay = Mix(s.Bg, black, 0.04)
		c.OverlaySel = Mix(s.Bg, black, 0.08)
		c.OverlayDim = Alpha(black, 0.5)
		c.Shadow = color.NRGBA{0, 0, 0, 50}
	}
	c.SidebarDim = Mix(c.SidebarFg, c.Sidebar, 0.4)
	c.SidebarSel = Mix(c.Sidebar, c.SidebarFg, 0.14)
	c.SidebarHover = Mix(c.Sidebar, c.SidebarFg, 0.06)
	c.TabHover = Mix(c.Tab, s.Fg, 0.05)
	return c
}
