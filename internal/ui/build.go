package ui

import (
	"bufio"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"inks/internal/buffer"
	"inks/internal/theme"
)

// Build systems: ⌘B runs the build for the current file, ⇧⌘B picks a
// variant. Output streams into a panel; file:line:col references are
// clickable and F4 / ⇧F4 step through them.

// BuildSystem is one way to build or run a file.
type BuildSystem struct {
	Name     string   `json:"name"`
	Cmd      string   `json:"cmd"`      // shell command; $file, $file_path, $file_name, $file_base_name, $folder
	Langs    []string `json:"syntax"`   // syntax names (lowercase match), empty = any
	Exts     []string `json:"ext"`      // file extensions without the dot
	Needs    string   `json:"needs"`    // a file that must exist in the folder tree (go.mod, Makefile…)
	WorkDir  string   `json:"work_dir"` // default $file_path
	FileExpr string   `json:"file_regex"`
}

var builtinBuilds = []BuildSystem{
	{Name: "Go: Build", Cmd: "go build ./... && go vet ./...", Langs: []string{"go"}, Needs: "go.mod"},
	{Name: "Go: Run", Cmd: "go run .", Langs: []string{"go"}},
	{Name: "Go: Test", Cmd: "go test ./...", Langs: []string{"go"}, Needs: "go.mod"},
	{Name: "Go: Run File", Cmd: "go run \"$file\"", Langs: []string{"go"}},
	{Name: "Terraform: Validate", Cmd: "terraform validate -no-color", Langs: []string{"terraform", "hcl"}, Exts: []string{"tf"}},
	{Name: "Terraform: Format Check", Cmd: "terraform fmt -check -diff -no-color", Langs: []string{"terraform", "hcl"}, Exts: []string{"tf", "tfvars"}},
	{Name: "Terraform: Plan", Cmd: "terraform plan -no-color", Langs: []string{"terraform", "hcl"}, Exts: []string{"tf"}},
	{Name: "Python", Cmd: "python3 -u \"$file\"", Langs: []string{"python", "python 3"}, Exts: []string{"py"}},
	{Name: "Node", Cmd: "node \"$file\"", Langs: []string{"javascript"}, Exts: []string{"js", "mjs", "cjs"}},
	{Name: "TypeScript (tsx)", Cmd: "npx --yes tsx \"$file\"", Langs: []string{"typescript"}, Exts: []string{"ts", "mts"}},
	{Name: "Shell", Cmd: "bash \"$file\"", Langs: []string{"bash", "sh", "zsh", "shell"}, Exts: []string{"sh", "bash", "zsh"}},
	{Name: "Ruby", Cmd: "ruby \"$file\"", Langs: []string{"ruby"}, Exts: []string{"rb"}},
	{Name: "Rust: Cargo Build", Cmd: "cargo build", Langs: []string{"rust"}, Needs: "Cargo.toml"},
	{Name: "Rust: Cargo Run", Cmd: "cargo run", Langs: []string{"rust"}, Needs: "Cargo.toml"},
	{Name: "Make", Cmd: "make", Needs: "Makefile"},
}

// Error references in build output: "path:line[:col]" (Go, gcc, Terraform,
// eslint…) and Python's 'File "x.py", line 3'.
var buildRefs = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(?:Error: |--> |at )?([^\s:"'()]+\.[A-Za-z0-9]+):(\d+)(?::(\d+))?`),
	regexp.MustCompile(`File "([^"]+)", line (\d+)`),
	regexp.MustCompile(`on ([^\s]+\.tf) line (\d+)`),
	regexp.MustCompile(`\(([^\s()]+\.[A-Za-z0-9]+):(\d+):(\d+)\)`),
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

type outLine struct {
	text      string
	file      string
	line, col int
	err       bool // stderr or failure text
}

// OutputPanel shows build output.
type OutputPanel struct {
	a        *App
	visible  bool
	title    string
	lines    []outLine
	scroll   int
	follow   bool
	running  bool
	cmd      *exec.Cmd
	started  time.Time
	cursor   int
	tag      struct{ _ int }
	closeTag struct{ _ int }
	mu       sync.Mutex
	pending  []outLine
	lastUsed map[string]string // syntax → build name
	gen      int

	mode    string // "build", "console" or "doc" (a plugin output panel)
	doc     *Doc
	input   widget.Editor // console prompt
	history []string
	histPos int
}

func newOutputPanel(a *App) *OutputPanel {
	o := &OutputPanel{a: a, follow: true, cursor: -1, lastUsed: map[string]string{}, mode: "build"}
	o.input.SingleLine = true
	o.input.Submit = true
	return o
}

func (o *OutputPanel) panelName() string {
	switch o.mode {
	case "console":
		return "console"
	case "doc":
		if o.doc != nil {
			return o.doc.Name
		}
	}
	return "output.build"
}

func (o *OutputPanel) count() int {
	switch o.mode {
	case "console":
		return len(o.a.console)
	case "doc":
		if o.doc != nil {
			return o.doc.M.Buf.LineCount()
		}
		return 0
	}
	return len(o.lines)
}

func (o *OutputPanel) lineAt(i int) outLine {
	switch o.mode {
	case "console":
		return o.a.console[i]
	case "doc":
		return outLine{text: strings.ReplaceAll(o.doc.M.Buf.Line(i), "\t", "    ")}
	}
	return o.lines[i]
}

// showPanel shows a named panel: console, output.<name>, find, replace,
// find_in_files.
func (a *App) showPanel(name string, toggle bool) {
	o := a.output
	switch {
	case name == "console":
		if toggle && o.visible && o.mode == "console" {
			o.visible = false
			a.focusEditor = true
			return
		}
		o.mode, o.visible, o.follow = "console", true, true
		a.exec(key.FocusCmd{Tag: &o.input})
		if !a.py.started && a.set.Bool("plugins_enabled") {
			a.py.start()
		}
	case strings.HasPrefix(name, "output."):
		n := strings.TrimPrefix(name, "output.")
		if d := a.py.panels[n]; d != nil {
			if toggle && o.visible && o.doc == d {
				o.visible = false
				return
			}
			o.mode, o.doc, o.visible, o.follow = "doc", d, true, true
			return
		}
		if toggle && o.visible && o.mode == "build" {
			o.visible = false
			return
		}
		o.mode, o.visible = "build", true
	case name == "find" || name == "incremental_find":
		a.find.show(false)
	case name == "replace":
		a.find.show(true)
	case name == "find_in_files":
		a.search.show()
	}
}

// buildsFor lists the build systems that apply to a document, user-defined first.
func (a *App) buildsFor(d *Doc) []BuildSystem {
	var all []BuildSystem
	if raw, ok := a.set.Get("build_systems").([]any); ok {
		var user []BuildSystem
		if remarshal(raw, &user) == nil {
			all = append(all, user...)
		}
	}
	all = append(all, builtinBuilds...)
	lang := ""
	ext := ""
	dir := a.defaultDir()
	if d != nil {
		lang = strings.ToLower(d.Lang)
		if d.Path != "" {
			ext = strings.TrimPrefix(filepath.Ext(d.Path), ".")
		}
	}
	var out []BuildSystem
	for _, b := range all {
		if b.Name == "" || b.Cmd == "" {
			continue
		}
		match := len(b.Langs) == 0 && len(b.Exts) == 0
		for _, l := range b.Langs {
			if strings.ToLower(l) == lang {
				match = true
			}
		}
		for _, e := range b.Exts {
			if e == ext {
				match = true
			}
		}
		if !match {
			continue
		}
		if b.Needs != "" && findUp(dir, b.Needs) == "" {
			continue
		}
		out = append(out, b)
	}
	return out
}

// findUp looks for name in dir and its parents.
func findUp(dir, name string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

// build runs the remembered (or first) build system; pick shows the list.
func (o *OutputPanel) build(pick bool) {
	a := o.a
	d := a.doc()
	builds := a.buildsFor(d)
	if len(builds) == 0 {
		a.setStatus("No build system for this file — add one under \"build_systems\" in Settings", 0)
		return
	}
	lang := ""
	if d != nil {
		lang = d.Lang
	}
	if pick && len(builds) > 1 {
		var items []QuickItem
		for _, b := range builds {
			items = append(items, QuickItem{Caption: b.Name, Detail: b.Cmd, Data: b})
		}
		a.pick(items, func(it *QuickItem) {
			b := it.Data.(BuildSystem)
			o.lastUsed[lang] = b.Name
			o.run(b)
		})
		return
	}
	chosen := builds[0]
	for _, b := range builds {
		if b.Name == o.lastUsed[lang] {
			chosen = b
		}
	}
	o.run(chosen)
}

func (o *OutputPanel) run(b BuildSystem) {
	a := o.a
	o.cancel()
	// Save first, like most editors do.
	for _, d := range a.allDocs() {
		if d.Path != "" && d.Dirty() {
			a.save(d, nil)
		}
	}
	d := a.doc()
	vars := map[string]string{"folder": a.defaultDir()}
	if len(a.folders) > 0 {
		vars["folder"] = a.folders[0]
	}
	if d != nil && d.Path != "" {
		vars["file"] = d.Path
		vars["file_path"] = filepath.Dir(d.Path)
		vars["file_name"] = filepath.Base(d.Path)
		vars["file_base_name"] = strings.TrimSuffix(filepath.Base(d.Path), filepath.Ext(d.Path))
	} else if d != nil {
		a.setStatus("Save the file first to build it", 0)
		return
	}
	expand := func(s string) string {
		return os.Expand(s, func(k string) string {
			if v, ok := vars[k]; ok {
				return v
			}
			return os.Getenv(k)
		})
	}
	cmdline := expand(b.Cmd)
	dir := vars["file_path"]
	if b.WorkDir != "" {
		dir = expand(b.WorkDir)
	} else if b.Needs != "" {
		if root := findUp(dir, b.Needs); root != "" {
			dir = root
		}
	}
	if dir == "" {
		dir = vars["folder"]
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	// A login shell, so PATH matches the terminal even when inks was
	// opened from Finder.
	cmd := exec.Command(shell, "-l", "-c", cmdline)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	o.gen++
	gen := o.gen
	o.mode = "build"
	o.visible = true
	o.lines = nil
	o.cursor = -1
	o.follow = true
	o.title = b.Name
	o.lines = append(o.lines, outLine{text: "$ " + cmdline + "   (in " + tildify(dir) + ")"})
	if err := cmd.Start(); err != nil {
		o.lines = append(o.lines, outLine{text: err.Error(), err: true})
		return
	}
	o.cmd = cmd
	o.running = true
	o.started = time.Now()
	flush := func() {
		o.mu.Lock()
		batch := o.pending
		o.pending = nil
		o.mu.Unlock()
		if len(batch) > 0 {
			a.Do(func() {
				if o.gen == gen {
					o.lines = append(o.lines, batch...)
				}
			})
		}
	}
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		last := time.Now()
		for sc.Scan() {
			o.mu.Lock()
			o.pending = append(o.pending, parseOutLine(sc.Text(), dir))
			o.mu.Unlock()
			if time.Since(last) > 80*time.Millisecond {
				flush()
				last = time.Now()
			}
		}
		flush()
	}()
	go func() {
		err := cmd.Wait()
		pw.Close()
		elapsed := time.Since(o.started).Round(10 * time.Millisecond)
		time.Sleep(50 * time.Millisecond) // let the reader drain
		flush()
		a.Do(func() {
			if o.gen != gen {
				return
			}
			o.running = false
			o.cmd = nil
			msg := fmt.Sprintf("[Finished in %s]", elapsed)
			isErr := false
			if err != nil {
				isErr = true
				if ee, ok := err.(*exec.ExitError); ok {
					msg = fmt.Sprintf("[Finished in %s with exit code %d]", elapsed, ee.ExitCode())
				} else {
					msg = fmt.Sprintf("[%v]", err)
				}
			}
			o.lines = append(o.lines, outLine{text: msg, err: isErr})
			refs := 0
			for _, l := range o.lines {
				if l.file != "" {
					refs++
				}
			}
			status := strings.Trim(msg, "[]")
			if refs > 0 {
				status += fmt.Sprintf(" — %d references, F4 to step through", refs)
			}
			a.setStatus(status, 6*time.Second)
		})
	}()
}

func (o *OutputPanel) cancel() {
	if o.cmd != nil && o.cmd.Process != nil {
		_ = syscall.Kill(-o.cmd.Process.Pid, syscall.SIGTERM)
		o.lines = append(o.lines, outLine{text: "[Cancelled]", err: true})
		o.gen++
		o.running = false
		o.cmd = nil
	}
}

func parseOutLine(text, dir string) outLine {
	text = ansiRe.ReplaceAllString(strings.TrimRight(text, "\r"), "")
	ol := outLine{text: strings.ReplaceAll(text, "\t", "    ")}
	low := strings.ToLower(text)
	ol.err = strings.Contains(low, "error") || strings.Contains(low, "panic:") || strings.HasPrefix(low, "fail") || strings.Contains(low, "traceback")
	for _, re := range buildRefs {
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			continue
		}
		ol.file = p
		ol.err = ol.err || !strings.Contains(low, "warning")
		ol.line, _ = strconv.Atoi(m[2])
		if len(m) > 3 && m[3] != "" {
			ol.col, _ = strconv.Atoi(m[3])
		}
		break
	}
	return ol
}

// openRef jumps to the file reference on output line i.
func (o *OutputPanel) openRef(i int) {
	a := o.a
	if i < 0 || i >= len(o.lines) || o.lines[i].file == "" {
		return
	}
	l := o.lines[i]
	o.cursor = i
	d := a.openPath(l.file)
	if d == nil {
		return
	}
	line := max(l.line-1, 0)
	col := 0
	if l.col > 0 {
		// Columns are usually 1-based characters.
		text := d.M.Buf.Line(min(line, d.M.Buf.LineCount()-1))
		col = len(string([]rune(text)[:min(l.col-1, len([]rune(text)))]))
	}
	p := d.M.Buf.Clamp(buffer.Pos{Line: line, Col: col})
	d.M.SetSels([]buffer.Range{buffer.Cursor(p)}, 0)
	d.reveal = true
	d.centerOnReveal = true
	a.focusEditor = true
	o.revealLine(i)
}

// step moves to the next or previous reference; false when there are none.
func (o *OutputPanel) step(dir int) bool {
	if o.mode != "build" {
		return false
	}
	var refs []int
	for i, l := range o.lines {
		if l.file != "" {
			refs = append(refs, i)
		}
	}
	if len(refs) == 0 {
		return false
	}
	k := -1
	for j, i := range refs {
		if i == o.cursor {
			k = j
		}
	}
	k = (k + dir + len(refs)) % len(refs)
	if k < 0 {
		k = 0
	}
	o.visible = true
	o.openRef(refs[k])
	o.a.setStatus(fmt.Sprintf("Reference %d of %d", k+1, len(refs)), 2*time.Second)
	return true
}

func (o *OutputPanel) revealLine(i int) {
	rowH := o.a.gtx.Dp(18)
	h := o.a.gtx.Dp(190)
	y := i * rowH
	if y < o.scroll || y > o.scroll+h-rowH*2 {
		o.scroll = y - h/2
	}
	o.follow = false
}

// Layout draws the output panel.
func (o *OutputPanel) Layout(gtx layout.Context) layout.Dimensions {
	a := o.a
	c := a.chrome
	w := gtx.Constraints.Max.X
	h := gtx.Dp(220)
	fullH := h
	headH := gtx.Dp(24)
	rowH := gtx.Dp(18)
	px := a.uiPx(gtx)
	monoPx := px - gtx.Dp(1)
	met := a.fonts.metrics(a.fonts.Mono, monoPx)
	fill(gtx.Ops, image.Rect(0, 0, w, h), c.Panel)
	fill(gtx.Ops, image.Rect(0, 0, w, 1), c.Border)

	// Header: title, state, close.
	small := px - gtx.Dp(2)
	title := o.title
	if o.running {
		title += "  — running… (⌃C cancels)"
	}
	switch o.mode {
	case "console":
		title = "Console — Python plugins log here; type Python below (e.g. view.file_name())"
	case "doc":
		title = o.panelName()
	}
	a.fonts.Draw(gtx.Ops, title, gtx.Dp(12), gtx.Dp(16), c.TabFg, a.fonts.UI, small)
	if a.closeButton(gtx, &o.closeTag, w, "Close the panel (Esc)") {
		o.visible = false
		a.focusEditor = true
		return layout.Dimensions{Size: image.Pt(w, h)}
	}

	// Console prompt.
	if o.mode == "console" {
		inH := gtx.Dp(26)
		h -= inH
		fill(gtx.Ops, image.Rect(0, h, w, h+1), c.Border)
		a.fonts.Draw(gtx.Ops, ">", gtx.Dp(12), h+inH/2+met.Ascent/2, c.OverlayDim, a.fonts.Mono, monoPx)
		for {
			ev, ok := o.input.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				src := o.input.Text()
				o.input.SetText("")
				if strings.TrimSpace(src) != "" {
					o.history = append(o.history, src)
					o.histPos = len(o.history)
					a.consoleLog(">>> "+src, false)
					a.py.eval(src)
					o.follow = true
				}
			}
		}
		for {
			ev, ok := gtx.Event(key.Filter{Focus: &o.input, Name: key.NameUpArrow}, key.Filter{Focus: &o.input, Name: key.NameDownArrow}, key.Filter{Focus: &o.input, Name: key.NameEscape})
			if !ok {
				break
			}
			e, ok := ev.(key.Event)
			if !ok || e.State != key.Press {
				continue
			}
			switch e.Name {
			case key.NameEscape:
				o.visible = false
				a.focusEditor = true
			case key.NameUpArrow:
				o.histPos = max(0, o.histPos-1)
			case key.NameDownArrow:
				o.histPos = min(len(o.history), o.histPos+1)
			}
			if e.Name != key.NameEscape {
				t := ""
				if o.histPos < len(o.history) {
					t = o.history[o.histPos]
				}
				o.input.SetText(t)
				o.input.SetCaret(len([]rune(t)), len([]rune(t)))
			}
		}
		off := op.Offset(image.Pt(gtx.Dp(26), h+(inH-met.Ascent-met.Descent)/2)).Push(gtx.Ops)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(w-gtx.Dp(40), met.Ascent+met.Descent+gtx.Dp(2)))
		o.input.Layout(g, a.fonts.Shaper, a.fonts.Mono, unit.Sp(float32(monoPx)/gtx.Metric.PxPerSp), colorMaterial(gtx.Ops, a.scheme.Fg), colorMaterial(gtx.Ops, a.scheme.Selection))
		off.Pop()
	}

	// Lines.
	top := headH
	listH := h - top
	total := o.count() * rowH
	maxScroll := max(0, total-listH+gtx.Dp(6))
	la := clip.Rect{Min: image.Pt(0, top), Max: image.Pt(w, h)}.Push(gtx.Ops)
	pointer.CursorDefault.Add(gtx.Ops)
	event.Op(gtx.Ops, &o.tag)
	la.Pop()
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &o.tag, Kinds: pointer.Press | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30}})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Scroll:
			o.scroll += int(e.Scroll.Y)
			o.follow = o.scroll >= maxScroll
		case pointer.Press:
			i := (int(e.Position.Y) - top + o.scroll) / rowH
			o.openRef(i)
		}
	}
	if o.follow {
		o.scroll = maxScroll
	}
	o.scroll = min(max(o.scroll, 0), maxScroll)
	cl := clip.Rect{Min: image.Pt(0, top), Max: image.Pt(w, h)}.Push(gtx.Ops)
	for i := o.scroll / rowH; i < o.count(); i++ {
		y := top + i*rowH - o.scroll
		if y > h {
			break
		}
		l := o.lineAt(i)
		fg := a.scheme.Fg
		switch {
		case l.err:
			fg = theme.Mix(a.scheme.Deleted, a.scheme.Fg, 0.25)
		case strings.HasPrefix(l.text, "[Finished") || strings.HasPrefix(l.text, "$ "):
			fg = c.OverlayDim
		}
		if i == o.cursor && o.mode == "build" {
			fill(gtx.Ops, image.Rect(0, y, w, y+rowH), c.OverlaySel)
		}
		base := y + rowH/2 + met.Ascent/2 - gtx.Dp(1)
		a.fonts.Draw(gtx.Ops, l.text, gtx.Dp(12), base, fg, a.fonts.Mono, monoPx)
		if l.file != "" {
			// Underline the reference.
			fill(gtx.Ops, image.Rect(gtx.Dp(12), base+gtx.Dp(2), gtx.Dp(12)+min(w-gtx.Dp(24), a.fonts.Width(l.text, a.fonts.Mono, monoPx)), base+gtx.Dp(2)+max(1, gtx.Dp(0.5))), theme.Alpha(fg, 0.35))
		}
	}
	cl.Pop()
	return layout.Dimensions{Size: image.Pt(w, fullH)}
}

const pluginTemplate = `import inks
import inks_plugin


# Run it from the console (View › Show Console) with: view.run_command("example")
# or bind it in Preferences › Key Bindings:
#   { "keys": ["super+alt+e"], "command": "example" }
class ExampleCommand(inks_plugin.TextCommand):
    def run(self, edit):
        for region in self.view.sel():
            self.view.insert(edit, region.begin(), "Hello from a plugin!")


class ExampleListener(inks_plugin.EventListener):
    def on_post_save(self, view):
        inks.status_message("Saved " + (view.file_name() or "untitled"))
`
