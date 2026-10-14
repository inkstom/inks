package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/unit"

	"inks/internal/macmenu"
	"inks/internal/pkgctl"
	"inks/internal/settings"
)

// Shared is what all inks windows have in common: settings, key bindings,
// the plugin host, packages, recent files, the console and the window list.
// Each App (window) embeds it.
//
// mu is held while any window processes a frame and while the plugin host
// answers an API call, so window and plugin code never run at the same time.
type Shared struct {
	mu sync.Mutex

	set    *settings.Settings
	keymap *Keymap
	py     *PyHost
	pkg    *pkgctl.Manager

	recent        []string // files, newest first
	recentFolders []string
	menuStale     bool

	console     []outLine
	consoleOpen bool // the last console line has no newline yet

	pkgPalette []PaletteEntry
	snippets   []snippet
	resources  bool // package resources loaded

	// Last copy, so a multi-caret paste can distribute the pieces.
	clipboardText  string
	clipboardParts []string
	clipboardLines bool

	windows []*App
	focused *App
	nextWin int
	// quitting is set once the app is quitting: windows then close on their
	// own and must not shrink the saved session.
	quitting bool
	// backedUp: this run's first save kept the previous session as a backup.
	backedUp bool

	// Test is handed to the first window (INKS_SELFTEST).
	Test *TestHooks

	// Work queued from other goroutines (see Shared.Do).
	taskMu sync.Mutex
	tasks  []func()
	wake   chan struct{}
}

// NewShared loads settings and key bindings and starts the plugin host
// machinery; Start opens the windows.
func NewShared() *Shared {
	s := &Shared{set: settings.Load(), wake: make(chan struct{}, 1)}
	s.keymap = newKeymap()
	s.py = newPyHost(s)
	go s.worker()
	return s
}

// Start restores the previous session's windows (or opens one) and opens
// the given files and folders. It returns the first window.
func (s *Shared) Start(open []string) *App {
	s.mu.Lock()
	defer s.mu.Unlock()
	var first *App
	if sess := loadSession(); sess != nil {
		s.recent, s.recentFolders = sess.Recent, sess.RecentFolders
		for i := range sess.Windows {
			ws := sess.Windows[i]
			a := s.NewWindow(func(a *App) { a.restoreFrom(&ws) })
			if first == nil {
				first = a
			}
		}
	}
	if first == nil {
		first = s.NewWindow(nil)
	}
	for _, p := range open {
		first.openAny(p)
	}
	macmenu.OnTerminate(s.onTerminate)
	macmenu.AcceptFirstMouse()
	macmenu.OnReopen(func() {
		s.Do(func() {
			if len(s.windows) == 0 {
				s.NewWindow(nil)
			}
		})
	})
	first.installMenu()
	s.loadPackageResources()
	if hasPlugins() {
		s.py.start()
	}
	go s.loop()
	return first
}

// NewWindow opens a window. setup (optional) fills it before it is shown;
// a window that ends up empty gets an untitled document. Call with mu held.
func (s *Shared) NewWindow(setup func(a *App)) *App {
	w := new(app.Window)
	w.Option(app.Title("inks"), app.Size(unit.Dp(1280), unit.Dp(820)), app.MinSize(unit.Dp(400), unit.Dp(260)))
	a := newApp(s, w)
	s.nextWin++
	a.id = s.nextWin
	s.windows = append(s.windows, a)
	s.focused = a
	if s.Test != nil {
		a.Test, s.Test = s.Test, nil
	}
	if setup != nil {
		setup(a)
	}
	if len(a.allDocs()) == 0 {
		a.newDoc("", "")
	}
	a.focusEditor = true
	go a.Run()
	return a
}

// active is the window commands go to: the focused one, else any.
func (s *Shared) active() *App {
	if s.focused != nil {
		return s.focused
	}
	if len(s.windows) > 0 {
		return s.windows[len(s.windows)-1]
	}
	return nil
}

// activeID is the plugin-facing id of the active window (0 for none).
func (s *Shared) activeID() int {
	if w := s.active(); w != nil {
		return w.id
	}
	return 0
}

// ownerOf returns the window showing d.
func (s *Shared) ownerOf(d *Doc) *App {
	for _, w := range s.windows {
		if w.isOpen(d) {
			return w
		}
	}
	return nil
}

// findOpen finds an open document by path in any window.
func (s *Shared) findOpen(path string) (*App, *Doc) {
	for _, w := range s.windows {
		for _, d := range w.allDocs() {
			if d.Path == path {
				return w, d
			}
		}
	}
	return nil, nil
}

// windowClosed forgets a window once it is gone.
func (s *Shared) windowClosed(a *App) {
	for i, w := range s.windows {
		if w == a {
			s.windows = append(s.windows[:i], s.windows[i+1:]...)
			break
		}
	}
	if s.focused == a {
		s.focused = nil
		if len(s.windows) > 0 {
			s.focused = s.windows[len(s.windows)-1]
		}
	}
	if s.quitting {
		return
	}
	for _, d := range a.allDocs() {
		d.hl.Close()
		s.py.event("on_close", d, nil, nil)
	}
	s.saveSession()
	s.menuStale = true
	if t := s.active(); t != nil {
		t.win.Invalidate()
	}
}

// Do runs fn under the shared lock on a background goroutine, in order,
// then redraws every window. Safe from any goroutine and from Cocoa
// callbacks on the main thread.
func (s *Shared) Do(fn func()) {
	s.taskMu.Lock()
	s.tasks = append(s.tasks, fn)
	s.taskMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Shared) worker() {
	for range s.wake {
		for {
			s.taskMu.Lock()
			q := s.tasks
			s.tasks = nil
			s.taskMu.Unlock()
			if len(q) == 0 {
				break
			}
			s.mu.Lock()
			for _, fn := range q {
				fn()
			}
			s.mu.Unlock()
		}
		s.invalidateAll()
	}
}

func (s *Shared) invalidateAll() {
	s.mu.Lock()
	wins := append([]*App(nil), s.windows...)
	s.mu.Unlock()
	for _, w := range wins {
		w.win.Invalidate()
	}
}

// loop does the periodic work that isn't tied to one window.
func (s *Shared) loop() {
	tick := time.NewTicker(3 * time.Second)
	n := 0
	for range tick.C {
		n++
		s.Do(func() {
			s.py.checkPlugins()
			if n%10 == 0 {
				s.saveSession()
			}
			if s.menuStale {
				s.menuStale = false
				if t := s.active(); t != nil {
					t.installMenu()
				}
			}
		})
	}
}

// onTerminate runs on the main thread when the app is asked to quit (menu,
// Dock, logout): save every window so unsaved work survives.
func (s *Shared) onTerminate() bool {
	deadline := time.Now().Add(time.Second)
	for !s.mu.TryLock() {
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.saveSession()
	s.quitting = true
	s.mu.Unlock()
	return true
}

// quit saves the session and ends the app.
func (s *Shared) quit() {
	s.saveSession()
	s.quitting = true
	macmenu.Terminate()
}

// OpenLater opens files or folders from Finder, the Dock or the command line.
func (s *Shared) OpenLater(paths ...string) {
	s.Do(func() {
		t := s.active()
		if t == nil {
			t = s.NewWindow(nil)
		}
		for _, p := range paths {
			t.openAny(p)
		}
		t.perform(system.ActionRaise)
	})
}

// ---------------------------------------------------------------------------
// Session (hot exit): every window's folders, layout and documents.

type appSession struct {
	Version       int       `json:"version"`
	Windows       []session `json:"windows"`
	Recent        []string  `json:"recent,omitempty"`
	RecentFolders []string  `json:"recentFolders,omitempty"`
}

func sessionPath() string {
	return filepath.Join(settings.DataDir(), "Local", "Session.inks_session.json")
}

func loadSession() *appSession {
	if os.Getenv("INKS_NO_SESSION") != "" {
		return nil
	}
	data, err := os.ReadFile(sessionPath())
	if err != nil {
		return nil
	}
	var as appSession
	if json.Unmarshal(data, &as) != nil {
		return nil
	}
	if as.Version < 3 {
		// One window per file before multiple windows.
		var old session
		if json.Unmarshal(data, &old) != nil {
			return nil
		}
		return &appSession{Version: 3, Windows: []session{old}, Recent: old.Recent}
	}
	return &as
}

// saveSession writes every window's state. Call with mu held.
func (s *Shared) saveSession() {
	if os.Getenv("INKS_NO_SESSION") != "" || s.quitting {
		return
	}
	if !s.backedUp {
		// Keep the previous run's session (unsaved text included) once, so
		// closing windows by mistake can't lose it: Local/Session.previous.json.
		s.backedUp = true
		if old, err := os.ReadFile(sessionPath()); err == nil {
			_ = os.WriteFile(filepath.Join(filepath.Dir(sessionPath()), "Session.previous.json"), old, 0o600)
		}
	}
	as := appSession{Version: 3, Recent: s.recent, RecentFolders: s.recentFolders}
	for _, w := range s.windows {
		as.Windows = append(as.Windows, w.sessionState())
	}
	data, _ := json.Marshal(as)
	_ = os.MkdirAll(filepath.Dir(sessionPath()), 0o755)
	_ = os.WriteFile(sessionPath(), data, 0o644)
}

// noteRecentFolder records a folder for File › Open Recent.
func (s *Shared) noteRecentFolder(p string) {
	out := []string{p}
	for _, r := range s.recentFolders {
		if r != p {
			out = append(out, r)
		}
	}
	if len(out) > 10 {
		out = out[:10]
	}
	s.recentFolders = out
	s.menuStale = true
}
