package ui

import (
	"image"
	"io"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"inks/internal/macmenu"
	"inks/internal/platform"
	"inks/internal/theme"
)

// Tab interactions: click to switch, middle-click to close, right-click for
// a menu, and drag to reorder, to move to another group, or out of the
// window to open the file in a new window.

type tabDrag struct {
	doc    *Doc
	group  int
	start  image.Point // window coordinates
	pos    image.Point
	moving bool
	title  string
}

// tabPointer handles the pointer on tab i (rect r, tab-bar coordinates) of
// the group being drawn.
func (a *App) tabPointer(gtx layout.Context, grp *Group, i int, d *Doc, r image.Rectangle) {
	tag := &grp.tabTags[i%len(grp.tabTags)]
	area := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()
	origin := image.Pt(a.sidebarPx+grp.rect.Min.X, grp.rect.Min.Y)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		pos := origin.Add(e.Position.Round())
		switch e.Kind {
		case pointer.Press:
			a.pressed = true
			a.active = i
			switch {
			case e.Buttons == pointer.ButtonSecondary:
				a.tabMenu(d)
			case e.Buttons == pointer.ButtonTertiary:
				a.pendingClose = d
			default:
				if grp.tabClickIdx == i && gtx.Now.Sub(grp.tabClickAt) < 400*time.Millisecond {
					d.preview = false // double click keeps a preview tab
				}
				grp.tabClickIdx, grp.tabClickAt = i, gtx.Now
				a.focusEditor = true
				a.drag = &tabDrag{doc: d, group: a.drawGroup, start: pos, pos: pos, title: d.Title()}
			}
		case pointer.Drag:
			if dr := a.drag; dr != nil && dr.doc == d {
				dr.pos = pos
				if !dr.moving {
					dx, dy := pos.X-dr.start.X, pos.Y-dr.start.Y
					dr.moving = dx*dx+dy*dy > gtx.Dp(6)*gtx.Dp(6)
				}
				a.win.Invalidate()
			}
		case pointer.Release:
			if dr := a.drag; dr != nil && dr.doc == d {
				dr.pos = pos
				if dr.moving {
					a.pendingDrop = dr // handled once all groups are drawn
				}
				a.drag = nil
			}
		case pointer.Cancel:
			a.drag = nil
		}
	}
}

// finishTabActions runs drops and closes requested while drawing groups.
func (a *App) finishTabActions() {
	if d := a.pendingClose; d != nil {
		a.pendingClose = nil
		if a.activate(d) {
			a.closeDoc(a.indexOf(d), nil)
		}
	}
	if dr := a.pendingDrop; dr != nil {
		a.pendingDrop = nil
		a.dropTab(dr)
	}
}

// dropTab moves a dragged tab to where it was released.
func (a *App) dropTab(dr *tabDrag) {
	p, d := dr.pos, dr.doc
	if !a.isOpen(d) {
		return
	}
	margin := 4
	if p.X < -margin || p.Y < -margin || p.X > a.winSize.X+margin || p.Y > a.winSize.Y+margin {
		// Outside the window: a window of its own, under the pointer.
		a.moveToNewWindow(d, true)
		return
	}
	for gi, g := range a.groups {
		box := g.rect.Add(image.Pt(a.sidebarPx, 0))
		bar := image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+a.tabBarH)
		if p.In(bar) {
			a.moveTab(d, gi, a.insertIndex(g, p.X-box.Min.X))
			return
		}
		if p.In(box) && gi != dr.group {
			a.moveTab(d, gi, -1)
			return
		}
	}
}

// insertIndex is where a tab dropped at x (tab-bar coordinates) goes.
func (a *App) insertIndex(g *Group, x int) int {
	for i, r := range g.tabRects {
		if x < (r.Min.X+r.Max.X)/2 {
			return i
		}
	}
	return len(g.tabRects)
}

// moveTab moves d to position idx of group gi (-1: the end).
func (a *App) moveTab(d *Doc, gi, idx int) {
	sg, si := a.groupOf(d)
	if sg < 0 || gi < 0 || gi >= len(a.groups) {
		return
	}
	a.setFocus(sg)
	a.docs = append(a.docs[:si], a.docs[si+1:]...)
	if sg == gi && idx > si {
		idx--
	}
	if a.active >= len(a.docs) {
		a.active = len(a.docs) - 1
	}
	a.setFocus(gi)
	if idx < 0 || idx > len(a.docs) {
		idx = len(a.docs)
	}
	a.docs = append(a.docs[:idx], append([]*Doc{d}, a.docs[idx:]...)...)
	a.active = idx
	a.focusEditor = true
}

// drawTabDrag draws the dragged tab under the pointer and, over a tab
// bar, where it will land.
func (a *App) drawTabDrag(gtx layout.Context) {
	dr := a.drag
	if dr == nil || !dr.moving {
		return
	}
	c := a.chrome
	px := a.uiPx(gtx)
	// Insertion marker.
	for _, g := range a.groups {
		box := g.rect.Add(image.Pt(a.sidebarPx, 0))
		bar := image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+a.tabBarH)
		if !dr.pos.In(bar) {
			continue
		}
		i := a.insertIndex(g, dr.pos.X-box.Min.X)
		x := box.Min.X + gtx.Dp(6)
		if i < len(g.tabRects) {
			x = box.Min.X + g.tabRects[i].Min.X
		} else if n := len(g.tabRects); n > 0 {
			x = box.Min.X + g.tabRects[n-1].Max.X
		}
		fill(gtx.Ops, image.Rect(x-gtx.Dp(1), bar.Min.Y+gtx.Dp(4), x+gtx.Dp(1), bar.Max.Y), c.Accent)
	}
	title := a.fonts.Elide(dr.title, a.fonts.UI, px, gtx.Dp(200))
	w := a.fonts.Width(title, a.fonts.UI, px) + gtx.Dp(24)
	h := gtx.Dp(26)
	r := image.Rect(dr.pos.X-w/2, dr.pos.Y-h/2, dr.pos.X+w/2, dr.pos.Y+h/2)
	fillRound(gtx.Ops, r.Add(image.Pt(0, gtx.Dp(2))), gtx.Dp(6), c.Shadow)
	fillRound(gtx.Ops, r, gtx.Dp(6), theme.Alpha(a.scheme.Bg, 0.92))
	stroke(gtx.Ops, r, gtx.Dp(6), 1, c.Accent)
	met := a.fonts.metrics(a.fonts.UI, px)
	a.fonts.Draw(gtx.Ops, title, r.Min.X+gtx.Dp(12), r.Min.Y+h/2+met.Ascent/2-gtx.Dp(1), c.TabActiveFg, a.fonts.UI, px)
	if dr.pos.X < gtx.Dp(30) || dr.pos.Y < gtx.Dp(30) || dr.pos.X > a.winSize.X-gtx.Dp(30) || dr.pos.Y > a.winSize.Y-gtx.Dp(30) {
		a.setStatus("Release outside the window to open the file in a new window", 2*time.Second)
	}
}

// tabMenu shows the right-click menu of a tab.
func (a *App) tabMenu(d *Doc) {
	type item struct {
		label string
		fn    func()
	}
	inGroup := func() []*Doc {
		if gi, _ := a.groupOf(d); gi >= 0 {
			if gi == a.focus {
				return append([]*Doc(nil), a.docs...)
			}
			return append([]*Doc(nil), a.groups[gi].docs...)
		}
		return nil
	}
	items := []item{
		{"Close", func() { a.closeDocs([]*Doc{d}) }},
		{"Close Others", func() {
			var others []*Doc
			for _, x := range inGroup() {
				if x != d {
					others = append(others, x)
				}
			}
			a.closeDocs(others)
		}},
		{"Close Tabs to the Right", func() {
			var right []*Doc
			seen := false
			for _, x := range inGroup() {
				if seen {
					right = append(right, x)
				}
				seen = seen || x == d
			}
			a.closeDocs(right)
		}},
		{"Close All", func() { a.closeDocs(inGroup()) }},
		{"", nil},
		{"Open in New Window", func() { a.moveToNewWindow(d, false) }},
	}
	if len(a.groups) > 1 {
		items = append(items, item{"Move to Next Group", func() {
			gi, _ := a.groupOf(d)
			a.moveTab(d, (gi+1)%len(a.groups), -1)
		}})
	} else {
		items = append(items, item{"Split Right", func() {
			a.activate(d)
			a.moveToGroup(1)
		}})
	}
	for _, w := range a.windows {
		if w != a {
			target := w
			items = append(items, item{"Move to Window: " + strings.TrimSuffix(w.title, " — inks"), func() { a.moveToWindow(d, target) }})
		}
	}
	if d.Path != "" {
		items = append(items, item{"", nil},
			item{"Copy Path", func() { a.copyText(d.Path) }},
			item{"Copy Relative Path", func() { a.copyText(filepath.ToSlash(a.relPath(d.Path))) }},
			item{"Reveal in Finder", func() { go platform.Reveal(d.Path) }})
	}
	if d.preview {
		items = append(items, item{"", nil}, item{"Keep Open", func() { d.preview = false }})
	}
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.label
	}
	macmenu.Popup(labels, func(i int) {
		if i < 0 || i >= len(items) || items[i].fn == nil {
			return
		}
		a.Do(items[i].fn)
	})
}

// closeDocs closes documents one after another (asking about unsaved ones).
func (a *App) closeDocs(list []*Doc) {
	if len(list) == 0 {
		return
	}
	d, rest := list[0], list[1:]
	if !a.activate(d) {
		a.closeDocs(rest)
		return
	}
	a.closeDoc(a.indexOf(d), func() { a.closeDocs(rest) })
}

func (a *App) copyText(s string) {
	a.clipboardText, a.clipboardParts = s, nil
	a.exec(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(s))})
	a.setStatus("Copied "+s, 2*time.Second)
}
