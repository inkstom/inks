package ui

import (
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
)

// Split layouts: the editor area holds one or more groups, each with its own
// tabs. The focused group's documents live in a.docs / a.active (so commands
// act on it); the others are parked in their Group until focused or drawn.

// Group is one pane of the layout.
type Group struct {
	docs         []*Doc
	active       int
	tag          *editorTagT
	rect         image.Rectangle
	tabTags      [64]tabTag
	tabCloseTags [64]tabCloseTag
	miniTag      minimapTag
	tabScroll    int
	tabRects     []image.Rectangle // tab positions in the tab bar, last frame
	tabClickIdx  int
	tabClickAt   time.Time
}

// Layouts: cells as fractions of the editor area.
var layouts = map[string][][4]float32{
	"single":    {{0, 0, 1, 1}},
	"columns_2": {{0, 0, .5, 1}, {.5, 0, 1, 1}},
	"columns_3": {{0, 0, 1. / 3, 1}, {1. / 3, 0, 2. / 3, 1}, {2. / 3, 0, 1, 1}},
	"rows_2":    {{0, 0, 1, .5}, {0, .5, 1, 1}},
	"grid_4":    {{0, 0, .5, .5}, {.5, 0, 1, .5}, {0, .5, .5, 1}, {.5, .5, 1, 1}},
	"columns_4": {{0, 0, .25, 1}, {.25, 0, .5, 1}, {.5, 0, .75, 1}, {.75, 0, 1, 1}},
	"rows_3":    {{0, 0, 1, 1. / 3}, {0, 1. / 3, 1, 2. / 3}, {0, 2. / 3, 1, 1}},
}

func (a *App) initGroups() {
	a.layoutName = "single"
	a.groups = []*Group{{tag: a.editorTag}}
	a.focus = 0
}

// syncFocus parks a.docs / a.active in the focused group.
func (a *App) syncFocus() {
	g := a.groups[a.focus]
	g.docs, g.active = a.docs, a.active
}

// setFocus makes group i the one commands and keys act on.
func (a *App) setFocus(i int) {
	if i < 0 || i >= len(a.groups) || i == a.focus {
		return
	}
	a.syncFocus()
	a.focus = i
	g := a.groups[i]
	a.docs, a.active, a.editorTag = g.docs, g.active, g.tag
	a.focusEditor = true
}

// allDocs returns every open document in every group.
func (a *App) allDocs() []*Doc {
	if len(a.groups) <= 1 {
		return a.docs
	}
	var out []*Doc
	for i, g := range a.groups {
		if i == a.focus {
			out = append(out, a.docs...)
		} else {
			out = append(out, g.docs...)
		}
	}
	return out
}

// groupOf finds the group and index of a document.
func (a *App) groupOf(d *Doc) (int, int) {
	for gi, g := range a.groups {
		docs := g.docs
		if gi == a.focus {
			docs = a.docs
		}
		for i, x := range docs {
			if x == d {
				return gi, i
			}
		}
	}
	return -1, -1
}

// isOpen reports whether d is open in any group.
func (a *App) isOpen(d *Doc) bool {
	g, _ := a.groupOf(d)
	return g >= 0
}

// activate shows d, focusing its group.
func (a *App) activate(d *Doc) bool {
	gi, i := a.groupOf(d)
	if gi < 0 {
		return false
	}
	a.setFocus(gi)
	a.active = i
	a.focusEditor = true
	return true
}

// setLayout switches the layout, moving documents out of removed groups.
func (a *App) setLayout(name string) {
	cells, ok := layouts[name]
	if !ok {
		return
	}
	a.syncFocus()
	n := len(cells)
	for len(a.groups) < n {
		a.groups = append(a.groups, &Group{tag: &editorTagT{}})
	}
	if len(a.groups) > n {
		last := a.groups[n-1]
		for _, g := range a.groups[n:] {
			last.docs = append(last.docs, g.docs...)
		}
		a.groups = a.groups[:n]
	}
	a.layoutName = name
	if a.focus >= n {
		a.focus = n - 1
	}
	g := a.groups[a.focus]
	if g.active >= len(g.docs) {
		g.active = len(g.docs) - 1
	}
	a.docs, a.active, a.editorTag = g.docs, g.active, g.tag
	a.focusEditor = true
}

// moveToGroup moves the active document to group i (creating columns as needed).
func (a *App) moveToGroup(i int) {
	d := a.doc()
	if d == nil || i == a.focus || i < 0 {
		return
	}
	if i >= len(a.groups) {
		switch {
		case i < 2:
			a.setLayout("columns_2")
		case i < 3:
			a.setLayout("columns_3")
		default:
			a.setLayout("grid_4")
		}
	}
	if i >= len(a.groups) {
		return
	}
	idx := a.indexOf(d)
	a.docs = append(a.docs[:idx], a.docs[idx+1:]...)
	if a.active >= len(a.docs) {
		a.active = len(a.docs) - 1
	}
	a.syncFocus()
	t := a.groups[i]
	t.docs = append(t.docs, d)
	t.active = len(t.docs) - 1
	a.setFocus(i)
}

// withGroup runs fn with group i's documents swapped in as the current ones.
func (a *App) withGroup(i int, fn func()) {
	a.drawGroup = i
	if i == a.focus {
		fn()
		return
	}
	docs, active, tag := a.docs, a.active, a.editorTag
	g := a.groups[i]
	a.docs, a.active, a.editorTag = g.docs, g.active, g.tag
	fn()
	g.docs, g.active = a.docs, a.active
	a.docs, a.active, a.editorTag = docs, active, tag
}

// layoutGroups draws every group's tabs and editor.
func (a *App) layoutGroups(gtx layout.Context) layout.Dimensions {
	size := gtx.Constraints.Max
	cells := layouts[a.layoutName]
	if cells == nil || len(cells) != len(a.groups) {
		cells = layouts["single"]
	}
	clickFocus := -1
	for i, cell := range cells {
		r := image.Rect(int(cell[0]*float32(size.X)), int(cell[1]*float32(size.Y)), int(cell[2]*float32(size.X)), int(cell[3]*float32(size.Y)))
		g := a.groups[i]
		g.rect = r
		a.withGroup(i, func() {
			a.pressed = false
			off := op.Offset(r.Min).Push(gtx.Ops)
			gg := gtx
			gg.Constraints = layout.Exact(r.Size())
			layout.Flex{Axis: layout.Vertical}.Layout(gg,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if a.distractionFree {
						return layout.Dimensions{}
					}
					return a.layoutTabs(gtx)
				}),
				layout.Flexed(1, a.layoutEditorArea),
			)
			off.Pop()
			if a.pressed && i != a.focus {
				clickFocus = i
			}
		})
		// Borders between groups.
		if r.Min.X > 0 {
			fill(gtx.Ops, image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), a.chrome.Border)
		}
		if r.Min.Y > 0 {
			fill(gtx.Ops, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), a.chrome.Border)
		}
	}
	a.drawGroup = a.focus
	if clickFocus >= 0 {
		a.setFocus(clickFocus)
	}
	a.finishTabActions()
	return layout.Dimensions{Size: size}
}
