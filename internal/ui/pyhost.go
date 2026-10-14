package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"gioui.org/io/clipboard"

	"inks/internal/buffer"
	"inks/internal/editor"
	"inks/internal/platform"
	"inks/internal/plugins"
	"inks/internal/settings"
	"inks/internal/syntax"
)

// PyHost runs Python plugins in a child process (internal/plugins/python).
// Messages are JSON lines:
//
//	editor → host  {"id", "method", "params"}      host → editor {"id", "result"|"error"}
//	host → editor  {"rid", "method", "params"}     editor → host {"rid", "result"|"error"}
//
// API calls from the host are answered on the reader goroutine while
// holding the app lock (between frames), so a plugin making many calls is
// not throttled to the frame rate.
type PyHost struct {
	a       *App
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wmu     sync.Mutex
	seq     int
	pmu     sync.Mutex
	pending map[int]func(json.RawMessage, error)

	ready    bool
	started  bool
	failed   string
	commands map[string]string // name → "text" | "window" | "app"
	events   map[string]bool
	winSet   map[string]any
	panels   map[string]*Doc // plugin output panels by name
	byID     map[int]*Doc    // panel docs
	pyMods   map[string]time.Time
	version  string

	lastActive *Doc
	lastSel    string
	notified   map[*Doc]int // doc → version sent with on_modified
}

func newPyHost(a *App) *PyHost {
	return &PyHost{a: a, pending: map[int]func(json.RawMessage, error){}, commands: map[string]string{},
		events: map[string]bool{}, winSet: map[string]any{}, panels: map[string]*Doc{}, byID: map[int]*Doc{},
		pyMods: map[string]time.Time{}, notified: map[*Doc]int{}}
}

// pythonPath finds a Python 3 interpreter.
func pythonPath(set *settings.Settings) string {
	if p := set.String("python_path"); p != "" {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/python3", "/usr/local/bin/python3"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("python3"); err == nil {
		return p
	}
	// /usr/bin/python3 is a stub that offers to install developer tools
	// unless they are present.
	if exec.Command("xcode-select", "-p").Run() == nil {
		return "/usr/bin/python3"
	}
	return ""
}

// hasPlugins reports whether any package contains Python plugins.
func hasPlugins() bool {
	dirs, _ := os.ReadDir(filepath.Join(settings.DataDir(), "Packages"))
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(settings.DataDir(), "Packages", d.Name(), "*.py"))
		if len(files) > 0 {
			return true
		}
	}
	return false
}

// start launches the host (once).
func (h *PyHost) start() {
	a := h.a
	if h.started || !a.set.Bool("plugins_enabled") {
		return
	}
	h.started = true
	py := pythonPath(a.set)
	if py == "" {
		h.failed = "Python 3 was not found; set \"python_path\" in Settings to use plugins"
		a.consoleLog(h.failed, true)
		return
	}
	script, err := plugins.Install(filepath.Join(settings.DataDir(), "Lib", "inks_host"))
	if err != nil {
		h.failed = err.Error()
		a.consoleLog("plugin host: "+err.Error(), true)
		return
	}
	cmd := exec.Command(py, "-u", script)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONDONTWRITEBYTECODE=1")
	cmd.Dir = settings.DataDir()
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = &consoleWriter{a: a, err: true}
	if err := cmd.Start(); err != nil {
		h.failed = err.Error()
		a.consoleLog("plugin host: "+err.Error(), true)
		return
	}
	h.cmd, h.stdin = cmd, stdin
	a.consoleLog("starting plugin host ("+py+")", false)
	go h.read(stdout)
	go func() {
		err := cmd.Wait()
		a.Do(func() {
			h.ready = false
			h.started = false
			if err != nil {
				a.consoleLog("plugin host exited: "+err.Error(), true)
			}
		})
	}()
}

func (h *PyHost) stop() {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.stdin.Close()
		_ = h.cmd.Process.Kill()
	}
	h.ready, h.started = false, false
}

func (h *PyHost) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.wmu.Lock()
	defer h.wmu.Unlock()
	if h.stdin != nil {
		_, _ = h.stdin.Write(append(data, '\n'))
	}
}

// request sends a method to the host; done runs on the UI goroutine.
func (h *PyHost) request(method string, params map[string]any, done func(json.RawMessage, error)) {
	if params == nil {
		params = map[string]any{}
	}
	h.pmu.Lock()
	h.seq++
	id := h.seq
	if done != nil {
		h.pending[id] = done
	} else {
		h.pending[id] = func(json.RawMessage, error) {}
	}
	h.pmu.Unlock()
	h.send(map[string]any{"id": id, "method": method, "params": params})
}

type hostMsg struct {
	ID        *int            `json:"id"`
	RID       *int            `json:"rid"`
	Method    string          `json:"method"`
	Params    map[string]any  `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *string         `json:"error"`
	Traceback string          `json:"traceback"`
}

func (h *PyHost) read(r io.Reader) {
	a := h.a
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		var m hostMsg
		if err := json.Unmarshal(line, &m); err != nil {
			text := string(line)
			a.Do(func() { a.consoleLog(text, false) })
			continue
		}
		switch {
		case m.Method == "log":
			text, _ := m.Params["text"].(string)
			kind, _ := m.Params["kind"].(string)
			a.Do(func() { a.consoleWrite(text, kind == "err") })
		case m.Method == "ready":
			h.version, _ = m.Params["version"].(string)
			a.Do(func() {
				h.ready = true
				a.consoleLog("plugin host ready (Python "+h.version+")", false)
				h.loadPlugins()
			})
		case m.RID != nil && m.Method != "":
			// API call: answer between frames under the app lock.
			a.mu.Lock()
			res, err := h.safeAPI(m.Method, m.Params)
			a.mu.Unlock()
			if err != nil {
				h.send(map[string]any{"rid": *m.RID, "error": err.Error()})
			} else {
				h.send(map[string]any{"rid": *m.RID, "result": res})
			}
			go a.win.Invalidate()
		case m.ID != nil:
			h.pmu.Lock()
			done := h.pending[*m.ID]
			delete(h.pending, *m.ID)
			h.pmu.Unlock()
			if done == nil {
				continue
			}
			var err error
			if m.Error != nil {
				err = fmt.Errorf("%s", *m.Error)
				tb := m.Traceback
				a.Do(func() {
					if tb != "" {
						a.consoleLog(strings.TrimRight(tb, "\n"), true)
					}
				})
			}
			res := m.Result
			a.Do(func() { done(res, err) })
		}
	}
}

func (h *PyHost) safeAPI(method string, p map[string]any) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: %v", method, r)
		}
	}()
	return h.api(method, p)
}

type registry struct {
	Plugins  []string `json:"plugins"`
	Commands []struct {
		Name, Kind string
	} `json:"commands"`
	Events   []string `json:"events"`
	Packages []string `json:"packages"`
}

func (h *PyHost) applyRegistry(raw json.RawMessage) *registry {
	var r registry
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	h.commands = map[string]string{}
	for _, c := range r.Commands {
		h.commands[c.Name] = c.Kind
	}
	h.events = map[string]bool{}
	for _, e := range r.Events {
		h.events[e] = true
	}
	return &r
}

func (h *PyHost) loadPlugins() {
	a := h.a
	h.request("load_plugins", nil, func(raw json.RawMessage, err error) {
		if err != nil {
			a.consoleLog("plugin loading failed: "+err.Error(), true)
			return
		}
		if r := h.applyRegistry(raw); r != nil {
			a.consoleLog(fmt.Sprintf("loaded %d plugin modules from %d packages", len(r.Plugins), len(r.Packages)), false)
		}
		h.snapshotPyFiles()
		a.loadPackageResources()
		if d := a.doc(); d != nil {
			h.event("on_activated", d, nil, nil)
		}
	})
}

// snapshotPyFiles records plugin modification times; checkPlugins reloads
// changed ones.
func (h *PyHost) snapshotPyFiles() {
	h.pyMods = map[string]time.Time{}
	files, _ := filepath.Glob(filepath.Join(settings.DataDir(), "Packages", "*", "*.py"))
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			h.pyMods[f] = st.ModTime()
		}
	}
}

func (h *PyHost) checkPlugins() {
	a := h.a
	if !h.ready {
		if !h.started && hasPlugins() {
			h.start()
		}
		return
	}
	files, _ := filepath.Glob(filepath.Join(settings.DataDir(), "Packages", "*", "*.py"))
	seen := map[string]bool{}
	for _, f := range files {
		seen[f] = true
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		if old, ok := h.pyMods[f]; !ok || !old.Equal(st.ModTime()) {
			h.pyMods[f] = st.ModTime()
			path := f
			h.request("reload_plugin", map[string]any{"path": path}, func(raw json.RawMessage, err error) {
				if err == nil {
					h.applyRegistry(raw)
					a.consoleLog("reloaded plugin "+filepath.Base(filepath.Dir(path))+"."+strings.TrimSuffix(filepath.Base(path), ".py"), false)
				}
			})
		}
	}
	for f := range h.pyMods {
		if !seen[f] {
			delete(h.pyMods, f)
			h.request("reload_plugin", map[string]any{"path": f}, func(raw json.RawMessage, err error) {
				if err == nil {
					h.applyRegistry(raw)
				}
			})
		}
	}
}

// runCommand runs a plugin command; false if no plugin has it.
func (h *PyHost) runCommand(name string, args map[string]any, d *Doc) bool {
	if !h.ready {
		return false
	}
	kind, ok := h.commands[name]
	if !ok {
		return false
	}
	if args == nil {
		args = map[string]any{}
	}
	params := map[string]any{"name": name, "args": args, "window": 1}
	if kind == "text" {
		if d == nil {
			d = h.a.doc()
		}
		if d == nil {
			return true
		}
		params["view"] = d.ID
		d.M.Buf.BreakUndoGroup()
	}
	h.request("run_command", params, func(_ json.RawMessage, err error) {
		if d != nil {
			d.M.Buf.BreakUndoGroup()
		}
		if err != nil {
			h.a.setStatus(name+": "+err.Error(), 0)
		}
	})
	return true
}

// event notifies listeners; done (optional) receives the result.
func (h *PyHost) event(name string, d *Doc, args []any, done func(json.RawMessage)) {
	if !h.ready || !h.events[name] {
		if done != nil {
			done(nil)
		}
		return
	}
	params := map[string]any{"name": name, "window": 1, "args": args}
	if d != nil {
		params["view"] = d.ID
	}
	if args == nil {
		params["args"] = []any{}
	}
	h.request("event", params, func(raw json.RawMessage, err error) {
		if done != nil {
			if err != nil {
				raw = nil
			}
			done(raw)
		}
	})
}

// afterFrame sends activation, modification and selection events.
func (h *PyHost) afterFrame() {
	if !h.ready {
		return
	}
	a := h.a
	d := a.doc()
	if d != h.lastActive {
		if h.lastActive != nil && a.isOpen(h.lastActive) {
			h.event("on_deactivated", h.lastActive, nil, nil)
		}
		if d != nil {
			h.event("on_activated", d, nil, nil)
		}
		h.lastActive = d
	}
	if h.events["on_modified"] {
		for _, x := range a.allDocs() {
			v := x.M.Buf.Version()
			if old, ok := h.notified[x]; !ok {
				h.notified[x] = v
			} else if old != v {
				h.notified[x] = v
				h.event("on_modified", x, nil, nil)
			}
		}
	}
	if d != nil && h.events["on_selection_modified"] {
		sel := fmt.Sprint(d.ID, d.M.Sels)
		if sel != h.lastSel {
			h.lastSel = sel
			h.event("on_selection_modified", d, nil, nil)
		}
	}
}

func (h *PyHost) callback(id any, args []any, release bool) {
	if id == nil || !h.ready {
		return
	}
	h.request("callback", map[string]any{"id": id, "args": args, "release": release}, nil)
}

func (h *PyHost) eval(source string) {
	if !h.ready {
		h.a.consoleLog("plugin host is not running (no Python plugins are installed, or Python was not found)", true)
		return
	}
	params := map[string]any{"source": source, "window": 1}
	if d := h.a.doc(); d != nil {
		params["view"] = d.ID
	}
	h.request("eval", params, nil)
}

// ---------------------------------------------------------------------------
// Console output

type consoleWriter struct {
	a   *App
	err bool
}

func (w *consoleWriter) Write(p []byte) (int, error) {
	text := string(p)
	w.a.Do(func() { w.a.consoleWrite(text, w.err) })
	return len(p), nil
}

// consoleWrite appends raw output, continuing an unfinished last line.
func (a *App) consoleWrite(text string, isErr bool) {
	if text == "" {
		return
	}
	parts := strings.Split(text, "\n")
	if a.consoleOpen && len(a.console) > 0 {
		last := &a.console[len(a.console)-1]
		last.text += strings.ReplaceAll(parts[0], "\t", "    ")
		last.err = last.err || isErr
		parts = parts[1:]
	}
	a.consoleOpen = !strings.HasSuffix(text, "\n")
	if !a.consoleOpen && len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for _, l := range parts {
		a.console = append(a.console, outLine{text: strings.ReplaceAll(l, "\t", "    "), err: isErr})
	}
	if len(a.console) > 5000 {
		a.console = a.console[len(a.console)-5000:]
	}
}

func (a *App) consoleLog(text string, isErr bool) {
	a.consoleOpen = false
	for _, l := range strings.Split(text, "\n") {
		a.console = append(a.console, outLine{text: strings.ReplaceAll(l, "\t", "    "), err: isErr})
	}
	if len(a.console) > 5000 {
		a.console = a.console[len(a.console)-5000:]
	}
}

// ---------------------------------------------------------------------------
// Points: plugins address text by code point offsets.

type pointIndex struct {
	ver    int
	starts []int // code point offset of each line start
}

func (d *Doc) points() *pointIndex {
	pi := d.pts
	b := d.M.Buf
	if pi != nil && pi.ver == b.Version() {
		return pi
	}
	pi = &pointIndex{ver: b.Version(), starts: make([]int, b.LineCount()+1)}
	n := 0
	for l := 0; l < b.LineCount(); l++ {
		pi.starts[l] = n
		n += utf8.RuneCountInString(b.Line(l)) + 1
	}
	pi.starts[b.LineCount()] = n - 1
	d.pts = pi
	return pi
}

func (d *Doc) size() int { return d.points().starts[d.M.Buf.LineCount()] }

func (d *Doc) pt(p buffer.Pos) int {
	pi := d.points()
	p = d.M.Buf.Clamp(p)
	line := d.M.Buf.Line(p.Line)
	return pi.starts[p.Line] + utf8.RuneCountInString(line[:p.Col])
}

func (d *Doc) pos(pt int) buffer.Pos {
	pi := d.points()
	n := d.M.Buf.LineCount()
	pt = min(max(pt, 0), pi.starts[n])
	l := sort.Search(n, func(i int) bool { return pi.starts[i+1] > pt }) // first line ending after pt
	if l >= n {
		l = n - 1
	}
	r := pt - pi.starts[l]
	line := d.M.Buf.Line(l)
	col := 0
	for i := 0; i < r && col < len(line); i++ {
		_, size := utf8.DecodeRuneInString(line[col:])
		col += size
	}
	return buffer.Pos{Line: l, Col: col}
}

// ---------------------------------------------------------------------------
// The API

func pInt(p map[string]any, k string) int {
	switch v := p[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func pStr(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}

func pBool(p map[string]any, k string) bool {
	b, _ := p[k].(bool)
	return b
}

func pairs(v any) [][2]int {
	list, _ := v.([]any)
	var out [][2]int
	for _, it := range list {
		pr, _ := it.([]any)
		if len(pr) >= 2 {
			a, _ := pr[0].(float64)
			b, _ := pr[1].(float64)
			out = append(out, [2]int{int(a), int(b)})
		}
	}
	return out
}

func (h *PyHost) view(id int) *Doc {
	a := h.a
	for _, d := range a.allDocs() {
		if d.ID == id {
			return d
		}
	}
	return h.byID[id]
}

func (h *PyHost) syntaxInfo(name string) map[string]any {
	return map[string]any{"path": "Packages/" + name + "/" + name + ".inks-syntax", "name": name, "hidden": false, "scope": baseScope(name)}
}

func baseScope(lang string) string {
	l := strings.ToLower(lang)
	switch l {
	case "plain text", "plaintext":
		return "text.plain"
	case "markdown":
		return "text.html.markdown"
	case "html":
		return "text.html.basic"
	case "xml":
		return "text.xml"
	case "terraform", "hcl":
		return "source.terraform"
	case "bash", "shell":
		return "source.shell.bash"
	case "c++":
		return "source.c++"
	}
	return "source." + regexp.MustCompile(`[^a-z0-9+#]+`).ReplaceAllString(l, "")
}

// syntaxFromPath maps "Packages/Python/Python.sublime-syntax" or
// "scope:source.python" to a syntax name.
func syntaxFromPath(p string) string {
	if strings.HasPrefix(p, "scope:") {
		scope := strings.TrimPrefix(p, "scope:")
		for _, n := range syntax.Names() {
			if baseScope(n) == scope {
				return n
			}
		}
		return ""
	}
	name := filepath.Base(p)
	if i := strings.Index(name, "."); i > 0 {
		name = name[:i]
	}
	for _, n := range syntax.Names() {
		if strings.EqualFold(n, name) {
			return n
		}
	}
	if strings.EqualFold(name, "plain text") {
		return "Plain Text"
	}
	return ""
}

func (h *PyHost) api(method string, p map[string]any) (any, error) {
	a := h.a
	var d *Doc
	if id, ok := p["view"].(float64); ok {
		d = h.view(int(id))
	}
	need := func() error {
		if d == nil {
			return fmt.Errorf("invalid view")
		}
		return nil
	}
	if strings.HasPrefix(method, "view_") && method != "view_is_valid" {
		if err := need(); err != nil {
			return nil, err
		}
	}
	clampPt := func(n int) int { return min(max(n, 0), d.size()) }
	switch method {
	// ---- application
	case "app_info":
		return map[string]any{
			"version": "4400", "arch": "arm64",
			"packages":           filepath.Join(settings.DataDir(), "Packages"),
			"installed_packages": filepath.Join(settings.DataDir(), "Installed Packages"),
			"cache":              filepath.Join(settings.DataDir(), "Cache"),
			"executable":         os.Args[0],
		}, nil
	case "status_message":
		a.setStatus(pStr(p, "text"), 4*time.Second)
		return nil, nil
	case "message_dialog":
		go platform.Message(pStr(p, "text"), "")
		return nil, nil
	case "ok_cancel_dialog":
		// Dialogs block the host until answered, as plugins expect.
		a.mu.Unlock()
		r := platform.Ask(pStr(p, "text"), "", []string{"Cancel", orStr(pStr(p, "ok_title"), "OK")}, 1, 0)
		a.mu.Lock()
		return r == 1, nil
	case "yes_no_cancel_dialog":
		a.mu.Unlock()
		r := platform.Ask(pStr(p, "text"), "", []string{"Cancel", orStr(pStr(p, "no_title"), "No"), orStr(pStr(p, "yes_title"), "Yes")}, 2, 0)
		a.mu.Lock()
		return []int{0, 2, 1}[r], nil // DIALOG_CANCEL=0, YES=1, NO=2
	case "set_clipboard":
		text := pStr(p, "text")
		a.clipboardText = text
		a.exec(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
		return nil, nil
	case "get_clipboard":
		out, _ := exec.Command("pbpaste").Output()
		return string(out), nil
	case "active_window":
		return 1, nil
	case "windows":
		return []int{1}, nil
	case "run_command":
		args, _ := p["args"].(map[string]any)
		name := pStr(p, "name")
		var target *Doc
		if pStr(p, "target") == "view" {
			target = d
		}
		a.runFromPlugin(name, args, target)
		return nil, nil
	case "list_syntaxes":
		var out []any
		for _, n := range syntax.Names() {
			out = append(out, h.syntaxInfo(n))
		}
		return out, nil
	case "syntax_for_file":
		return h.syntaxInfo(syntax.Detect(pStr(p, "path"), pStr(p, "first_line"))), nil
	case "find_resources":
		return findResources(pStr(p, "pattern")), nil
	case "load_resource":
		path := resourcePath(pStr(p, "name"))
		if path == "" {
			return nil, fmt.Errorf("resource not found: %s", pStr(p, "name"))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return string(data), nil

	// ---- settings
	case "settings_get":
		key := pStr(p, "key")
		if pStr(p, "target") == "view" {
			v := h.view(pInt(p, "ident"))
			if v == nil {
				return nil, fmt.Errorf("invalid view")
			}
			return v.setting(a, key), nil
		}
		return h.winSet[key], nil
	case "settings_set":
		key := pStr(p, "key")
		if pStr(p, "target") == "view" {
			v := h.view(pInt(p, "ident"))
			if v == nil {
				return nil, fmt.Errorf("invalid view")
			}
			if pBool(p, "erase") {
				delete(v.overrides, key)
			} else {
				v.setOverride(a, key, p["value"])
			}
			return nil, nil
		}
		if pBool(p, "erase") {
			delete(h.winSet, key)
		} else {
			h.winSet[key] = p["value"]
		}
		return nil, nil
	case "settings_all":
		if pStr(p, "target") == "view" {
			if v := h.view(pInt(p, "ident")); v != nil {
				return v.overrides, nil
			}
			return map[string]any{}, nil
		}
		return h.winSet, nil

	// ---- view
	case "view_is_valid":
		return d != nil, nil
	case "view_buffer_id":
		return d.ID, nil
	case "view_window":
		return 1, nil
	case "view_clones":
		return []int{d.ID}, nil
	case "view_file_name":
		if d.Path == "" {
			return nil, nil
		}
		return d.Path, nil
	case "view_name":
		return d.Name, nil
	case "view_set_name":
		d.Name = pStr(p, "name")
		return nil, nil
	case "view_close":
		if i := a.indexOf(d); i >= 0 {
			a.closeDoc(i, nil)
		} else if gi, i := a.groupOf(d); gi >= 0 {
			a.setFocus(gi)
			a.closeDoc(i, nil)
		}
		return true, nil
	case "view_retarget":
		d.Path = pStr(p, "path")
		return nil, nil
	case "view_is_dirty":
		return d.Dirty(), nil
	case "view_is_read_only":
		return d.M.ReadOnly, nil
	case "view_set_read_only":
		d.M.ReadOnly = pBool(p, "value")
		return nil, nil
	case "view_is_scratch":
		return d.Scratch, nil
	case "view_set_scratch":
		d.Scratch = pBool(p, "value")
		return nil, nil
	case "view_encoding":
		return d.Encoding, nil
	case "view_set_encoding":
		d.Encoding = pStr(p, "value")
		return nil, nil
	case "view_line_endings":
		if d.CRLF {
			return "Windows", nil
		}
		return "Unix", nil
	case "view_set_line_endings":
		d.CRLF = strings.Contains(strings.ToLower(pStr(p, "value")), "win")
		return nil, nil
	case "view_size":
		return d.size(), nil
	case "view_substr":
		x, y := pInt(p, "a"), pInt(p, "b")
		if x > y {
			x, y = y, x
		}
		return d.M.Buf.Slice(d.pos(x), d.pos(y)), nil
	case "view_insert":
		text := pStr(p, "text")
		h.edit(d, d.pos(clampPt(pInt(p, "pt"))), d.pos(clampPt(pInt(p, "pt"))), text)
		return utf8.RuneCountInString(text), nil
	case "view_replace":
		x, y := pInt(p, "a"), pInt(p, "b")
		if x > y {
			x, y = y, x
		}
		h.edit(d, d.pos(x), d.pos(y), pStr(p, "text"))
		return nil, nil
	case "view_change_count":
		return d.M.Buf.Version(), nil
	case "view_sel":
		out := make([][2]int, len(d.M.Sels))
		for i, r := range d.M.Sels {
			out[i] = [2]int{d.pt(r.Anchor), d.pt(r.Head)}
		}
		return out, nil
	case "view_set_sel":
		var sels []buffer.Range
		for _, pr := range pairs(p["regions"]) {
			sels = append(sels, buffer.Range{Anchor: d.pos(pr[0]), Head: d.pos(pr[1]), Goal: -1})
		}
		if len(sels) > 0 {
			d.M.SetSels(sels, len(sels)-1)
			if d == a.doc() {
				d.reveal = true
			}
		}
		return nil, nil
	case "view_line":
		x, y := pInt(p, "a"), pInt(p, "b")
		if x > y {
			x, y = y, x
		}
		s, e := d.pos(x), d.pos(y)
		from := d.pt(buffer.Pos{Line: s.Line})
		to := d.pt(buffer.Pos{Line: e.Line, Col: len(d.M.Buf.Line(e.Line))})
		if pBool(p, "full") {
			to = min(to+1, d.size())
		}
		return [2]int{from, to}, nil
	case "view_lines":
		s, e := d.pos(pInt(p, "a")), d.pos(pInt(p, "b"))
		var out [][2]int
		for l := s.Line; l <= e.Line; l++ {
			out = append(out, [2]int{d.pt(buffer.Pos{Line: l}), d.pt(buffer.Pos{Line: l, Col: len(d.M.Buf.Line(l))})})
		}
		return out, nil
	case "view_word":
		x, y := d.pos(pInt(p, "a")), d.pos(pInt(p, "b"))
		from, to := d.pt(x), d.pt(y)
		if s, _, ok := d.M.WordAt(x); ok {
			from = d.pt(s)
		}
		if _, e, ok := d.M.WordAt(y); ok {
			to = d.pt(e)
		}
		return [2]int{from, to}, nil
	case "view_classify":
		return classify(d, clampPt(pInt(p, "pt"))), nil
	case "view_find_by_class":
		pt := clampPt(pInt(p, "pt"))
		step := -1
		if pBool(p, "forward") {
			step = 1
		}
		classes := pInt(p, "classes")
		size := d.size()
		for {
			pt += step
			if pt <= 0 {
				return 0, nil
			}
			if pt >= size {
				return size, nil
			}
			if classify(d, pt)&classes != 0 {
				return pt, nil
			}
		}
	case "view_expand_by_class":
		x, y := clampPt(pInt(p, "a")), clampPt(pInt(p, "b"))
		classes := pInt(p, "classes")
		for x > 0 && classify(d, x)&classes == 0 {
			x--
		}
		for y < d.size() && classify(d, y)&classes == 0 {
			y++
		}
		return [2]int{x, y}, nil
	case "view_find":
		re, err := pluginRegexp(pStr(p, "pattern"), pInt(p, "flags"))
		if err != nil {
			return nil, err
		}
		text := d.M.Buf.Text("\n")
		start := len(d.M.Buf.Slice(buffer.Pos{}, d.pos(pInt(p, "start"))))
		loc := re.FindStringIndex(text[min(start, len(text)):])
		if loc == nil {
			return []int{-1, -1}, nil
		}
		return [2]int{d.pt(d.M.Buf.PosAt(start + loc[0])), d.pt(d.M.Buf.PosAt(start + loc[1]))}, nil
	case "view_find_all":
		re, err := pluginRegexp(pStr(p, "pattern"), pInt(p, "flags"))
		if err != nil {
			return nil, err
		}
		text := d.M.Buf.Text("\n")
		fmtStr, hasFmt := p["fmt"].(string)
		var out []any
		for _, m := range re.FindAllStringSubmatchIndex(text, 100000) {
			from := utf8.RuneCountInString(text[:m[0]])
			to := from + utf8.RuneCountInString(text[m[0]:m[1]])
			var extraction any
			if hasFmt {
				extraction = string(re.ExpandString(nil, strings.NewReplacer(`\`, `$`).Replace(fmtStr), text, m))
			}
			out = append(out, []any{from, to, extraction})
		}
		return out, nil
	case "view_extract_scope":
		pt := clampPt(pInt(p, "pt"))
		s, e, ok := d.M.WordAt(d.pos(pt))
		if !ok {
			return [2]int{pt, pt}, nil
		}
		return [2]int{d.pt(s), d.pt(e)}, nil
	case "view_scope_name":
		return scopeAt(d, d.pos(clampPt(pInt(p, "pt")))), nil
	case "view_find_by_selector":
		return findBySelector(d, pStr(p, "selector")), nil
	case "view_style":
		s := a.scheme
		return map[string]any{"background": hexColor(s.Bg), "foreground": hexColor(s.Fg), "caret": hexColor(s.Caret),
			"selection": hexColor(s.Selection), "line_highlight": hexColor(s.LineHighlight)}, nil
	case "view_style_for_scope":
		return map[string]any{"foreground": hexColor(a.scheme.Token(roleForScope(pStr(p, "scope"))))}, nil
	case "view_indented_region":
		pos := d.pos(clampPt(pInt(p, "pt")))
		ind := func(l int) int {
			t := d.M.Buf.Line(l)
			return len(t) - len(strings.TrimLeft(t, " \t"))
		}
		base := ind(pos.Line)
		x, y := pos.Line, pos.Line
		for x > 0 && (strings.TrimSpace(d.M.Buf.Line(x-1)) == "" || ind(x-1) >= base) {
			x--
		}
		for y+1 < d.M.Buf.LineCount() && (strings.TrimSpace(d.M.Buf.Line(y+1)) == "" || ind(y+1) >= base) {
			y++
		}
		return [2]int{d.pt(buffer.Pos{Line: x}), min(d.size(), d.pt(buffer.Pos{Line: y, Col: len(d.M.Buf.Line(y))})+1)}, nil
	case "view_indentation_level":
		pos := d.pos(clampPt(pInt(p, "pt")))
		t := d.M.Buf.Line(pos.Line)
		v := visualCol(t, len(t)-len(strings.TrimLeft(t, " \t")), d.M.TabSize)
		return v / max(1, d.M.TabSize), nil
	case "view_rowcol":
		pos := d.pos(clampPt(pInt(p, "pt")))
		return [2]int{pos.Line, utf8.RuneCountInString(d.M.Buf.Line(pos.Line)[:pos.Col])}, nil
	case "view_text_point":
		row, col := pInt(p, "row"), pInt(p, "col")
		if row >= d.M.Buf.LineCount() {
			return d.size(), nil
		}
		row = max(row, 0)
		start := d.pt(buffer.Pos{Line: row})
		return min(start+max(col, 0), d.pt(buffer.Pos{Line: row, Col: len(d.M.Buf.Line(row))})), nil
	case "view_visible_region":
		g := a.geom(a.gtx, d)
		first := int(d.scrollY) / max(1, g.lineH)
		l0, _ := g.rows.lineOfRow(first)
		l1, _ := g.rows.lineOfRow(first + a.editorRect.Dy()/max(1, g.lineH))
		return [2]int{d.pt(buffer.Pos{Line: l0}), d.pt(buffer.Pos{Line: l1, Col: len(d.M.Buf.Line(l1))})}, nil
	case "view_show":
		pos := d.pos(clampPt(pInt(p, "a")))
		d.revealAt = &pos
		d.reveal, d.centerOnReveal = true, pBool(p, "center")
		return nil, nil
	case "view_viewport_position":
		return [2]float32{d.scrollX, d.scrollY}, nil
	case "view_set_viewport_position":
		d.scrollX, d.scrollY = float32(pInt(p, "x")), float32(pInt(p, "y"))
		return nil, nil
	case "view_viewport_extent":
		return [2]int{a.editorRect.Dx(), a.editorRect.Dy()}, nil
	case "view_text_to_layout":
		g := a.geom(a.gtx, d)
		pos := d.pos(clampPt(pInt(p, "pt")))
		row, from := g.rows.rowOf(pos)
		line := d.M.Buf.Line(pos.Line)
		x := float32(visualCol(line, pos.Col, d.M.TabSize)-visualCol(line, from, d.M.TabSize)) * g.met.Advance
		return [2]float32{x, float32(row * g.lineH)}, nil
	case "view_layout_to_text":
		g := a.geom(a.gtx, d)
		row := pInt(p, "y") / max(1, g.lineH)
		l, k := g.rows.lineOfRow(row)
		line := d.M.Buf.Line(l)
		from, _ := g.rows.rowSpan(line, l, k)
		col := editor.ColForVisual(line, visualCol(line, from, d.M.TabSize)+int(float32(pInt(p, "x"))/g.met.Advance), d.M.TabSize)
		return d.pt(buffer.Pos{Line: l, Col: col}), nil
	case "view_line_height":
		return a.geom(a.gtx, d).lineH, nil
	case "view_em_width":
		return a.geom(a.gtx, d).met.Advance, nil
	case "view_fold", "view_unfold":
		return false, nil // folding is not supported yet
	case "view_add_regions":
		var rs []buffer.Range
		for _, pr := range pairs(p["regions"]) {
			rs = append(rs, buffer.Range{Anchor: d.pos(pr[0]), Head: d.pos(pr[1])})
		}
		if d.regions == nil {
			d.regions = map[string]*regionSet{}
		}
		d.regions[pStr(p, "key")] = &regionSet{ranges: rs, scope: pStr(p, "scope"), flags: pInt(p, "flags")}
		return nil, nil
	case "view_get_regions":
		var out [][2]int
		if rs := d.regions[pStr(p, "key")]; rs != nil {
			for _, r := range rs.ranges {
				out = append(out, [2]int{d.pt(r.Anchor), d.pt(r.Head)})
			}
		}
		return out, nil
	case "view_erase_regions":
		delete(d.regions, pStr(p, "key"))
		return nil, nil
	case "view_add_phantom":
		if d.phantoms == nil {
			d.phantoms = map[string][]phantom{}
		}
		key := pStr(p, "key")
		d.phantoms[key] = append(d.phantoms[key], phantom{pos: d.pos(clampPt(pInt(p, "a"))), text: htmlText(pStr(p, "content"))})
		return len(d.phantoms[key]), nil
	case "view_erase_phantoms":
		delete(d.phantoms, pStr(p, "key"))
		return nil, nil
	case "view_set_syntax":
		if name := syntaxFromPath(pStr(p, "syntax")); name != "" {
			d.setLang(name, a.set)
		}
		return nil, nil
	case "view_syntax":
		return h.syntaxInfo(d.Lang), nil
	case "view_symbols":
		var out []any
		for _, it := range a.symbolItems(d, "") {
			if r, ok := it.Data.(buffer.Range); ok {
				out = append(out, []any{d.pt(r.Start()), d.pt(r.End()), it.Caption})
			}
		}
		return out, nil
	case "view_set_status":
		if d.status == nil {
			d.status = map[string]string{}
		}
		if v, ok := p["value"].(string); ok {
			d.status[pStr(p, "key")] = v
		} else {
			delete(d.status, pStr(p, "key"))
		}
		return nil, nil
	case "view_get_status":
		return d.status[pStr(p, "key")], nil
	case "view_extract_completions":
		return bufferWords(d, pStr(p, "prefix"), 500), nil
	case "view_show_popup":
		loc := pInt(p, "location")
		pos := d.M.MainSel().Head
		if loc >= 0 {
			pos = d.pos(loc)
		}
		a.popup = &popup{doc: d, pos: pos, text: htmlText(pStr(p, "content"))}
		return nil, nil
	case "view_update_popup":
		if a.popup != nil {
			a.popup.text = htmlText(pStr(p, "content"))
		}
		return nil, nil
	case "view_is_popup_visible":
		return a.popup != nil, nil
	case "view_hide_popup":
		a.popup = nil
		return nil, nil
	case "view_is_auto_complete_visible":
		return a.complete != nil && a.complete.visible, nil
	case "view_show_popup_menu":
		var items []QuickItem
		list, _ := p["items"].([]any)
		for i, it := range list {
			items = append(items, QuickItem{Caption: fmt.Sprint(it), Data: i})
		}
		cb := p["callback"]
		a.pick(items, func(it *QuickItem) { h.callback(cb, []any{it.Data}, true) })
		a.quick.onCancel = func() { h.callback(cb, []any{-1}, true) }
		return nil, nil

	// ---- window
	case "window_active_view":
		if d := a.doc(); d != nil {
			return d.ID, nil
		}
		return nil, nil
	case "window_views":
		var out []int
		g := pInt(p, "group")
		for gi, grp := range a.groups {
			if g >= 0 && gi != g {
				continue
			}
			docs := grp.docs
			if gi == a.focus {
				docs = a.docs
			}
			for _, x := range docs {
				out = append(out, x.ID)
			}
		}
		return out, nil
	case "window_new_file":
		nd := a.newDoc("", "")
		if name := syntaxFromPath(pStr(p, "syntax")); name != "" {
			nd.setLang(name, a.set)
		}
		return nd.ID, nil
	case "window_open_file":
		path := pStr(p, "path")
		line, col := 0, 0
		if pBool(p, "encoded") {
			if m := regexp.MustCompile(`^(.*?):(\d+)(?::(\d+))?$`).FindStringSubmatch(path); m != nil {
				path = m[1]
				fmt.Sscan(m[2], &line)
				col = 1
				if m[3] != "" {
					fmt.Sscan(m[3], &col)
				}
			}
		}
		if g := pInt(p, "group"); g >= 0 && g < len(a.groups) {
			a.setFocus(g)
		}
		var nd *Doc
		if pBool(p, "transient") {
			a.openPreview(path)
			nd = a.doc()
		} else {
			nd = a.openPath(path)
		}
		if nd == nil {
			return 0, nil
		}
		if line > 0 {
			a.gotoLine(nd, line, col)
		}
		return nd.ID, nil
	case "window_find_open_file":
		for _, x := range a.allDocs() {
			if x.Path == pStr(p, "path") {
				return x.ID, nil
			}
		}
		return nil, nil
	case "window_num_groups":
		return len(a.groups), nil
	case "window_active_group":
		return a.focus, nil
	case "window_focus_group":
		a.setFocus(pInt(p, "group"))
		return nil, nil
	case "window_focus_view":
		if v := h.view(pInt(p, "target")); v != nil {
			a.activate(v)
		}
		return nil, nil
	case "window_get_view_index":
		if v := h.view(pInt(p, "target")); v != nil {
			g, i := a.groupOf(v)
			return [2]int{g, i}, nil
		}
		return [2]int{-1, -1}, nil
	case "window_set_view_index":
		if v := h.view(pInt(p, "target")); v != nil && a.activate(v) {
			a.moveToGroup(pInt(p, "group"))
		}
		return nil, nil
	case "window_active_view_in_group":
		g := pInt(p, "group")
		if g < 0 || g >= len(a.groups) {
			return nil, nil
		}
		docs, act := a.groups[g].docs, a.groups[g].active
		if g == a.focus {
			docs, act = a.docs, a.active
		}
		if act >= 0 && act < len(docs) {
			return docs[act].ID, nil
		}
		return nil, nil
	case "window_layout":
		cells := layouts[a.layoutName]
		var xs, ys []float32
		var out [][4]int
		idx := func(list *[]float32, v float32) int {
			for i, x := range *list {
				if x == v {
					return i
				}
			}
			*list = append(*list, v)
			sort.Slice(*list, func(i, j int) bool { return (*list)[i] < (*list)[j] })
			return -1
		}
		for _, c := range cells {
			idx(&xs, c[0])
			idx(&xs, c[2])
			idx(&ys, c[1])
			idx(&ys, c[3])
		}
		find := func(list []float32, v float32) int {
			for i, x := range list {
				if x == v {
					return i
				}
			}
			return 0
		}
		for _, c := range cells {
			out = append(out, [4]int{find(xs, c[0]), find(ys, c[1]), find(xs, c[2]), find(ys, c[3])})
		}
		return map[string]any{"cols": xs, "rows": ys, "cells": out}, nil
	case "window_set_layout":
		lay, _ := p["layout"].(map[string]any)
		cells, _ := lay["cells"].([]any)
		cols, _ := lay["cols"].([]any)
		rows, _ := lay["rows"].([]any)
		switch {
		case len(cells) <= 1:
			a.setLayout("single")
		case len(cells) == 4:
			a.setLayout("grid_4")
		case len(rows) > 2:
			a.setLayout("rows_2")
		case len(cols) > 3:
			a.setLayout("columns_3")
		default:
			a.setLayout("columns_2")
		}
		return nil, nil
	case "window_folders":
		return append([]string{}, a.folders...), nil
	case "window_project_file_name":
		return nil, nil
	case "window_project_data":
		if len(a.folders) == 0 {
			return nil, nil
		}
		var fs []any
		for _, f := range a.folders {
			fs = append(fs, map[string]any{"path": f})
		}
		return map[string]any{"folders": fs}, nil
	case "window_set_project_data":
		data, _ := p["data"].(map[string]any)
		fs, _ := data["folders"].([]any)
		a.folders = nil
		for _, f := range fs {
			if m, ok := f.(map[string]any); ok {
				if path, ok := m["path"].(string); ok {
					a.addFolder(path)
				}
			}
		}
		return nil, nil
	case "window_extract_variables":
		vars := map[string]any{"platform": "OSX", "packages": filepath.Join(settings.DataDir(), "Packages")}
		if cur := a.doc(); cur != nil && cur.Path != "" {
			vars["file"] = cur.Path
			vars["file_path"] = filepath.Dir(cur.Path)
			vars["file_name"] = filepath.Base(cur.Path)
			vars["file_extension"] = strings.TrimPrefix(filepath.Ext(cur.Path), ".")
			vars["file_base_name"] = strings.TrimSuffix(filepath.Base(cur.Path), filepath.Ext(cur.Path))
		}
		if len(a.folders) > 0 {
			vars["folder"] = a.folders[0]
		}
		return vars, nil
	case "window_create_output_panel":
		name := pStr(p, "name")
		if v := h.panels[name]; v != nil {
			return v.ID, nil
		}
		v := newDoc("", "", a.set, a.win.Invalidate)
		v.Scratch = true
		v.Name = "output." + name
		h.panels[name] = v
		h.byID[v.ID] = v
		return v.ID, nil
	case "window_find_output_panel":
		if v := h.panels[pStr(p, "name")]; v != nil {
			return v.ID, nil
		}
		return nil, nil
	case "window_destroy_output_panel":
		if v := h.panels[pStr(p, "name")]; v != nil {
			if a.output.doc == v {
				a.output.visible = false
			}
			delete(h.panels, pStr(p, "name"))
			delete(h.byID, v.ID)
		}
		return nil, nil
	case "window_active_panel":
		if a.output.visible {
			return a.output.panelName(), nil
		}
		if a.find.visible {
			return "find", nil
		}
		return nil, nil
	case "window_panels":
		out := []string{"console", "find", "replace", "find_in_files", "output.build"}
		for n := range h.panels {
			out = append(out, "output."+n)
		}
		return out, nil
	case "window_show_input_panel":
		onDone, onChange, onCancel := p["on_done"], p["on_change"], p["on_cancel"]
		a.input.show(pStr(p, "caption"), pStr(p, "initial"), func(text string) { h.callback(onDone, []any{text}, true) })
		a.input.onChange = nil
		if onChange != nil {
			a.input.onChange = func(text string) { h.callback(onChange, []any{text}, false) }
		}
		a.input.onCancel = nil
		if onCancel != nil {
			a.input.onCancel = func() { h.callback(onCancel, nil, true) }
		}
		return nil, nil
	case "window_show_quick_panel":
		list, _ := p["items"].([]any)
		var items []QuickItem
		for i, it := range list {
			row, _ := it.([]any)
			qi := QuickItem{Data: i}
			if len(row) > 0 {
				qi.Caption = fmt.Sprint(row[0])
			}
			if len(row) > 1 && row[1] != nil {
				qi.Detail = fmt.Sprint(row[1])
			}
			items = append(items, qi)
		}
		onSelect, onHighlight := p["on_select"], p["on_highlight"]
		selected := false
		a.pick(items, func(it *QuickItem) {
			selected = true
			h.callback(onSelect, []any{it.Data}, true)
		})
		if onHighlight != nil {
			a.quick.onMove = func(it *QuickItem, _ string) { h.callback(onHighlight, []any{it.Data}, false) }
		}
		a.quick.onCancel = func() {
			if !selected {
				h.callback(onSelect, []any{-1}, true)
			}
		}
		if si := pInt(p, "selected_index"); si > 0 {
			a.quick.index = min(si, len(items)-1)
		}
		return nil, nil
	case "window_ui_visible":
		switch pStr(p, "what") {
		case "sidebar":
			return a.sidebarVisible(), nil
		case "minimap":
			return a.set.Bool("draw_minimap"), nil
		}
		return true, nil
	case "window_set_ui_visible":
		switch pStr(p, "what") {
		case "sidebar":
			a.setSidebar(pBool(p, "value"))
		case "minimap":
			a.setSetting("draw_minimap", pBool(p, "value"))
		}
		return nil, nil
	case "window_lookup_symbol":
		return []any{}, nil
	}
	return nil, fmt.Errorf("API not implemented: %s", method)
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// edit applies a plugin edit as part of the current command's undo step.
func (h *PyHost) edit(d *Doc, from, to buffer.Pos, text string) {
	sels := d.M.Buf.Apply([]buffer.Edit{{Start: from, End: to, Text: text}}, d.M.Sels, buffer.ApplyOptions{Kind: "plugin", Merge: true})
	d.M.Sels = sels
}

// pluginRegexp converts a plugin (Python/Oniguruma flavoured) pattern.
func pluginRegexp(pattern string, flags int) (*regexp.Regexp, error) {
	src := pattern
	if flags&1 != 0 {
		src = regexp.QuoteMeta(src)
	}
	prefix := "(?m)"
	if flags&2 != 0 {
		prefix = "(?mi)"
	}
	src = strings.NewReplacer(`\A`, `^`, `\Z`, `$`, `\z`, `$`, `\h`, `[0-9a-fA-F]`).Replace(src)
	src = regexp.MustCompile(`\(\?P=\w+\)`).ReplaceAllString(src, "")
	return regexp.Compile(prefix + src)
}

// classify returns Sublime-style point classes.
func classify(d *Doc, pt int) int {
	b := d.M.Buf
	p := d.pos(pt)
	line := b.Line(p.Line)
	var before, after rune
	if p.Col > 0 {
		before, _ = utf8.DecodeLastRuneInString(line[:p.Col])
	} else if p.Line > 0 {
		before = '\n'
	}
	if p.Col < len(line) {
		after, _ = utf8.DecodeRuneInString(line[p.Col:])
	} else if p.Line < b.LineCount()-1 {
		after = '\n'
	}
	w := func(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	punct := func(r rune) bool { return r != 0 && !w(r) && !unicode.IsSpace(r) }
	r := 0
	if w(after) && !w(before) {
		r |= 1
	}
	if w(before) && !w(after) {
		r |= 2
	}
	if punct(after) && !punct(before) {
		r |= 4
	}
	if punct(before) && !punct(after) {
		r |= 8
	}
	if (w(after) && !w(before)) || (unicode.IsLower(before) && unicode.IsUpper(after)) || (before == '_' && w(after)) {
		r |= 16
	}
	if (w(before) && !w(after)) || (unicode.IsLower(before) && unicode.IsUpper(after)) || (after == '_' && w(before)) {
		r |= 32
	}
	lineStart := before == 0 || before == '\n'
	lineEnd := after == 0 || after == '\n'
	if lineStart {
		r |= 64
	}
	if lineEnd {
		r |= 128
	}
	if lineStart && lineEnd {
		r |= 256
	}
	return r
}

// scopeAt approximates a scope name from the highlighter's token role.
func scopeAt(d *Doc, p buffer.Pos) string {
	base := baseScope(d.Lang)
	ext := base[strings.LastIndex(base, ".")+1:]
	for _, sp := range d.hl.Line(p.Line) {
		if sp.Start <= p.Col && p.Col < sp.End {
			if s := scopeForRole(sp.Role); s != "" {
				return base + " " + s + "." + ext + " "
			}
		}
	}
	return base + " "
}

var roleScopes = map[string]string{
	"comment": "comment", "keyword": "keyword", "storage": "storage.type", "string": "string.quoted",
	"number": "constant.numeric", "constant": "constant.language", "function": "entity.name.function",
	"call": "variable.function", "type": "storage.type", "tag": "entity.name.tag",
	"attribute": "entity.other.attribute-name", "operator": "keyword.operator", "builtin": "support.function",
	"punctuation": "punctuation", "heading": "markup.heading", "variable": "variable.other",
	"escape": "constant.character.escape", "invalid": "invalid",
}

func scopeForRole(role string) string { return roleScopes[role] }

func roleForScope(scope string) string {
	best, bestLen := "", 0
	for role, s := range roleScopes {
		for _, part := range strings.Fields(scope) {
			if strings.HasPrefix(part, s) && len(s) > bestLen {
				best, bestLen = role, len(s)
			}
		}
	}
	return best
}

func findBySelector(d *Doc, sel string) [][2]int {
	var out [][2]int
	sels := strings.Split(sel, ",")
	for l := 0; l < d.M.Buf.LineCount(); l++ {
		for _, sp := range d.hl.Line(l) {
			s := scopeForRole(sp.Role)
			if s == "" {
				continue
			}
			for _, want := range sels {
				if want = strings.TrimSpace(want); want != "" && strings.HasPrefix(s, strings.Fields(want)[len(strings.Fields(want))-1]) {
					out = append(out, [2]int{d.pt(buffer.Pos{Line: l, Col: sp.Start}), d.pt(buffer.Pos{Line: l, Col: sp.End})})
					break
				}
			}
		}
	}
	return out
}

func hexColor(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// htmlText reduces popup / phantom HTML to plain text.
func htmlText(s string) string {
	s = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?is)<style.*?</style>`).ReplaceAllString(s, "")
	s = tagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ").Replace(s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

// bufferWords lists words in d starting with prefix.
func bufferWords(d *Doc, prefix string, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for l := 0; l < d.M.Buf.LineCount() && len(out) < limit; l++ {
		line := d.M.Buf.Line(l)
		start := -1
		for i, r := range line + " " {
			isW := r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
			if isW && start < 0 {
				start = i
			} else if !isW && start >= 0 {
				w := line[start:i]
				start = -1
				if len(w) > len(prefix) && strings.HasPrefix(w, prefix) && !seen[w] {
					seen[w] = true
					out = append(out, w)
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Package resources

func packagesDir() string { return filepath.Join(settings.DataDir(), "Packages") }

// findResources returns "Packages/<pkg>/<file>" paths whose name matches pattern.
func findResources(pattern string) []string {
	var out []string
	root := packagesDir()
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() && strings.HasPrefix(e.Name(), ".") && p != root {
			return filepath.SkipDir
		}
		if !e.IsDir() {
			if ok, _ := filepath.Match(pattern, e.Name()); ok {
				rel, _ := filepath.Rel(filepath.Dir(root), p)
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func resourcePath(name string) string {
	name = strings.TrimPrefix(name, "/")
	if !strings.HasPrefix(name, "Packages/") {
		return ""
	}
	p := filepath.Join(filepath.Dir(packagesDir()), filepath.FromSlash(name))
	if _, err := os.Stat(p); err == nil {
		return p
	}
	// Legacy resource names map to inks ones and back.
	for _, pair := range [][2]string{{".sublime-", ".inks-"}, {".inks-", ".sublime-"}} {
		alt := strings.Replace(p, pair[0], pair[1], 1)
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	}
	return ""
}

// loadPackageResources adds palette entries and key bindings from packages
// (*.inks-commands / *.inks-keymap, and their legacy spellings).
func (a *App) loadPackageResources() {
	a.pkgPalette = nil
	var cmdFiles []string
	for _, pat := range []string{"*.inks-commands", "*.sublime-commands"} {
		cmdFiles = append(cmdFiles, findResources(pat)...)
	}
	for _, f := range cmdFiles {
		if strings.HasPrefix(f, "Packages/User/") && strings.Contains(f, "Default") {
			continue
		}
		data, err := os.ReadFile(resourcePath(f))
		if err != nil {
			continue
		}
		var entries []PaletteEntry
		var raw []struct {
			Caption string         `json:"caption"`
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		if parseJSONCInto(string(data), &raw) != nil {
			continue
		}
		for _, r := range raw {
			if r.Caption != "" && r.Command != "" {
				entries = append(entries, PaletteEntry{Caption: r.Caption, Command: r.Command, Args: r.Args})
			}
		}
		a.pkgPalette = append(a.pkgPalette, entries...)
	}
	var keymaps []string
	for _, pat := range []string{"Default (OSX).inks-keymap", "Default.inks-keymap", "Default (OSX).sublime-keymap", "Default.sublime-keymap"} {
		for _, f := range findResources(pat) {
			if !strings.HasPrefix(f, "Packages/User/") {
				keymaps = append(keymaps, resourcePath(f))
			}
		}
	}
	a.keymap.packageFiles = keymaps
	a.keymap.Load()
	a.loadSnippetsAndSchemes()
	a.menuStale = true
}

// runFromPlugin runs an editor or plugin command requested by a plugin.
func (a *App) runFromPlugin(name string, args map[string]any, target *Doc) {
	if _, ok := a.commands[name]; ok {
		if target != nil && target != a.doc() {
			// Text commands act on the active document; switch briefly.
			if gi, i := a.groupOf(target); gi >= 0 {
				prevG, prevI := a.focus, a.active
				a.setFocus(gi)
				a.active = i
				a.run(name, args)
				a.setFocus(prevG)
				if prevG == a.focus {
					a.active = prevI
				}
				return
			}
		}
		a.run(name, args)
		return
	}
	a.py.runCommand(name, args, target)
}
