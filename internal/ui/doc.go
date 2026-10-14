package ui

import (
	"image"

	"encoding/json"
	"gioui.org/io/key"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"inks/internal/buffer"
	"inks/internal/editor"
	"inks/internal/linediff"
	"inks/internal/settings"
	"inks/internal/syntax"
)

// Doc is one open file (a tab).
type Doc struct {
	M              *editor.Model
	Path           string
	Name           string // for untitled buffers with a custom name
	Lang           string
	CRLF           bool
	Encoding       string
	Scratch        bool
	saved          string
	dirtyVer       int
	dirty          bool
	modTime        time.Time
	hl             *syntax.Highlighter
	hlVer          int
	scrollY        float32
	scrollX        float32
	reveal         bool // scroll the main caret into view on next frame
	centerOnReveal bool // …placing it a third down the view when it was off screen
	dragging       bool
	dragFrom       buffer.Pos
	lastClick      time.Time
	clicks         int
	blink          time.Time
	asking         bool // a "changed on disk" prompt is open
	preview        bool // transient tab from a side bar click

	ID        int
	pts       *pointIndex
	regions   map[string]*regionSet
	phantoms  map[string][]phantom
	status    map[string]string
	overrides map[string]any
	revealAt  *buffer.Pos // reveal this position instead of the caret

	folds     []fold
	foldVer   int
	bookmarks []buffer.Pos
	foldMarks []foldMark

	rows         *rowLayout
	wrapOverride *bool
	mini         *minimapCache

	gitBase     []string
	gitBaseAt   time.Time
	gitTracked  bool
	gitLoading  bool
	gitDiffing  bool
	gitMarks    []linediff.Mark
	gitMarksVer int

	// Input method state.
	caretPx      image.Point
	caretAscent  int
	caretDescent int
	imeState     string
	composing    bool
	compose      key.Range
}

func newDoc(text, path string, s *settings.Settings, notify func()) *Doc {
	d := &Doc{Path: path, Encoding: "UTF-8", ID: nextDocID()}
	if strings.Contains(text, "\r\n") {
		d.CRLF = true
	}
	if strings.HasPrefix(text, "\ufeff") {
		d.Encoding = "UTF-8 with BOM"
		text = strings.TrimPrefix(text, "\ufeff")
	}
	b := buffer.New(text)
	d.M = editor.NewModel(b)
	d.saved = b.Text("\n")
	d.dirtyVer = b.Version()
	first := b.Line(0)
	d.Lang = syntax.Detect(path, first)
	d.applySettings(s)
	if s.Bool("detect_indentation") {
		d.detectIndentation()
	}
	d.hl = syntax.NewHighlighter(notify)
	d.trackEdits()
	d.blink = time.Now()
	return d
}

func (d *Doc) applySettings(s *settings.Settings) {
	m := d.M
	m.TabSize = s.Int("tab_size")
	if m.TabSize <= 0 {
		m.TabSize = 4
	}
	m.UseSpaces = s.Bool("translate_tabs_to_spaces")
	m.AutoIndent = s.Bool("auto_indent")
	m.AutoPair = s.Bool("auto_match_enabled")
	m.LineComment, m.BlockComment = syntax.Comments(d.Lang)
}

// detectIndentation guesses tabs vs spaces and the indent width.
func (d *Doc) detectIndentation() {
	tabs, spaces := 0, 0
	widths := map[int]int{}
	prev := 0
	n := d.M.Buf.LineCount()
	if n > 2000 {
		n = 2000
	}
	for i := 0; i < n; i++ {
		l := d.M.Buf.Line(i)
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] == '\t' {
			tabs++
			continue
		}
		w := len(l) - len(strings.TrimLeft(l, " "))
		if w > 0 {
			spaces++
		}
		if diff := w - prev; diff >= 2 && diff <= 8 {
			widths[diff]++
		} else if diff <= -2 && diff >= -8 {
			widths[-diff]++
		}
		prev = w
	}
	if tabs == 0 && spaces == 0 {
		return
	}
	if tabs > spaces {
		d.M.UseSpaces = false
		return
	}
	d.M.UseSpaces = true
	best, bestN := 0, 0
	for w, c := range widths {
		if c > bestN || (c == bestN && w < best) {
			best, bestN = w, c
		}
	}
	if best > 0 {
		d.M.TabSize = best
	}
}

// Title is the tab label.
func (d *Doc) Title() string {
	if d.Path != "" {
		return filepath.Base(d.Path)
	}
	if d.Name != "" {
		return d.Name
	}
	if first := strings.TrimSpace(d.M.Buf.Line(0)); first != "" {
		if utf8.RuneCountInString(first) > 40 {
			first = string([]rune(first)[:40])
		}
		return first
	}
	return "untitled"
}

// Dirty reports unsaved changes (comparing text only when it changed).
func (d *Doc) Dirty() bool {
	if d.Scratch {
		return false
	}
	if v := d.M.Buf.Version(); v != d.dirtyVer {
		d.dirtyVer = v
		d.dirty = d.M.Buf.Text("\n") != d.saved
	}
	return d.dirty
}

func (d *Doc) markSaved() {
	d.saved = d.M.Buf.Text("\n")
	d.dirtyVer = d.M.Buf.Version()
	d.dirty = false
}

// Save writes the document to its path.
func (d *Doc) Save(s *settings.Settings) error {
	if d.Path == "" {
		return os.ErrInvalid
	}
	m := d.M
	if s.Bool("trim_trailing_white_space_on_save") {
		m.ReplaceLinesOrAll(func(t string) (string, error) {
			lines := strings.Split(t, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " \t")
			}
			return strings.Join(lines, "\n"), nil
		})
	}
	text := m.Buf.Text("\n")
	if s.Bool("ensure_newline_at_eof_on_save") && text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
		end := m.Buf.EndPos()
		m.Buf.Apply([]buffer.Edit{{Start: end, End: end, Text: "\n"}}, m.Sels, buffer.ApplyOptions{Kind: "save"})
	}
	out := text
	if d.CRLF {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if d.Encoding == "UTF-8 with BOM" {
		out = "\ufeff" + out
	}
	if err := os.WriteFile(d.Path, []byte(out), 0o644); err != nil {
		return err
	}
	d.markSaved()
	if st, err := os.Stat(d.Path); err == nil {
		d.modTime = st.ModTime()
	}
	return nil
}

// Highlight schedules re-highlighting if the text changed.
func (d *Doc) Highlight() {
	if v := d.M.Buf.Version(); v != d.hlVer {
		d.hlVer = v
		d.hl.Update(d.Lang, d.M.Buf.Text("\n"), v)
	}
}

func (d *Doc) setLang(lang string, s *settings.Settings) {
	d.Lang = lang
	d.M.LineComment, d.M.BlockComment = syntax.Comments(lang)
	d.hlVer = -1
}

// readFile loads a text file; binary files are refused.
func readFile(path string) (string, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, err
	}
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for _, c := range data[:n] {
		if c == 0 {
			return "", time.Time{}, errBinary
		}
	}
	st, _ := os.Stat(path)
	var mt time.Time
	if st != nil {
		mt = st.ModTime()
	}
	text := string(data)
	if !utf8.ValidString(text) {
		// Treat as Latin-1.
		r := make([]rune, len(data))
		for i, c := range data {
			r[i] = rune(c)
		}
		text = string(r)
	}
	return text, mt, nil
}

type binaryErr struct{}

func (binaryErr) Error() string { return "binary file" }

var errBinary = binaryErr{}

func remarshal(in any, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// normalizeDiskText converts file contents to buffer form.
func normalizeDiskText(text string) (string, bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	crlf := strings.Contains(text, "\r\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n"), crlf
}

// diffEdit returns the single edit that turns cur into next (common prefix
// and suffix kept), so selections and undo survive a reload.
func diffEdit(b *buffer.Buffer, cur, next string) (buffer.Edit, bool) {
	if cur == next {
		return buffer.Edit{}, false
	}
	p := 0
	for p < len(cur) && p < len(next) && cur[p] == next[p] {
		p++
	}
	for p > 0 && p < len(cur) && !utf8.RuneStart(cur[p]) {
		p--
	}
	s := 0
	for s < len(cur)-p && s < len(next)-p && cur[len(cur)-1-s] == next[len(next)-1-s] {
		s++
	}
	for s > 0 && !utf8.RuneStart(cur[len(cur)-s]) {
		s--
	}
	return buffer.Edit{Start: b.PosAt(p), End: b.PosAt(len(cur) - s), Text: next[p : len(next)-s]}, true
}
