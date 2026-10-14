package ui

import (
	"fmt"
	"time"

	"gioui.org/io/system"

	"inks/internal/platform"
)

// Window commands: closing a window (asking about unsaved files) and moving
// documents between windows.

// hasUnsaved reports whether any document in the window has unsaved edits.
func (a *App) hasUnsaved() bool {
	for _, d := range a.allDocs() {
		if d.Dirty() {
			return true
		}
	}
	return false
}

// closeWindow closes the window, first offering to save unsaved files.
func (a *App) closeWindow() {
	var dirty []*Doc
	for _, d := range a.allDocs() {
		if d.Dirty() {
			dirty = append(dirty, d)
		}
	}
	if len(dirty) == 0 {
		a.forceClose()
		return
	}
	msg := "Save changes to " + dirty[0].Title() + " before closing the window?"
	if len(dirty) > 1 {
		msg = fmt.Sprintf("Save changes to %d files before closing the window?", len(dirty))
	}
	go func() {
		choice := platform.Ask(msg, "Your changes will be lost if you don't save them.", []string{"Don't Save", "Cancel", "Save All"}, 2, 1)
		a.Do(func() {
			switch choice {
			case 0:
				a.forceClose()
			case 2:
				for _, d := range dirty {
					if d.Path != "" {
						a.save(d, nil)
					}
				}
				for _, d := range dirty {
					if d.Path == "" && d.Dirty() {
						// Untitled: Save As, then try closing again.
						a.activate(d)
						a.saveAs(d, a.closeWindow)
						return
					}
				}
				if !a.hasUnsaved() {
					a.forceClose()
				}
			}
		})
	}()
}

// forceClose closes the window without asking.
func (a *App) forceClose() {
	a.closing = true
	a.perform(system.ActionClose)
}

// detach removes d from this window without closing it.
func (a *App) detach(d *Doc) bool {
	gi, i := a.groupOf(d)
	if gi < 0 {
		return false
	}
	a.setFocus(gi)
	a.docs = append(a.docs[:i], a.docs[i+1:]...)
	if a.active >= len(a.docs) {
		a.active = len(a.docs) - 1
	}
	if len(a.allDocs()) == 0 {
		a.newDoc("", "")
	}
	if a.popup != nil && a.popup.doc == d {
		a.popup = nil
	}
	a.focusEditor = true
	return true
}

// adopt shows a document that came from another window.
func (a *App) adopt(d *Doc) {
	d.setInvalidate(a.win.Invalidate)
	// Replace a lone empty untitled tab, like opening a file does.
	if len(a.docs) == 1 && a.docs[0].Path == "" && !a.docs[0].Dirty() && a.docs[0].M.Buf.Len() == 0 && a.docs[0] != d {
		a.docs[0].hl.Close()
		a.docs = nil
	}
	a.docs = append(a.docs, d)
	a.active = len(a.docs) - 1
	d.reveal = true
	a.focusEditor = true
}

// moveToNewWindow opens d in a new window; atMouse places it under the
// pointer (dragging a tab out).
func (a *App) moveToNewWindow(d *Doc, atMouse bool) {
	if len(a.allDocs()) == 1 && a.allDocs()[0] == d {
		a.setStatus("This is the window's only tab", 3*time.Second)
		return
	}
	a.detach(d)
	n := a.NewWindow(func(n *App) {
		n.atMouse = atMouse
		n.folders = append([]string(nil), a.folders...)
		for _, f := range n.folders {
			n.tree.expanded[f] = true
		}
		n.showSidebar = a.showSidebar
		n.sidebarPinned = a.sidebarPinned
		n.sidebarW = a.sidebarW
		n.adopt(d)
	})
	n.perform(system.ActionRaise)
}

// moveToWindow moves d into another window.
func (a *App) moveToWindow(d *Doc, to *App) {
	if to == a {
		return
	}
	a.detach(d)
	to.adopt(d)
	to.perform(system.ActionRaise)
	to.win.Invalidate()
}

// perform asks the window to close, raise itself, etc. It never waits:
// Gio's Perform blocks until the window's own event loop runs, and that
// loop may be waiting for the shared lock this goroutine holds.
func (a *App) perform(act system.Action) {
	go a.win.Perform(act)
}
