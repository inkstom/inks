package ui

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"inks/internal/buffer"
	"inks/internal/platform"
	"inks/internal/search"
	"inks/internal/theme"
)

// The side bar Search view: find and replace across the project and open
// files, with per-match and per-file selection.

const (
	maxSearchResults = 20000
	maxSearchFile    = 16 << 20
)

type lineMatch struct {
	m        search.Match
	line     int    // 0-based line of the match start
	col      int    // byte column of the match start
	endLine  int    // line of the match end
	endCol   int    // byte column of the match end
	preview  string // the line, trimmed around the match
	pfrom    int    // match range within preview
	pto      int
	excluded bool
}

type fileResult struct {
	path      string // "" for an untitled document
	rel       string
	doc       *Doc // set when the text came from an open document
	docVer    int
	matches   []lineMatch
	collapsed bool
}

type searchQuery struct {
	text, include, exclude      string
	regex, cas, word, structure bool
}

type replaceUndo struct {
	path          string
	before, after string
	doc           *Doc
	docVer        int
}

// SearchView holds the Search side bar state.
type SearchView struct {
	a                            *App
	find, repl, include, exclude widget.Editor
	opts                         map[string]bool
	details                      bool

	query     searchQuery
	matcher   *search.Matcher
	gen       int
	running   bool
	files     []*fileResult
	total     int
	truncated bool
	err       string
	pending   bool // the query changed; search when the debounce ends
	changedAt time.Time

	rows      []srow
	rowsDirty bool
	scroll    int
	hover     image.Point
	hovering  bool
	regions   []sregion
	tag       struct{ _ int }
	cursor    int // index into flattened matches for next/prev result
	undo      []replaceUndo
	undoCount int
}

type srow struct {
	file  int
	match int // -1 for the file row
}

type sregion struct {
	r     image.Rectangle
	kind  string
	file  int
	match int
	tip   string
}

func newSearchView(a *App) *SearchView {
	s := &SearchView{a: a, opts: map[string]bool{}, details: true, cursor: -1}
	s.include.SingleLine = true
	s.exclude.SingleLine = true
	return s
}

// show focuses the search field, seeded from the selection.
func (s *SearchView) show() {
	a := s.a
	a.sideMode = "search"
	a.showSidebar = true
	if d := a.doc(); d != nil {
		r := d.M.MainSel()
		if !r.Empty() {
			t := d.M.Buf.Slice(r.Start(), r.End())
			if len(t) < 2000 {
				s.find.SetText(t)
				s.queryChanged()
			}
		}
	}
	s.find.SetCaret(0, len([]rune(s.find.Text())))
	a.exec(key.FocusCmd{Tag: &s.find})
}

func (s *SearchView) snapshot() searchQuery {
	return searchQuery{
		text: s.find.Text(), include: s.include.Text(), exclude: s.exclude.Text(),
		regex: s.opts["regex"], cas: s.opts["case"], word: s.opts["word"], structure: s.opts["structural"],
	}
}

func (s *SearchView) queryChanged() {
	s.pending = true
	s.changedAt = time.Now()
	s.a.exec(op.InvalidateCmd{At: s.changedAt.Add(300 * time.Millisecond)})
}

type searchSource struct {
	path, rel string
	doc       *Doc
	text      string
	ver       int
}

// start runs the search in the background, replacing earlier results.
func (s *SearchView) start() {
	a := s.a
	s.pending = false
	q := s.snapshot()
	s.query = q
	s.gen++
	gen := s.gen
	s.files = nil
	s.total = 0
	s.truncated = false
	s.err = ""
	s.rowsDirty = true
	s.cursor = -1
	s.scroll = 0
	if q.text == "" {
		s.running = false
		s.matcher = nil
		return
	}
	m := search.New(q.text, search.Options{Regex: q.regex, CaseSensitive: q.cas, WholeWord: q.word, Structural: q.structure, MaxMatches: 5000})
	s.matcher = m
	if !m.Valid() {
		s.err = m.Err.Error()
		s.running = false
		return
	}
	// Open documents are searched as they are in the editor, saved or not.
	var sources []searchSource
	seen := map[string]bool{}
	for _, d := range a.allDocs() {
		if d.Scratch {
			continue
		}
		if d.Path == "" && len(a.folders) > 0 && d.M.Buf.Len() == 0 {
			continue
		}
		rel := d.Title()
		if d.Path != "" {
			rel = a.relPath(d.Path)
			seen[d.Path] = true
		}
		sources = append(sources, searchSource{path: d.Path, rel: rel, doc: d, text: d.M.Buf.Text("\n"), ver: d.M.Buf.Version()})
	}
	a.fileCache = nil
	filter := search.PathFilter(q.include, q.exclude)
	for _, p := range a.projectFiles() {
		if seen[p] {
			continue
		}
		rel := a.relPath(p)
		if !filter(rel) {
			continue
		}
		sources = append(sources, searchSource{path: p, rel: rel})
	}
	// Apply include/exclude to open documents too.
	if q.include != "" || q.exclude != "" {
		kept := sources[:0]
		for _, src := range sources {
			if src.doc == nil || filter(src.rel) {
				kept = append(kept, src)
			}
		}
		sources = kept
	}
	s.running = true

	jobs := make(chan searchSource)
	results := make(chan *fileResult, 64)
	var wg sync.WaitGroup
	var stop bool
	var stopMu sync.Mutex
	stopped := func() bool {
		stopMu.Lock()
		defer stopMu.Unlock()
		return stop
	}
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobs {
				if stopped() {
					continue
				}
				if fr := searchOne(m, src); fr != nil {
					results <- fr
				}
			}
		}()
	}
	go func() {
		for _, src := range sources {
			jobs <- src
			if stopped() {
				break
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	go func() {
		var batch []*fileResult
		total := 0
		flush := func(done bool) {
			b := batch
			batch = nil
			truncated := total >= maxSearchResults
			a.Do(func() {
				if s.gen != gen {
					return
				}
				s.files = append(s.files, b...)
				sort.SliceStable(s.files, func(i, j int) bool { return pathLess(s.files[i].rel, s.files[j].rel) })
				s.total = 0
				for _, f := range s.files {
					s.total += len(f.matches)
				}
				s.truncated = truncated
				s.rowsDirty = true
				if done {
					s.running = false
				}
			})
		}
		last := time.Now()
		for fr := range results {
			if total >= maxSearchResults {
				continue
			}
			if total+len(fr.matches) > maxSearchResults {
				fr.matches = fr.matches[:maxSearchResults-total]
			}
			total += len(fr.matches)
			batch = append(batch, fr)
			if total >= maxSearchResults {
				stopMu.Lock()
				stop = true
				stopMu.Unlock()
			}
			if time.Since(last) > 120*time.Millisecond {
				flush(false)
				last = time.Now()
			}
		}
		flush(true)
	}()
}

func pathLess(a, b string) bool {
	da, db := filepath.Dir(a), filepath.Dir(b)
	if da != db {
		return naturalLess(da, db)
	}
	return naturalLess(filepath.Base(a), filepath.Base(b))
}

// searchOne finds all matches in one file or document.
func searchOne(m *search.Matcher, src searchSource) *fileResult {
	text := src.text
	if src.doc == nil {
		st, err := os.Stat(src.path)
		if err != nil || st.IsDir() || st.Size() > maxSearchFile {
			return nil
		}
		data, err := os.ReadFile(src.path)
		if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil
		}
		if !utf8.Valid(data) {
			return nil
		}
		text = strings.TrimPrefix(string(data), "\ufeff")
	}
	ms := m.FindAll(text, 0, 0)
	if len(ms) == 0 {
		return nil
	}
	fr := &fileResult{path: src.path, rel: src.rel, doc: src.doc, docVer: src.ver}
	line, lineStart, pos := 0, 0, 0
	advance := func(to int) {
		for pos < to {
			k := strings.IndexByte(text[pos:to], '\n')
			if k < 0 {
				pos = to
				break
			}
			pos += k + 1
			line++
			lineStart = pos
		}
	}
	for _, mt := range ms {
		advance(mt.From)
		lm := lineMatch{m: mt, line: line, col: mt.From - lineStart}
		end := strings.IndexByte(text[lineStart:], '\n')
		lineText := text[lineStart:]
		if end >= 0 {
			lineText = text[lineStart : lineStart+end]
		}
		lineText = strings.TrimSuffix(lineText, "\r")
		lm.preview, lm.pfrom, lm.pto = trimPreview(lineText, lm.col, lm.col+len(mt.Text))
		// The end position (matches may span lines).
		nl := strings.Count(mt.Text, "\n")
		lm.endLine = line + nl
		if nl == 0 {
			lm.endCol = lm.col + len(mt.Text)
		} else {
			lm.endCol = len(mt.Text) - strings.LastIndexByte(mt.Text, '\n') - 1
		}
		fr.matches = append(fr.matches, lm)
	}
	return fr
}

// trimPreview shortens a long line around the match [from, to).
func trimPreview(line string, from, to int) (string, int, int) {
	if to > len(line) {
		to = len(line)
	}
	const before, maxLen = 30, 300
	start := 0
	if from > before+10 {
		start = from - before
		for start < len(line) && !utf8.RuneStart(line[start]) {
			start++
		}
	}
	// Leading indentation is noise in a narrow list.
	for start < from && (line[start] == ' ' || line[start] == '\t') {
		start++
	}
	end := len(line)
	if end-start > maxLen {
		end = max(start+maxLen, to)
		for end < len(line) && !utf8.RuneStart(line[end]) {
			end++
		}
	}
	prefix := ""
	if start > 0 && start > len(line)-len(strings.TrimLeft(line, " \t")) {
		prefix = "…"
	}
	out := prefix + line[start:end]
	shift := len(prefix) - start
	return out, from + shift, to + shift
}

// ---------------------------------------------------------------------------
// Results

func (s *SearchView) buildRows() {
	s.rows = s.rows[:0]
	for fi, f := range s.files {
		s.rows = append(s.rows, srow{file: fi, match: -1})
		if f.collapsed {
			continue
		}
		for mi := range f.matches {
			s.rows = append(s.rows, srow{file: fi, match: mi})
		}
	}
	s.rowsDirty = false
}

func (s *SearchView) included() (matches, files int) {
	for _, f := range s.files {
		n := 0
		for _, m := range f.matches {
			if !m.excluded {
				n++
			}
		}
		if n > 0 {
			files++
			matches += n
		}
	}
	return
}

// open shows a match in the editor.
func (s *SearchView) open(fi, mi int) {
	a := s.a
	if fi < 0 || fi >= len(s.files) {
		return
	}
	f := s.files[fi]
	var d *Doc
	if f.doc != nil && a.isOpen(f.doc) {
		d = f.doc
		a.activate(d)
	} else if f.path != "" {
		d = a.openPath(f.path)
	}
	if d == nil {
		return
	}
	if mi >= 0 && mi < len(f.matches) {
		m := f.matches[mi]
		from := d.M.Buf.Clamp(buffer.Pos{Line: m.line, Col: m.col})
		to := d.M.Buf.Clamp(buffer.Pos{Line: m.endLine, Col: m.endCol})
		d.M.SetSels([]buffer.Range{{Anchor: from, Head: to, Goal: -1}}, 0)
		d.reveal = true
		d.centerOnReveal = true
	}
	a.focusEditor = true
}

// step moves to the next or previous result (F4 / ⇧F4).
func (s *SearchView) step(dir int) {
	type ref struct{ f, m int }
	var all []ref
	for fi, f := range s.files {
		for mi, m := range f.matches {
			if !m.excluded {
				all = append(all, ref{fi, mi})
			}
		}
	}
	if len(all) == 0 {
		s.a.setStatus("No search results", 0)
		return
	}
	s.cursor = (s.cursor + dir + len(all)) % len(all)
	if s.cursor < 0 {
		s.cursor = 0
	}
	r := all[s.cursor]
	s.files[r.f].collapsed = false
	s.rowsDirty = true
	s.open(r.f, r.m)
	s.a.setStatus(fmt.Sprintf("Result %d of %d", s.cursor+1, len(all)), 2*time.Second)
	s.revealRow(r.f, r.m)
}

func (s *SearchView) revealRow(fi, mi int) {
	s.buildRows()
	for i, r := range s.rows {
		if r.file == fi && r.match == mi {
			y := i * s.a.gtx.Dp(22)
			if y < s.scroll {
				s.scroll = y
			} else if y > s.scroll+s.a.gtx.Dp(300) {
				s.scroll = y - s.a.gtx.Dp(150)
			}
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Replace

// replace replaces the included matches of the given files (all when nil),
// or a single match when mi >= 0.
func (s *SearchView) replace(only []int, mi int, confirm bool) {
	a := s.a
	if s.matcher == nil || !s.matcher.Valid() {
		return
	}
	targets := only
	if targets == nil {
		for i := range s.files {
			targets = append(targets, i)
		}
	}
	type job struct {
		f    *fileResult
		pick map[int]bool // indexes of matches to replace
	}
	var jobs []job
	count := 0
	for _, fi := range targets {
		if fi < 0 || fi >= len(s.files) {
			continue
		}
		f := s.files[fi]
		pick := map[int]bool{}
		for i, m := range f.matches {
			if (mi >= 0 && i == mi) || (mi < 0 && !m.excluded) {
				pick[i] = true
			}
		}
		if len(pick) > 0 {
			jobs = append(jobs, job{f, pick})
			count += len(pick)
		}
	}
	if count == 0 {
		a.setStatus("Nothing to replace", 0)
		return
	}
	template := s.repl.Text()
	preserve := s.opts["preserve"]
	m := s.matcher
	run := func() {
		var undo []replaceUndo
		replaced, files, stale := 0, 0, 0
		for _, j := range jobs {
			n, u, ok := replaceInFile(a, m, j.f, j.pick, template, preserve)
			if !ok {
				stale++
				continue
			}
			if n > 0 {
				replaced += n
				files++
				undo = append(undo, u)
			}
		}
		if len(undo) > 0 {
			s.undo = undo
			s.undoCount = replaced
		}
		msg := fmt.Sprintf("Replaced %d occurrences in %d files", replaced, files)
		if stale > 0 {
			msg += fmt.Sprintf(" (%d changed since the search — search again)", stale)
		}
		if replaced > 0 {
			msg += " — Find › Undo Replace in Files to revert"
		}
		a.setStatus(msg, 8*time.Second)
		a.tree.refresh()
		s.start()
	}
	if !confirm {
		run()
		return
	}
	with := "“" + template + "”"
	if template == "" {
		with = "nothing (delete them)"
	}
	go func() {
		choice := platform.Ask(fmt.Sprintf("Replace %d occurrences across %d files with %s?", count, len(jobs), with),
			"Files that are not open are changed on disk. Open files are changed in the editor and can be undone with ⌘Z.",
			[]string{"Cancel", "Replace"}, 1, 0)
		if choice == 1 {
			a.Do(run)
		}
	}()
}

// replaceInFile re-runs the search on the current text and replaces the
// picked matches that are still where they were.
func replaceInFile(a *App, m *search.Matcher, f *fileResult, pick map[int]bool, template string, preserve bool) (int, replaceUndo, bool) {
	same := func(x, y search.Match) bool { return x.From == y.From && x.To == y.To && x.Text == y.Text }
	if f.doc != nil && a.isOpen(f.doc) {
		d := f.doc
		text := d.M.Buf.Text("\n")
		cur := m.FindAll(text, 0, 0)
		var edits []buffer.Edit
		for i, lm := range f.matches {
			if !pick[i] {
				continue
			}
			for _, c := range cur {
				if same(c, lm.m) {
					edits = append(edits, buffer.Edit{Start: d.M.Buf.PosAt(c.From), End: d.M.Buf.PosAt(c.To), Text: m.Replacement(c, template, preserve)})
					break
				}
			}
		}
		if len(edits) < len(pick) && d.M.Buf.Version() != f.docVer {
			return 0, replaceUndo{}, false
		}
		sels := d.M.Buf.Apply(edits, d.M.Sels, buffer.ApplyOptions{Kind: "replace"})
		d.M.SetSels(sels, min(d.M.Main, len(sels)-1))
		return len(edits), replaceUndo{doc: d, docVer: d.M.Buf.Version()}, true
	}
	if f.path == "" {
		return 0, replaceUndo{}, false
	}
	st, err := os.Stat(f.path)
	if err != nil {
		return 0, replaceUndo{}, false
	}
	data, err := os.ReadFile(f.path)
	if err != nil || !utf8.Valid(data) {
		return 0, replaceUndo{}, false
	}
	raw := string(data)
	bom := strings.HasPrefix(raw, "\ufeff")
	text := strings.TrimPrefix(raw, "\ufeff")
	cur := m.FindAll(text, 0, 0)
	var sb strings.Builder
	last, n := 0, 0
	ci := 0
	for i, lm := range f.matches {
		if !pick[i] {
			continue
		}
		for ci < len(cur) && cur[ci].From < lm.m.From {
			ci++
		}
		if ci >= len(cur) || !same(cur[ci], lm.m) {
			return 0, replaceUndo{}, false
		}
		c := cur[ci]
		sb.WriteString(text[last:c.From])
		sb.WriteString(m.Replacement(c, template, preserve))
		last = c.To
		n++
	}
	sb.WriteString(text[last:])
	out := sb.String()
	if bom {
		out = "\ufeff" + out
	}
	if err := os.WriteFile(f.path, []byte(out), st.Mode().Perm()); err != nil {
		a.setStatus("Unable to write "+tildify(f.path)+": "+err.Error(), 0)
		return 0, replaceUndo{}, false
	}
	return n, replaceUndo{path: f.path, before: raw, after: out}, true
}

// undoReplace reverts the last Replace in Files.
func (s *SearchView) undoReplace() {
	a := s.a
	if len(s.undo) == 0 {
		a.setStatus("Nothing to undo", 0)
		return
	}
	restored, skipped := 0, 0
	for _, u := range s.undo {
		if u.doc != nil {
			if a.isOpen(u.doc) && u.doc.M.Buf.Version() == u.docVer {
				if sels, ok := u.doc.M.Buf.Undo(); ok {
					u.doc.M.SetSels(sels, len(sels)-1)
					restored++
					continue
				}
			}
			skipped++
			continue
		}
		data, err := os.ReadFile(u.path)
		if err != nil || string(data) != u.after {
			skipped++
			continue
		}
		st, _ := os.Stat(u.path)
		mode := os.FileMode(0o644)
		if st != nil {
			mode = st.Mode().Perm()
		}
		if os.WriteFile(u.path, []byte(u.before), mode) == nil {
			restored++
		} else {
			skipped++
		}
	}
	s.undo = nil
	msg := fmt.Sprintf("Restored %d files", restored)
	if skipped > 0 {
		msg += fmt.Sprintf("; %d were changed since and left alone", skipped)
	}
	a.setStatus(msg, 6*time.Second)
	s.start()
}

// ---------------------------------------------------------------------------
// Layout

func (s *SearchView) Layout(gtx layout.Context, top int) {
	a := s.a
	c := a.chrome
	w := gtx.Constraints.Max.X
	h := gtx.Constraints.Max.Y
	px := a.uiPx(gtx)
	monoPx := px - gtx.Dp(1)
	met := a.fonts.metrics(a.fonts.UI, px)
	mmet := a.fonts.metrics(a.fonts.Mono, monoPx)

	// Pointer events for everything except the text fields (drawn on top).
	area := clip.Rect{Min: image.Pt(0, top), Max: image.Pt(w, h)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &s.tag)
	area.Pop()
	s.handlePointer(gtx)

	// Field keys and edits.
	for _, ed := range []*widget.Editor{&s.find, &s.repl, &s.include, &s.exclude} {
		for {
			ev, ok := gtx.Event(
				key.Filter{Focus: ed, Name: key.NameEscape},
				key.Filter{Focus: ed, Name: key.NameReturn},
				key.Filter{Focus: ed, Name: key.NameReturn, Required: key.ModAlt},
			)
			if !ok {
				break
			}
			e, ok := ev.(key.Event)
			if !ok || e.State != key.Press {
				continue
			}
			switch {
			case e.Name == key.NameEscape:
				a.focusEditor = true
			case ed == &s.repl && e.Modifiers.Contain(key.ModAlt):
				s.replace(nil, -1, true)
			default:
				s.start()
			}
		}
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.ChangeEvent); ok {
				if ed != &s.repl {
					s.queryChanged()
				}
			}
		}
	}
	if s.pending && !gtx.Now.Before(s.changedAt.Add(300*time.Millisecond)) {
		s.start()
	}

	s.regions = s.regions[:0]
	pad := gtx.Dp(10)
	y := top + gtx.Dp(4)
	lineH := mmet.Ascent + mmet.Descent + gtx.Dp(3)

	field := func(ed *widget.Editor, placeholder string, multi bool) {
		lines := 1
		if multi {
			lines = min(6, strings.Count(ed.Text(), "\n")+1)
		}
		fh := lines*lineH + gtx.Dp(10)
		r := image.Rect(pad, y, w-pad, y+fh)
		fillRound(gtx.Ops, r, gtx.Dp(4), c.Input)
		border := c.Border
		if gtx.Focused(ed) {
			border = c.Accent
		}
		stroke(gtx.Ops, r, gtx.Dp(4), 1, border)
		if ed.Len() == 0 {
			a.fonts.Draw(gtx.Ops, placeholder, r.Min.X+gtx.Dp(7), r.Min.Y+gtx.Dp(5)+mmet.Ascent, c.OverlayDim, a.fonts.UI, px-gtx.Dp(1))
		}
		off := op.Offset(image.Pt(r.Min.X+gtx.Dp(7), r.Min.Y+gtx.Dp(5))).Push(gtx.Ops)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(r.Dx()-gtx.Dp(14), lines*lineH))
		cl := clip.Rect{Max: g.Constraints.Max}.Push(gtx.Ops)
		ed.Layout(g, a.fonts.Shaper, a.fonts.Mono, unit.Sp(float32(monoPx)/gtx.Metric.PxPerSp), colorMaterial(gtx.Ops, a.scheme.Fg), colorMaterial(gtx.Ops, a.scheme.Selection))
		cl.Pop()
		off.Pop()
		y += fh + gtx.Dp(6)
	}
	field(&s.find, "Search  (⇧↩ for a new line)", true)
	field(&s.repl, "Replace  (⌥↩ replaces all)", true)

	// Toggles.
	x := pad
	th := gtx.Dp(22)
	for _, t := range findToggles {
		tw := max(gtx.Dp(26), a.fonts.Width(t.label, a.fonts.Mono, monoPx-gtx.Dp(1))+gtx.Dp(10))
		r := image.Rect(x, y, x+tw, y+th)
		fg := c.TabFg
		if s.opts[t.key] {
			fillRound(gtx.Ops, r, gtx.Dp(4), c.OverlaySel)
			stroke(gtx.Ops, r, gtx.Dp(4), 1, c.Accent)
			fg = c.TabActiveFg
		}
		lw := a.fonts.Width(t.label, a.fonts.Mono, monoPx-gtx.Dp(1))
		a.fonts.Draw(gtx.Ops, t.label, x+(tw-lw)/2, y+th/2+mmet.Ascent/2-gtx.Dp(1), fg, a.fonts.Mono, monoPx-gtx.Dp(1))
		s.regions = append(s.regions, sregion{r: r, kind: "toggle:" + t.key, tip: strings.Split(t.tip, " (")[0]})
		x += tw + gtx.Dp(2)
	}
	// Details toggle (include / exclude).
	dr := image.Rect(w-pad-gtx.Dp(26), y, w-pad, y+th)
	if s.details {
		fillRound(gtx.Ops, dr, gtx.Dp(4), c.OverlaySel)
	}
	a.fonts.Draw(gtx.Ops, "⋯", dr.Min.X+gtx.Dp(7), y+th/2+met.Ascent/2-gtx.Dp(1), c.TabFg, a.fonts.UI, px)
	s.regions = append(s.regions, sregion{r: dr, kind: "details", tip: "Show files to include / exclude"})
	y += th + gtx.Dp(6)
	if s.details {
		field(&s.include, "files to include  (*.tf, modules/**)", false)
		field(&s.exclude, "files to exclude  (*.lock, vendor/**)", false)
	}

	// Summary and actions.
	small := px - gtx.Dp(2)
	summary := ""
	switch {
	case s.err != "":
		summary = "Invalid: " + s.err
	case s.query.text == "":
		summary = "Type to search the project and open files"
	case s.running && s.total == 0:
		summary = "Searching…"
	case s.total == 0:
		summary = "No results"
	default:
		summary = fmt.Sprintf("%d results in %d files", s.total, len(s.files))
		if s.truncated {
			summary += " (limit reached)"
		}
		if s.running {
			summary += "…"
		}
	}
	a.fonts.Draw(gtx.Ops, a.fonts.Elide(summary, a.fonts.UI, small, w-2*pad-gtx.Dp(90)), pad, y+gtx.Dp(15), c.SidebarDim, a.fonts.UI, small)
	if s.total > 0 {
		label := "Replace All"
		bw := a.fonts.Width(label, a.fonts.UI, small) + gtx.Dp(14)
		br := image.Rect(w-pad-bw, y, w-pad, y+gtx.Dp(21))
		fillRound(gtx.Ops, br, gtx.Dp(4), c.Accent)
		a.fonts.Draw(gtx.Ops, label, br.Min.X+gtx.Dp(7), y+gtx.Dp(15), theme.Hex("#ffffff"), a.fonts.UI, small)
		n, nf := s.included()
		s.regions = append(s.regions, sregion{r: br, kind: "replace_all", tip: fmt.Sprintf("Replace the %d checked results in %d files (⌥↩ in the Replace field)", n, nf)})
	} else if len(s.undo) > 0 {
		label := "Undo Replace"
		bw := a.fonts.Width(label, a.fonts.UI, small) + gtx.Dp(14)
		br := image.Rect(w-pad-bw, y, w-pad, y+gtx.Dp(21))
		fillRound(gtx.Ops, br, gtx.Dp(4), c.OverlaySel)
		a.fonts.Draw(gtx.Ops, label, br.Min.X+gtx.Dp(7), y+gtx.Dp(15), c.TabActiveFg, a.fonts.UI, small)
		s.regions = append(s.regions, sregion{r: br, kind: "undo", tip: fmt.Sprintf("Revert the last replace (%d occurrences)", s.undoCount)})
	}
	y += gtx.Dp(28)
	fill(gtx.Ops, image.Rect(0, y-gtx.Dp(3), w, y-gtx.Dp(2)), c.Border)

	// Result rows.
	if s.rowsDirty {
		s.buildRows()
	}
	listTop := y
	rowH := gtx.Dp(22)
	maxScroll := max(0, len(s.rows)*rowH-(h-listTop))
	s.scroll = min(max(s.scroll, 0), maxScroll)
	cl := clip.Rect{Min: image.Pt(0, listTop), Max: image.Pt(w, h)}.Push(gtx.Ops)
	showRepl := s.repl.Len() > 0 && s.matcher != nil && s.matcher.Valid()
	first := s.scroll / rowH
	for i := first; i < len(s.rows); i++ {
		ry := listTop + i*rowH - s.scroll
		if ry > h {
			break
		}
		row := s.rows[i]
		f := s.files[row.file]
		rr := image.Rect(0, ry, w, ry+rowH)
		hot := s.hovering && s.hover.In(rr)
		if hot {
			fill(gtx.Ops, rr, c.SidebarHover)
		}
		base := ry + rowH/2 + met.Ascent/2 - gtx.Dp(1)
		if row.match < 0 {
			// File row: chevron, checkbox, name, folder, count.
			arrow := "▾"
			if f.collapsed {
				arrow = "▸"
			}
			a.fonts.Draw(gtx.Ops, arrow, gtx.Dp(6), base, c.SidebarDim, a.fonts.UI, px)
			n := 0
			for _, m := range f.matches {
				if !m.excluded {
					n++
				}
			}
			cb := s.checkbox(gtx, gtx.Dp(20), ry+rowH/2, n == len(f.matches), n > 0 && n < len(f.matches))
			s.regions = append(s.regions, sregion{r: cb.Inset(-gtx.Dp(3)), kind: "check_file", file: row.file, match: -1, tip: "Include or skip this file when replacing"})
			nx := gtx.Dp(38)
			name := filepath.Base(f.rel)
			if f.path == "" {
				name = f.rel
			}
			if a.set.Bool("sidebar_icons") {
				a.fonts.drawIcon(gtx.Ops, name, false, false, !a.scheme.Dark, nx, base, c.SidebarDim, px)
				nx += gtx.Dp(18)
			}
			countText := fmt.Sprint(len(f.matches))
			cw := a.fonts.Width(countText, a.fonts.UI, small) + gtx.Dp(10)
			right := w - pad - cw
			if hot {
				right -= gtx.Dp(44)
			}
			nw := a.fonts.Draw(gtx.Ops, a.fonts.Elide(name, a.fonts.UI, px, right-nx-gtx.Dp(4)), nx, base, c.TabActiveFg, a.fonts.UI, px)
			if dir := filepath.Dir(f.rel); dir != "." && f.path != "" && nx+nw+gtx.Dp(30) < right {
				a.fonts.Draw(gtx.Ops, a.fonts.Elide(dir, a.fonts.UI, small, right-nx-nw-gtx.Dp(12)), nx+nw+gtx.Dp(6), base, c.SidebarDim, a.fonts.UI, small)
			}
			if f.doc != nil && f.doc.Dirty() {
				a.fonts.Draw(gtx.Ops, "•", nx-gtx.Dp(8), base, c.Accent, a.fonts.UI, px)
			}
			badge := image.Rect(w-pad-cw, ry+gtx.Dp(4), w-pad, ry+rowH-gtx.Dp(4))
			fillRound(gtx.Ops, badge, badge.Dy()/2, c.OverlaySel)
			a.fonts.Draw(gtx.Ops, countText, badge.Min.X+gtx.Dp(5), base-gtx.Dp(1), c.SidebarFg, a.fonts.UI, small)
			if hot {
				s.rowButtons(gtx, rr, right+gtx.Dp(4), base, row)
			}
			s.regions = append(s.regions, sregion{r: rr, kind: "file", file: row.file, match: -1})
			continue
		}
		// Match row: checkbox, line number, preview with the match marked.
		m := f.matches[row.match]
		cb := s.checkbox(gtx, gtx.Dp(24), ry+rowH/2, !m.excluded, false)
		s.regions = append(s.regions, sregion{r: cb.Inset(-gtx.Dp(3)), kind: "check", file: row.file, match: row.match, tip: "Include or skip this match when replacing"})
		lx := gtx.Dp(40)
		ln := fmt.Sprint(m.line + 1)
		a.fonts.Draw(gtx.Ops, ln, lx, base, c.SidebarDim, a.fonts.Mono, small)
		tx := lx + max(gtx.Dp(28), a.fonts.Width(ln, a.fonts.Mono, small)+gtx.Dp(8))
		limit := w - pad
		if hot {
			limit -= gtx.Dp(44)
		}
		pcl := clip.Rect{Min: image.Pt(tx, ry), Max: image.Pt(limit, ry+rowH)}.Push(gtx.Ops)
		mb := ry + rowH/2 + mmet.Ascent/2 - gtx.Dp(1)
		pre, mid, post := m.preview[:m.pfrom], m.preview[m.pfrom:m.pto], m.preview[m.pto:]
		dim := c.SidebarFg
		if m.excluded {
			dim = c.SidebarDim
		}
		tx += a.fonts.Draw(gtx.Ops, pre, tx, mb, dim, a.fonts.Mono, monoPx)
		mw := a.fonts.Width(mid, a.fonts.Mono, monoPx)
		hr := image.Rect(tx, ry+gtx.Dp(3), tx+mw, ry+rowH-gtx.Dp(3))
		if showRepl && !m.excluded {
			fill(gtx.Ops, hr, theme.Alpha(a.scheme.Deleted, 0.3))
			a.fonts.Draw(gtx.Ops, mid, tx, mb, c.TabActiveFg, a.fonts.Mono, monoPx)
			fill(gtx.Ops, image.Rect(tx, (hr.Min.Y+hr.Max.Y)/2, tx+mw, (hr.Min.Y+hr.Max.Y)/2+max(1, gtx.Dp(1))), c.TabActiveFg)
			tx += mw
			rep := s.matcher.Replacement(m.m, s.repl.Text(), s.opts["preserve"])
			rep = strings.ReplaceAll(rep, "\n", "⏎")
			rw := a.fonts.Width(rep, a.fonts.Mono, monoPx)
			fill(gtx.Ops, image.Rect(tx, hr.Min.Y, tx+rw, hr.Max.Y), theme.Alpha(a.scheme.Added, 0.3))
			tx += a.fonts.Draw(gtx.Ops, rep, tx, mb, c.TabActiveFg, a.fonts.Mono, monoPx)
		} else {
			fill(gtx.Ops, hr, theme.Alpha(c.Match, 0.45))
			tx += a.fonts.Draw(gtx.Ops, mid, tx, mb, c.TabActiveFg, a.fonts.Mono, monoPx)
		}
		a.fonts.Draw(gtx.Ops, post, tx, mb, dim, a.fonts.Mono, monoPx)
		pcl.Pop()
		if hot {
			s.rowButtons(gtx, rr, limit+gtx.Dp(4), base, row)
		}
		s.regions = append(s.regions, sregion{r: rr, kind: "match", file: row.file, match: row.match})
	}
	cl.Pop()
	if len(s.rows)*rowH > h-listTop {
		// Scroll indicator.
		total := len(s.rows) * rowH
		vis := h - listTop
		th := max(gtx.Dp(20), vis*vis/total)
		ty := listTop + (vis-th)*s.scroll/max(1, total-vis)
		fillRound(gtx.Ops, image.Rect(w-gtx.Dp(6), ty, w-gtx.Dp(2), ty+th), gtx.Dp(2), theme.Alpha(c.SidebarFg, 0.3))
	}
}

func (s *SearchView) checkbox(gtx layout.Context, x, cy int, on, partial bool) image.Rectangle {
	c := s.a.chrome
	sz := gtx.Dp(12)
	r := image.Rect(x, cy-sz/2, x+sz, cy+sz/2)
	if on || partial {
		fillRound(gtx.Ops, r, gtx.Dp(3), c.Accent)
		if partial {
			fill(gtx.Ops, image.Rect(r.Min.X+gtx.Dp(3), cy-gtx.Dp(1), r.Max.X-gtx.Dp(3), cy+gtx.Dp(1)), theme.Hex("#ffffff"))
		} else {
			s.a.fonts.Draw(gtx.Ops, "✓", r.Min.X+gtx.Dp(1), r.Max.Y-gtx.Dp(2), theme.Hex("#ffffff"), s.a.fonts.UI, gtx.Dp(11))
		}
	} else {
		stroke(gtx.Ops, r, gtx.Dp(3), 1, c.SidebarDim)
	}
	return r
}

// rowButtons draws the hover actions (replace ↻, dismiss ×) at x.
func (s *SearchView) rowButtons(gtx layout.Context, rr image.Rectangle, x, base int, row srow) {
	c := s.a.chrome
	px := s.a.uiPx(gtx)
	bw := gtx.Dp(20)
	{
		r := image.Rect(x, rr.Min.Y+gtx.Dp(1), x+bw, rr.Max.Y-gtx.Dp(1))
		s.a.fonts.Draw(gtx.Ops, "↻", x+gtx.Dp(4), base, c.TabActiveFg, s.a.fonts.UI, px)
		tip := "Replace this match"
		if row.match < 0 {
			tip = "Replace the checked matches in this file"
		}
		s.regions = append(s.regions, sregion{r: r, kind: "replace_one", file: row.file, match: row.match, tip: tip})
		x += bw
	}
	r := image.Rect(x, rr.Min.Y+gtx.Dp(1), x+bw, rr.Max.Y-gtx.Dp(1))
	s.a.fonts.Draw(gtx.Ops, "×", x+gtx.Dp(5), base, c.TabActiveFg, s.a.fonts.UI, px+gtx.Dp(1))
	s.regions = append(s.regions, sregion{r: r, kind: "dismiss", file: row.file, match: row.match, tip: "Dismiss (remove from results)"})
}

func (s *SearchView) handlePointer(gtx layout.Context) {
	a := s.a
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &s.tag, Kinds: pointer.Press | pointer.Move | pointer.Leave | pointer.Enter | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1 << 30, Max: 1 << 30}})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Scroll:
			s.scroll += int(e.Scroll.Y)
		case pointer.Leave:
			s.hovering = false
		case pointer.Move, pointer.Enter:
			s.hovering = true
			s.hover = e.Position.Round()
			for i := len(s.regions) - 1; i >= 0; i-- {
				if r := s.regions[i]; s.hover.In(r.r) {
					if r.tip != "" && a.status != r.tip {
						a.setStatus(r.tip, 3*time.Second)
					}
					break
				}
			}
		case pointer.Press:
			s.hover = e.Position.Round()
			// The most specific (last drawn) region wins.
			for i := len(s.regions) - 1; i >= 0; i-- {
				r := s.regions[i]
				if s.hover.In(r.r) {
					s.click(r, e)
					break
				}
			}
		}
	}
}

func (s *SearchView) click(r sregion, e pointer.Event) {
	a := s.a
	switch {
	case strings.HasPrefix(r.kind, "toggle:"):
		k := strings.TrimPrefix(r.kind, "toggle:")
		s.opts[k] = !s.opts[k]
		if k == "regex" && s.opts[k] {
			s.opts["structural"] = false
		}
		if k == "structural" && s.opts[k] {
			s.opts["regex"] = false
		}
		if k != "preserve" {
			s.start()
		}
	case r.kind == "details":
		s.details = !s.details
	case r.kind == "replace_all":
		s.replace(nil, -1, true)
	case r.kind == "undo":
		s.undoReplace()
	case r.kind == "check_file":
		f := s.files[r.file]
		all := true
		for _, m := range f.matches {
			if m.excluded {
				all = false
			}
		}
		for i := range f.matches {
			f.matches[i].excluded = all
		}
	case r.kind == "check":
		m := &s.files[r.file].matches[r.match]
		m.excluded = !m.excluded
	case r.kind == "replace_one":
		s.replace([]int{r.file}, r.match, false)
	case r.kind == "dismiss":
		f := s.files[r.file]
		if r.match < 0 || len(f.matches) == 1 {
			s.files = append(s.files[:r.file], s.files[r.file+1:]...)
		} else {
			f.matches = append(f.matches[:r.match], f.matches[r.match+1:]...)
		}
		s.total = 0
		for _, f := range s.files {
			s.total += len(f.matches)
		}
		s.rowsDirty = true
	case r.kind == "file":
		if e.Modifiers.Contain(key.ModAlt) {
			// ⌥-click collapses or expands every file.
			c := !s.files[r.file].collapsed
			for _, f := range s.files {
				f.collapsed = c
			}
		} else {
			s.files[r.file].collapsed = !s.files[r.file].collapsed
		}
		s.rowsDirty = true
	case r.kind == "match":
		s.open(r.file, r.match)
	}
	a.win.Invalidate()
}
