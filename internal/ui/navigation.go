package ui

import (
	"fmt"
	"strings"
	"time"

	"gioui.org/io/key"

	"inks/internal/buffer"
)

// Bookmarks, jump history and macros.

// ---------------------------------------------------------------------------
// Bookmarks (kept in place as text changes; see Doc.trackEdits)

func (d *Doc) toggleBookmark() {
	for _, r := range d.M.Sels {
		line := r.Head.Line
		found := -1
		for i, b := range d.bookmarks {
			if b.Line == line {
				found = i
			}
		}
		if found >= 0 {
			d.bookmarks = append(d.bookmarks[:found], d.bookmarks[found+1:]...)
		} else {
			d.bookmarks = append(d.bookmarks, r.Head)
		}
	}
}

func (d *Doc) hasBookmark(line int) bool {
	for _, b := range d.bookmarks {
		if b.Line == line {
			return true
		}
	}
	return false
}

func (d *Doc) nextBookmark(dir int) bool {
	if len(d.bookmarks) == 0 {
		return false
	}
	cur := d.M.MainSel().Head.Line
	best := -1
	for i, b := range d.bookmarks {
		if dir > 0 && b.Line > cur && (best < 0 || b.Line < d.bookmarks[best].Line) {
			best = i
		}
		if dir < 0 && b.Line < cur && (best < 0 || b.Line > d.bookmarks[best].Line) {
			best = i
		}
	}
	if best < 0 { // wrap around
		for i, b := range d.bookmarks {
			if best < 0 || (dir > 0 && b.Line < d.bookmarks[best].Line) || (dir < 0 && b.Line > d.bookmarks[best].Line) {
				best = i
			}
		}
	}
	d.M.SetSels([]buffer.Range{buffer.Cursor(d.M.Buf.Clamp(d.bookmarks[best]))}, 0)
	d.reveal, d.centerOnReveal = true, true
	return true
}

// ---------------------------------------------------------------------------
// Jump history: big caret moves (goto, find, clicks, switching files) are
// recorded so ⌃- / ⌃⇧- can go back and forth.

type jump struct {
	doc *Doc
	pos buffer.Pos
}

// noteJumps runs after each frame.
func (a *App) noteJumps() {
	d := a.doc()
	if d == nil {
		return
	}
	h := d.M.MainSel().Head
	last := a.lastPos
	a.lastPos = jump{d, h}
	if a.jumping || last.doc == nil {
		a.jumping = false
		return
	}
	if last.doc == d && abs(last.pos.Line-h.Line) < 10 {
		return
	}
	if !a.isOpen(last.doc) {
		return
	}
	// Moving after going back drops the forward history.
	a.jumps = append(a.jumps[:a.jumpAt], last)
	if len(a.jumps) > 100 {
		a.jumps = a.jumps[len(a.jumps)-100:]
	}
	a.jumpAt = len(a.jumps)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (a *App) jumpBy(dir int) {
	if dir < 0 && a.jumpAt == len(a.jumps) {
		// Remember where we are so jump_forward can return.
		if d := a.doc(); d != nil {
			a.jumps = append(a.jumps, jump{d, d.M.MainSel().Head})
		}
	}
	i := a.jumpAt
	if dir < 0 {
		i = min(a.jumpAt, len(a.jumps)-1) - 1
	} else {
		i = a.jumpAt + 1
	}
	for i >= 0 && i < len(a.jumps) && !a.isOpen(a.jumps[i].doc) {
		i += dir
	}
	if i < 0 || i >= len(a.jumps) {
		a.setStatus("No more jumps", 2*time.Second)
		return
	}
	a.jumpAt = i
	j := a.jumps[i]
	a.activate(j.doc)
	j.doc.M.SetSels([]buffer.Range{buffer.Cursor(j.doc.M.Buf.Clamp(j.pos))}, 0)
	j.doc.reveal, j.doc.centerOnReveal = true, true
	a.jumping = true
	a.lastPos = jump{j.doc, j.pos}
}

// ---------------------------------------------------------------------------
// Macros: ⌃Q starts and stops recording; ⌃⇧Q plays it back.

type macroStep struct {
	cmd  string
	args map[string]any
	text string     // typed text
	key  *key.Event // an editor key (arrows, ↩, ⌫…)
}

func (a *App) toggleMacro() {
	if a.recording {
		a.recording = false
		a.macro = a.recorded
		a.setStatus(fmt.Sprintf("Macro recorded: %d steps — ⌃⇧Q plays it back", len(a.macro)), 4*time.Second)
		return
	}
	a.recording = true
	a.recorded = nil
	a.setStatus("Recording macro… ⌃Q to stop", time.Hour)
}

func (a *App) recordCmd(name string, args map[string]any) {
	if !a.recording {
		return
	}
	switch name {
	case "toggle_record_macro", "run_macro", "show_overlay_command_palette", "show_overlay_goto", "escape":
		return
	}
	a.recorded = append(a.recorded, macroStep{cmd: name, args: args})
}

func (a *App) recordText(text string) {
	if !a.recording || text == "" {
		return
	}
	if n := len(a.recorded); n > 0 && a.recorded[n-1].text != "" && !strings.Contains(text, "\n") {
		a.recorded[n-1].text += text
		return
	}
	a.recorded = append(a.recorded, macroStep{text: text})
}

func (a *App) recordKey(e key.Event) {
	if a.recording {
		a.recorded = append(a.recorded, macroStep{key: &e})
	}
}

func (a *App) runMacro() {
	if a.recording {
		a.toggleMacro()
	}
	d := a.doc()
	if d == nil || len(a.macro) == 0 {
		a.setStatus("No macro recorded (⌃Q to record one)", 0)
		return
	}
	d.M.Buf.BreakUndoGroup()
	for _, s := range a.macro {
		switch {
		case s.cmd != "":
			a.run(s.cmd, s.args)
		case s.key != nil:
			a.editorKey(a.doc(), *s.key)
		case s.text != "":
			a.doc().M.Insert(s.text)
		}
	}
	a.doc().M.Buf.BreakUndoGroup()
	a.afterEdit(a.doc())
}
