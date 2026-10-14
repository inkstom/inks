// Package ui is the inks window: layout, input routing and commands.
package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"inks/internal/buffer"
	"inks/internal/macmenu"
	"inks/internal/platform"
	"inks/internal/settings"
	"inks/internal/theme"
)

// App is one inks window. It embeds the state all windows share.
type App struct {
	*Shared

	id      int
	win     *app.Window
	view    uintptr // the NSView, once the window exists
	closing bool    // closing on purpose (unsaved changes were handled)
	title   string
	done    chan struct{}
	// atMouse places the new window under the pointer (a torn-off tab).
	atMouse bool

	ops    op.Ops
	fonts  *Fonts
	scheme *theme.Scheme
	chrome theme.Chrome

	docs   []*Doc
	active int

	folders     []string
	tree        *Tree
	showSidebar bool
	// sidebarPinned keeps the side bar open with no folders (the user asked
	// for it); otherwise it only appears once a folder is open.
	sidebarPinned bool
	sidebarW      int // dp

	commands map[string]*Command
	palette  []PaletteEntry

	quick  *QuickPanel
	find   *FindPanel
	input  *InputPanel
	search *SearchView
	git    *gitInfo
	output *OutputPanel

	sideMode string // "folders" or "search"
	resizing bool

	status      string
	statusUntil time.Time

	taskMu       sync.Mutex
	tasks        []func()
	focusEditor  bool
	gtx          layout.Context
	closed       []string
	fileCache    []string
	fileCacheAt  time.Time
	pendingPaste *Doc
	editorRect   image.Rectangle
	editorTag    *editorTagT

	Test *TestHooks // optional automation (INKS_SELFTEST)

	// Split layout.
	groups     []*Group
	focus      int
	drawGroup  int
	layoutName string
	pressed    bool // a pointer press landed in the group being drawn

	inFrame bool            // a frame is being processed (gtx is live)
	cmdQ    []input.Command // Gio commands issued between frames

	hovered  map[event.Tag]bool // areas under the pointer (see hit)
	popup    *popup
	complete *Completer

	// Tab dragging (see tabs.go).
	drag         *tabDrag
	pendingDrop  *tabDrag
	pendingClose *Doc
	sidebarPx    int         // side bar width this frame
	tabBarH      int         // tab bar height
	winSize      image.Point // window size this frame

	jumps     []jump
	jumpAt    int
	lastPos   jump
	jumping   bool
	recording bool
	recorded  []macroStep
	macro     []macroStep

	// Click targets (stable addresses so Gio can route pointer events).
	statusTags    [4]statusTag
	sidebarScroll struct{ _ int }
}

type editorTagT struct{}

// newApp creates a window's state (see Shared.NewWindow).
func newApp(s *Shared, w *app.Window) *App {
	a := &App{
		Shared:      s,
		win:         w,
		showSidebar: true,
		sidebarW:    240,
		editorTag:   &editorTagT{},
		done:        make(chan struct{}),
	}
	a.initGroups()
	a.fonts = newFonts(a.set.String("font_face"))
	a.applyTheme()
	a.tree = newTree(a)
	a.quick = newQuickPanel(a)
	a.find = newFindPanel(a)
	a.input = newInputPanel(a)
	a.search = newSearchView(a)
	a.git = newGitInfo()
	a.output = newOutputPanel(a)
	a.complete = newCompleter(a)
	a.sideMode = "folders"
	a.registerCommands()
	a.showSidebar = a.set.Bool("show_side_bar")
	return a
}

// openAny opens a file in this window, or a folder (see openFolder).
func (a *App) openAny(p string) {
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		a.openFolder(p)
		return
	}
	a.openPath(p)
}

// openFolder shows a folder: in this window if it has none yet, otherwise
// in a new window (File › Add Folder to Workspace adds to this one).
func (a *App) openFolder(p string) {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	for _, w := range a.windows {
		for _, f := range w.folders {
			if f == p {
				w.perform(system.ActionRaise)
				return
			}
		}
	}
	if len(a.folders) == 0 {
		a.addFolder(p)
		a.showSidebar = true
		a.sideMode = "folders"
		return
	}
	a.NewWindow(func(n *App) {
		n.addFolder(p)
		n.showSidebar = true
	})
}

func (a *App) indexOf(d *Doc) int {
	for i, x := range a.docs {
		if x == d {
			return i
		}
	}
	return -1
}

func (a *App) applyTheme() {
	a.scheme = theme.Get(a.set.String("color_scheme"))
	a.chrome = theme.ChromeFor(a.scheme)
}

// Do runs fn on the UI goroutine during the next frame. It is safe from any
// goroutine and from Cocoa callbacks on the main thread, where Gio's
// Invalidate must not be called directly (it would wait on the window lock
// held by the UI goroutine).
func (a *App) Do(fn func()) {
	a.taskMu.Lock()
	a.tasks = append(a.tasks, fn)
	a.taskMu.Unlock()
	go a.win.Invalidate()
}

// exec issues a Gio command now during a frame, or at the start of the next
// one when called between frames (e.g. from the plugin host).
func (a *App) exec(c input.Command) {
	if a.inFrame {
		a.gtx.Execute(c)
		return
	}
	a.cmdQ = append(a.cmdQ, c)
	go a.win.Invalidate()
}

// runTasks runs queued Do functions in order.
func (a *App) runTasks() {
	for {
		a.taskMu.Lock()
		q := a.tasks
		a.tasks = nil
		a.taskMu.Unlock()
		if len(q) == 0 {
			return
		}
		for _, fn := range q {
			fn()
		}
	}
}

func (a *App) setStatus(msg string, d time.Duration) {
	a.status = msg
	if d == 0 {
		d = 4 * time.Second
	}
	a.statusUntil = time.Now().Add(d)
}

// Run processes window events until the window closes.
func (a *App) Run() {
	go a.checkpointLoop()
	for {
		switch e := a.win.Event().(type) {
		case app.DestroyEvent:
			close(a.done)
			a.mu.Lock()
			a.windowClosed(a)
			a.mu.Unlock()
			return
		case app.ConfigEvent:
			if e.Config.Focused {
				a.mu.Lock()
				a.focused = a
				a.mu.Unlock()
			}
		case app.AppKitViewEvent:
			if e.View != 0 {
				a.view = e.View
				if a.atMouse {
					a.atMouse = false
					macmenu.PlaceWindowAtMouse(e.View)
				}
			}
		case *app.ClosingEvent:
			// The close button: ask about unsaved changes first.
			a.mu.Lock()
			if !a.closing && a.hasUnsaved() {
				e.Abort()
				a.Do(a.closeWindow)
			}
			a.mu.Unlock()
		case app.FrameEvent:
			gtx := app.NewContext(&a.ops, e)
			a.mu.Lock()
			a.frame(gtx)
			a.mu.Unlock()
			e.Frame(gtx.Ops)
		}
	}
}

func (a *App) checkpointLoop() {
	tick := time.NewTicker(1500 * time.Millisecond)
	defer tick.Stop()
	n := 0
	for {
		select {
		case <-a.done:
			return
		case <-tick.C:
		}
		n++
		a.Do(func() {
			a.checkDisk()
			if n%4 == 1 {
				a.refreshGit()
			}
		})
	}
}

// checkDisk reloads open files changed by other programs. Files with unsaved
// edits ask first (once per change).
func (a *App) checkDisk() {
	for _, d := range a.allDocs() {
		if d.Path == "" || d.asking {
			continue
		}
		st, err := os.Stat(d.Path)
		if err != nil || st.IsDir() || st.ModTime().Equal(d.modTime) || d.modTime.IsZero() {
			continue
		}
		mt := st.ModTime()
		text, _, err := readFile(d.Path)
		if err != nil {
			continue
		}
		norm, _ := normalizeDiskText(text)
		if norm == d.saved {
			d.modTime = mt
			continue
		}
		if !d.Dirty() {
			a.reloadDoc(d, text, mt)
			continue
		}
		d.asking = true
		doc := d
		go func() {
			choice := platform.Ask(doc.Title()+" changed on disk.", "It has unsaved changes in inks. Reload it and lose them? (Undo brings them back.)", []string{"Keep Mine", "Reload"}, 0, 0)
			a.Do(func() {
				doc.asking = false
				if choice == 1 {
					a.reloadDoc(doc, text, mt)
				} else {
					doc.modTime = mt
				}
			})
		}()
	}
}

// reloadDoc replaces a document's text with what is on disk, as one undoable edit.
func (a *App) reloadDoc(d *Doc, text string, mt time.Time) {
	norm, crlf := normalizeDiskText(text)
	if e, ok := diffEdit(d.M.Buf, d.M.Buf.Text("\n"), norm); ok {
		sels := d.M.Buf.Apply([]buffer.Edit{e}, d.M.Sels, buffer.ApplyOptions{Kind: "reload"})
		d.M.SetSels(sels, min(d.M.Main, len(sels)-1))
	}
	d.CRLF = crlf
	d.modTime = mt
	d.markSaved()
	a.win.Invalidate()
}

// ---------------------------------------------------------------------------
// Frame

func (a *App) doc() *Doc {
	if a.active < 0 || a.active >= len(a.docs) {
		return nil
	}
	return a.docs[a.active]
}

func (a *App) frame(gtx layout.Context) {
	a.gtx = gtx
	a.inFrame = true
	defer func() { a.inFrame = false }()
	for _, c := range a.cmdQ {
		gtx.Execute(c)
	}
	a.cmdQ = nil
	a.runTasks()
	if a.Test != nil {
		a.Test.frame(a)
	}
	a.handleKeys(gtx)
	a.drawUI(gtx)
	if a.focusEditor {
		a.focusEditor = false
		gtx.Execute(key.FocusCmd{Tag: a.editorTag})
	}
	a.updateTitle()
	a.noteJumps()
	if a.focused == a {
		a.py.afterFrame(a)
	}
	if a.menuStale {
		a.menuStale = false
		a.installMenu()
	}
}

// drawUI lays out and paints the whole window.
func (a *App) drawUI(gtx layout.Context) {
	fill(gtx.Ops, image.Rectangle{Max: gtx.Constraints.Max}, a.scheme.Bg)
	a.winSize = gtx.Constraints.Max
	a.sidebarPx = 0

	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !a.sidebarVisible() {
						return layout.Dimensions{}
					}
					return a.layoutSidebar(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return a.layoutGroups(gtx)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !a.output.visible {
				return layout.Dimensions{}
			}
			return a.output.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			switch {
			case a.find.visible:
				return a.find.Layout(gtx)
			case a.input.visible:
				return a.input.Layout(gtx)
			}
			return layout.Dimensions{}
		}),
		layout.Rigid(a.layoutStatus),
	)
	if a.quick.visible {
		a.quick.Layout(gtx)
	}
	a.drawTabDrag(gtx)
}

func (a *App) layoutEditorArea(gtx layout.Context) layout.Dimensions {
	d := a.doc()
	if d == nil {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	return a.layoutEditor(gtx, d)
}

func (a *App) updateTitle() {
	d := a.doc()
	title := "inks"
	if d != nil {
		name := d.Title()
		if d.Path != "" {
			name = tildify(d.Path)
		}
		if d.Dirty() {
			name += " •"
		}
		title = name + " — inks"
	}
	if title != a.title {
		a.title = title
		a.win.Option(app.Title(title))
	}
}

func tildify(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// ---------------------------------------------------------------------------
// Keyboard routing

var editorKeys = []key.Name{
	key.NameLeftArrow, key.NameRightArrow, key.NameUpArrow, key.NameDownArrow, key.NameHome, key.NameEnd,
	key.NamePageUp, key.NamePageDown, key.NameReturn, key.NameEnter, key.NameDeleteBackward,
	key.NameDeleteForward, key.NameTab, key.NameEscape,
}

func (a *App) keyFilters() []event.Filter {
	all := key.ModShift | key.ModAlt | key.ModCtrl | key.ModCommand
	fs := []event.Filter{
		key.FocusFilter{Target: a.editorTag},
		transfer.TargetFilter{Target: a.editorTag, Type: "application/text"},
		// App-wide shortcuts: anything with ⌘ or ⌃, function keys, Escape.
		key.Filter{Name: "", Required: key.ModCommand, Optional: key.ModShift | key.ModAlt | key.ModCtrl},
		key.Filter{Name: "", Required: key.ModCtrl, Optional: key.ModShift | key.ModAlt},
		key.Filter{Name: key.NameEscape, Optional: all},
		key.Filter{Name: key.NameTab, Required: key.ModCtrl, Optional: key.ModShift},
	}
	for i := 1; i <= 12; i++ {
		fs = append(fs, key.Filter{Name: key.Name(fmt.Sprintf("F%d", i)), Optional: all})
	}
	for _, n := range editorKeys {
		fs = append(fs, key.Filter{Focus: a.editorTag, Name: n, Optional: all})
	}
	return fs
}

func (a *App) handleKeys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(a.keyFilters()...)
		if !ok {
			break
		}
		d := a.doc()
		switch e := ev.(type) {
		case key.Event:
			if e.State != key.Press {
				continue
			}
			k := keyString(e)
			if bd, consumed, hint := a.keymap.Feed(k); consumed {
				if hint != "" {
					a.setStatus(hint, 3*time.Second)
				} else if bd != nil {
					a.run(bd.Command, bd.Args)
				}
				continue
			}
			if d != nil {
				a.editorKey(d, e)
			}
		case key.EditEvent:
			if d != nil {
				a.imeEdit(d, e)
				a.recordText(e.Text)
				a.afterEdit(d)
				a.complete.afterType(d, e.Text)
			}
		case key.SelectionEvent:
			if d != nil {
				a.imeSelect(d, key.Range(e))
			}
		case key.CompositionEvent:
			if d != nil {
				d.compose = key.Range(e)
				d.composing = e.Start != e.End
			}
		case transfer.DataEvent:
			if d != nil && a.pendingPaste == d {
				a.pendingPaste = nil
				r := e.Open()
				buf := new(strings.Builder)
				_, _ = ioCopy(buf, r)
				r.Close()
				a.pasteText(d, buf.String())
			}
		case key.FocusEvent:
		}
	}
}

func (a *App) afterEdit(d *Doc) {
	d.preview = false
	d.reveal = true
	d.blink = time.Now()
	a.win.Invalidate()
}

// ---------------------------------------------------------------------------
// Documents

func (a *App) newDoc(text, path string) *Doc {
	d := newDoc(text, path, a.set, a.win.Invalidate)
	a.docs = append(a.docs, d)
	a.active = len(a.docs) - 1
	a.focusEditor = true
	return d
}

// openPath opens a file (or adds a folder to the side bar).
func (a *App) openPath(p string) *Doc {
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	st, err := os.Stat(p)
	if err == nil && st.IsDir() {
		a.addFolder(p)
		return nil
	}
	if w, d := a.findOpen(p); d != nil {
		// Already open: show it where it is.
		w.activate(d)
		if w != a {
			w.perform(system.ActionRaise)
		}
		return d
	}
	text, mt, err := "", time.Time{}, error(nil)
	if st != nil {
		text, mt, err = readFile(p)
		if err != nil {
			a.setStatus(fmt.Sprintf("Unable to open %s: %v", tildify(p), err), 0)
			return nil
		}
	}
	// Replace a single empty untitled tab.
	if len(a.docs) == 1 && a.docs[0].Path == "" && !a.docs[0].Dirty() && a.docs[0].M.Buf.Len() == 0 {
		a.docs = nil
	}
	d := a.newDoc(text, p)
	d.modTime = mt
	a.fileCache = nil
	if st != nil {
		a.noteRecent(p)
		a.loadGitBase(d)
		a.py.event("on_load", d, nil, nil)
	}
	return d
}

// openPreview opens a file in a transient tab that the next preview
// replaces (single click in the side bar).
func (a *App) openPreview(p string) {
	for _, d := range a.allDocs() {
		if d.Path == p {
			a.activate(d)
			return
		}
	}
	var old *Doc
	for _, d := range a.docs {
		if d.preview && !d.Dirty() {
			old = d
		}
	}
	d := a.openPath(p)
	if d == nil {
		return
	}
	d.preview = true
	if old != nil && old != d {
		// Take the old preview's place in the tab order.
		oi, ni := a.indexOf(old), a.indexOf(d)
		a.docs[oi] = d
		a.docs = append(a.docs[:ni], a.docs[ni+1:]...)
		a.active = oi
		old.hl.Close()
	}
}

func (a *App) addFolder(p string) {
	for _, f := range a.folders {
		if f == p {
			return
		}
	}
	a.folders = append(a.folders, p)
	a.tree.expanded[p] = true
	a.fileCache = nil
	a.noteRecentFolder(p)
}

func (a *App) save(d *Doc, then func()) {
	if d.Path == "" {
		a.saveAs(d, then)
		return
	}
	a.py.event("on_pre_save", d, nil, nil)
	if err := d.Save(a.set); err != nil {
		go platform.Message("Unable to save "+tildify(d.Path), err.Error())
		return
	}
	a.setStatus("Saved "+tildify(d.Path), 2*time.Second)
	a.py.event("on_post_save", d, nil, nil)
	if d.gitBaseAt.IsZero() {
		a.loadGitBase(d)
	}
	a.refreshGit()
	if strings.HasPrefix(d.Path, settings.UserDir()) {
		a.reloadSettings()
	}
	if then != nil {
		then()
	}
}

func (a *App) saveAs(d *Doc, then func()) {
	def := d.Path
	if def == "" {
		dir := ""
		if len(a.folders) > 0 {
			dir = a.folders[0]
		} else if home, err := os.UserHomeDir(); err == nil {
			dir = home
		}
		def = filepath.Join(dir, sanitizeName(d.Title()))
	}
	go func() {
		p := platform.SaveFile("Save As", def)
		if p == "" {
			return
		}
		a.Do(func() {
			d.Path = p
			d.Name = ""
			d.gitBaseAt = time.Time{}
			if d.Lang == "Plain Text" {
				d.setLang(detectLang(p, d), a.set)
			}
			a.save(d, then)
		})
	}()
}

func detectLang(p string, d *Doc) string {
	return syntaxDetect(p, d.M.Buf.Line(0))
}

func sanitizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, s)
	if s == "" {
		s = "untitled"
	}
	return s
}

// closeDoc closes a tab, asking about unsaved changes.
func (a *App) closeDoc(i int, then func()) {
	if i < 0 || i >= len(a.docs) {
		return
	}
	d := a.docs[i]
	finish := func() {
		if d.Path != "" {
			a.closed = append(a.closed, d.Path)
		}
		d.hl.Close()
		a.py.event("on_close", d, nil, nil)
		if a.popup != nil && a.popup.doc == d {
			a.popup = nil
		}
		idx := -1
		for k, x := range a.docs {
			if x == d {
				idx = k
			}
		}
		if idx >= 0 {
			a.docs = append(a.docs[:idx], a.docs[idx+1:]...)
		}
		if a.active >= len(a.docs) {
			a.active = len(a.docs) - 1
		}
		if len(a.allDocs()) == 0 || (len(a.docs) == 0 && len(a.groups) == 1) {
			a.newDoc("", "")
		}
		a.focusEditor = true
		if then != nil {
			then()
		}
	}
	if !d.Dirty() {
		finish()
		return
	}
	go func() {
		choice := platform.Ask("Save changes to "+d.Title()+" before closing?", tildify(d.Path), []string{"Don't Save", "Cancel", "Save"}, 2, 1)
		a.Do(func() {
			switch choice {
			case 2:
				a.save(d, finish)
			case 0:
				finish()
			}
		})
	}()
}

func (a *App) reloadSettings() {
	if err := a.set.Reload(); err != nil {
		a.setStatus("Error in settings: "+err.Error(), 8*time.Second)
		return
	}
	a.keymap.Load()
	for _, w := range a.windows {
		w.fonts.SetMono(a.set.String("font_face"))
		w.applyTheme()
		for _, d := range w.allDocs() {
			d.applySettings(a.set)
		}
		w.win.Invalidate()
	}
	a.menuStale = true
}

// sidebarVisible reports whether the side bar is drawn.
func (a *App) sidebarVisible() bool {
	return a.showSidebar && (len(a.folders) > 0 || a.sideMode == "search" || a.sidebarPinned)
}

// setSidebar shows or hides the side bar on request.
func (a *App) setSidebar(show bool) {
	a.showSidebar = show
	a.sidebarPinned = show
}

// ---------------------------------------------------------------------------
// Session (hot exit)

type sessionDoc struct {
	Path    string  `json:"path,omitempty"`
	Name    string  `json:"name,omitempty"`
	Text    *string `json:"text,omitempty"` // unsaved contents
	Saved   *string `json:"saved,omitempty"`
	Lang    string  `json:"lang"`
	Line    int     `json:"line"`
	Col     int     `json:"col"`
	ScrollY float32 `json:"scrollY"`
	Group   int     `json:"group,omitempty"`
}

type session struct {
	Version  int          `json:"version"`
	Docs     []sessionDoc `json:"docs"`
	Active   int          `json:"active"`
	Folders  []string     `json:"folders"`
	Expanded []string     `json:"expanded"`
	Sidebar  bool         `json:"sidebar"`
	Closed   []string     `json:"closed,omitempty"`
	Recent   []string     `json:"recent,omitempty"`
	Pinned   bool         `json:"sidebarPinned,omitempty"`
	SideMode string       `json:"sideMode,omitempty"`
	SideW    int          `json:"sidebarWidth,omitempty"`
	Layout   string       `json:"layout,omitempty"`
	Focus    int          `json:"focus,omitempty"`
	Actives  []int        `json:"groupActive,omitempty"`
}

// sessionState captures this window for the session file.
func (a *App) sessionState() session {
	s := session{Version: 3, Active: a.active, Folders: a.folders, Sidebar: a.showSidebar, Closed: a.closed,
		SideMode: a.sideMode, SideW: a.sidebarW, Pinned: a.sidebarPinned}
	for p, open := range a.tree.expanded {
		if open {
			s.Expanded = append(s.Expanded, p)
		}
	}
	sort.Strings(s.Expanded)
	a.syncFocus()
	s.Layout, s.Focus = a.layoutName, a.focus
	type gdoc struct {
		d *Doc
		g int
	}
	var docs []gdoc
	for gi, g := range a.groups {
		s.Actives = append(s.Actives, g.active)
		for _, d := range g.docs {
			docs = append(docs, gdoc{d, gi})
		}
	}
	for _, x := range docs {
		d := x.d
		h := d.M.MainSel().Head
		sd := sessionDoc{Path: d.Path, Name: d.Name, Lang: d.Lang, Line: h.Line, Col: h.Col, ScrollY: d.scrollY, Group: x.g}
		if d.Dirty() || d.Path == "" {
			t := d.M.Buf.Text("\n")
			sd.Text = &t
			saved := d.saved
			sd.Saved = &saved
		}
		s.Docs = append(s.Docs, sd)
	}
	return s
}

// restoreFrom refills this window from its saved state.
func (a *App) restoreFrom(s *session) bool {
	for _, f := range s.Folders {
		if st, err := os.Stat(f); err == nil && st.IsDir() {
			a.addFolder(f)
		}
	}
	for _, p := range s.Expanded {
		a.tree.expanded[p] = true
	}
	a.showSidebar = s.Sidebar
	a.sidebarPinned = s.Pinned
	a.closed = s.Closed
	if s.SideMode != "" {
		a.sideMode = s.SideMode
	}
	if s.SideW >= 150 {
		a.sidebarW = s.SideW
	}
	if s.Layout != "" {
		a.setLayout(s.Layout)
	}
	for _, sd := range s.Docs {
		if sd.Group < len(a.groups) {
			a.setFocus(sd.Group)
		}
		var d *Doc
		switch {
		case sd.Path != "" && sd.Text == nil:
			d = a.openPath(sd.Path)
		case sd.Path != "":
			disk, mt, _ := readFile(sd.Path)
			d = a.newDoc(disk, sd.Path)
			d.modTime = mt
			d.M.Buf.SetText(*sd.Text)
			d.M.Buf.ClearHistory()
		default:
			saved := ""
			if sd.Saved != nil {
				saved = *sd.Saved
			}
			d = a.newDoc(saved, "")
			d.Name = sd.Name
			d.M.Buf.SetText(*sd.Text)
			d.M.Buf.ClearHistory()
		}
		if d == nil {
			continue
		}
		if sd.Lang != "" {
			d.setLang(sd.Lang, a.set)
		}
		p := d.M.Buf.Clamp(buffer.Pos{Line: sd.Line, Col: sd.Col})
		d.M.Sels = []buffer.Range{buffer.Cursor(p)}
		d.scrollY = sd.ScrollY
	}
	if len(a.allDocs()) == 0 {
		return false
	}
	a.syncFocus()
	for gi, g := range a.groups {
		if gi < len(s.Actives) && s.Actives[gi] < len(g.docs) {
			g.active = s.Actives[gi]
		}
	}
	if len(s.Actives) == 0 {
		a.groups[a.focus].active = min(s.Active, len(a.docs)-1)
	}
	f := min(max(s.Focus, 0), len(a.groups)-1)
	a.focus, a.docs, a.active, a.editorTag = f, a.groups[f].docs, a.groups[f].active, a.groups[f].tag
	if len(a.docs) == 0 {
		a.newDoc("", "")
	}
	return true
}

// quit saves the session and closes the window. Unsaved work is kept by hot
// exit (like closing and reopening the app later).
func (a *App) quit() { a.Shared.quit() }

var _ = pointer.Press
var _ = unit.Dp(0)
