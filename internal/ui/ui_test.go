package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/app"

	"gioui.org/io/key"

	"inks/internal/buffer"
	"inks/internal/editor"
	"inks/internal/macmenu"
)

func TestTrimPreview(t *testing.T) {
	line := "    resource \"aws_s3_bucket\" \"logs\" {"
	p, from, to := trimPreview(line, 14, 27)
	if p[from:to] != "aws_s3_bucket" || p[0] != 'r' {
		t.Fatalf("got %q [%d:%d]", p, from, to)
	}
	long := "x := aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa needle tail"
	i := len(long) - len("needle tail")
	p, from, to = trimPreview(long, i, i+6)
	if p[from:to] != "needle" || p[:len("…")] != "…" {
		t.Fatalf("long: %q [%d:%d]", p, from, to)
	}
}

func TestDiffEdit(t *testing.T) {
	for _, c := range []struct{ cur, next string }{
		{"hello world", "hello brave world"},
		{"a\nb\nc", "a\nc"},
		{"héllo", "hállo"},
		{"same", "same"},
		{"", "new"},
	} {
		b := buffer.New(c.cur)
		e, ok := diffEdit(b, c.cur, c.next)
		if !ok {
			if c.cur != c.next {
				t.Fatalf("%q→%q: no edit", c.cur, c.next)
			}
			continue
		}
		b.Apply([]buffer.Edit{e}, nil, buffer.ApplyOptions{})
		if got := b.Text("\n"); got != c.next {
			t.Fatalf("%q→%q: got %q", c.cur, c.next, got)
		}
	}
}

func TestMenuKey(t *testing.T) {
	for _, c := range []struct {
		in   string
		key  string
		mods int
	}{
		{"super+shift+f", "f", macmenu.Command | macmenu.Shift},
		{"f4", string(rune(0xF707)), 0},
		{"shift+f4", string(rune(0xF707)), macmenu.Shift},
		{"super+equals", "=", macmenu.Command},
		{"super+backspace", "\b", macmenu.Command},
		{"escape", "", 0},  // would steal Escape from the editor
		{"shift+a", "", 0}, // plain typing
		{"super+k", "k", macmenu.Command},
	} {
		k, m := menuKey(c.in)
		if k != c.key || m != c.mods {
			t.Errorf("%s: got %q %x, want %q %x", c.in, k, m, c.key, c.mods)
		}
	}
}

func TestWrapLine(t *testing.T) {
	line := "the quick brown fox jumps over the lazy dog"
	br := wrapLine(line, 10, 4)
	prev := 0
	for _, b := range append(br, len(line)) {
		seg := line[prev:b]
		if len(seg) > 10 {
			t.Fatalf("segment too wide: %q (%v)", seg, br)
		}
		prev = b
	}
	if len(br) == 0 || line[br[0]-1] != ' ' {
		t.Fatalf("should break after spaces: %v", br)
	}
	// No spaces: hard breaks.
	if br := wrapLine("abcdefghijklmnopqrstuvwxyz", 10, 4); len(br) != 2 || br[0] != 10 {
		t.Fatalf("hard breaks: %v", br)
	}
}

func TestRowLayout(t *testing.T) {
	d := &Doc{}
	d.M = editorModel("short\nthe quick brown fox jumps over the lazy dog\nend")
	rl := d.layoutRows(20, 4, true)
	if rl.total() != 5 {
		t.Fatalf("rows: %d (%v)", rl.total(), rl.segs)
	}
	if l, k := rl.lineOfRow(3); l != 1 || k != 2 {
		t.Fatalf("lineOfRow(3) = %d,%d", l, k)
	}
	if l, _ := rl.lineOfRow(4); l != 2 {
		t.Fatalf("lineOfRow(4) = %d", l)
	}
	row, from := rl.rowOf(buffer.Pos{Line: 1, Col: 25})
	if row != 2 || from == 0 {
		t.Fatalf("rowOf: %d %d", row, from)
	}
	// Down from line 0 lands on the first row of line 1, then its second row.
	p := rl.moveRows(d.M.Buf, buffer.Pos{Line: 0, Col: 2}, 2, 1, 4)
	p = rl.moveRows(d.M.Buf, p, 2, 1, 4)
	if r, _ := rl.rowOf(p); p.Line != 1 || r != 2 {
		t.Fatalf("moveRows: %+v row %d", p, r)
	}
	// Without wrap, rows are lines.
	if rl := d.layoutRows(0, 4, false); rl.total() != 3 {
		t.Fatalf("no-wrap rows: %d", rl.total())
	}
}

func editorModel(text string) *editor.Model { return editor.NewModel(buffer.New(text)) }

func TestIMEEdit(t *testing.T) {
	a := &App{}
	d := &Doc{M: editorModel("cafe\nx")}
	m := d.M
	m.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 0, Col: 3})}, 0)
	// Dead key: the accent is inserted as marked text, then replaced.
	head := imeCoord(m.Buf, m.MainSel().Head)
	a.imeEdit(d, key.EditEvent{Range: key.Range{Start: head, End: head}, Text: "´"})
	if got := m.Buf.Line(0); got != "caf´e" {
		t.Fatalf("marked: %q", got)
	}
	d.composing = true
	h2 := imeCoord(m.Buf, m.MainSel().Head)
	a.imeEdit(d, key.EditEvent{Range: key.Range{Start: h2 - 1, End: h2}, Text: "é"})
	d.composing = false
	if got := m.Buf.Line(0); got != "café"+"e" {
		t.Fatalf("composed: %q", got)
	}
	// Plain typing with a stale range still inserts at the caret.
	a.imeEdit(d, key.EditEvent{Range: key.Range{Start: 2, End: 2}, Text: "!"})
	if got := m.Buf.Line(0); got != "café!e" {
		t.Fatalf("plain: %q", got)
	}
	if p := imePos(m.Buf, imeCoord(m.Buf, buffer.Pos{Line: 0, Col: 5})); p.Col != 5 {
		t.Fatalf("coord round trip: %+v", p)
	}
}

func TestSnippetInsert(t *testing.T) {
	a := &App{win: new(app.Window)}
	a.complete = newCompleter(a)
	d := &Doc{M: editorModel("    fn\n    fn"), Lang: "Go"}
	d.M.TabSize = 4
	d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Line: 0, Col: 6}), buffer.Cursor(buffer.Pos{Line: 1, Col: 6})}, 1)
	a.insertSnippet(d, "func ${1:name}() {\n\t$0\n}", "fn")
	want := "    func name() {\n    \t\n    }"
	if got := d.M.Buf.Text("\n"); got != want+"\n"+want {
		t.Fatalf("snippet text:\n%s", got)
	}
	for i, r := range d.M.Sels {
		if d.M.Buf.Slice(r.Start(), r.End()) != "name" {
			t.Fatalf("caret %d selects %q", i, d.M.Buf.Slice(r.Start(), r.End()))
		}
	}
	sn, ok := parseSnippet([]byte(`<snippet><content><![CDATA[for ${1:i} := 0; $1 < ${2:n}; $1++ {
	$0
}]]></content><tabTrigger>fori</tabTrigger><scope>source.go</scope></snippet>`))
	if !ok || sn.trigger != "fori" || !scopeMatches(sn.scope, "Go") || scopeMatches(sn.scope, "Python") {
		t.Fatalf("parse snippet: %+v", sn)
	}
}

func TestSessionMigration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("INKS_USER_DATA", dir)
	t.Setenv("INKS_NO_SESSION", "")
	_ = os.MkdirAll(filepath.Join(dir, "Local"), 0o755)
	old := `{"version": 2, "docs": [{"path": "/tmp/a.txt", "lang": "Go", "line": 3}], "folders": ["/tmp"], "recent": ["/tmp/a.txt"]}`
	if err := os.WriteFile(sessionPath(), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadSession()
	if s == nil || len(s.Windows) != 1 || len(s.Windows[0].Docs) != 1 || s.Windows[0].Folders[0] != "/tmp" || s.Recent[0] != "/tmp/a.txt" {
		t.Fatalf("migrated session: %+v", s)
	}
	sh := &Shared{recent: []string{"x"}, recentFolders: []string{"/f"}}
	sh.saveSession() // no windows: writes an empty window list
	if prev, err := os.ReadFile(filepath.Join(dir, "Local", "Session.previous.json")); err != nil || string(prev) != old {
		t.Fatalf("previous session not kept: %v %q", err, prev)
	}
	sh.quitting = true
	sh.recentFolders = []string{"/changed"}
	sh.saveSession() // ignored while quitting
	s = loadSession()
	if s == nil || s.Version != 3 || len(s.Windows) != 0 || s.RecentFolders[0] != "/f" {
		// "/f", not "/changed": saving stops once quitting.
		t.Fatalf("v3 session: %+v", s)
	}
}

func TestDefaultBindingsUnique(t *testing.T) {
	seen := map[string]string{}
	for _, b := range DefaultBindings {
		var keys []string
		for _, k := range b.Keys {
			keys = append(keys, normalizeKey(k))
		}
		k := strings.Join(keys, " ")
		if prev, ok := seen[k]; ok {
			t.Errorf("%s is bound to both %s and %s", k, prev, b.Command)
		}
		seen[k] = b.Command
	}
}

func TestClassicHelpers(t *testing.T) {
	text := "f(a, [b, c]) { x }"
	if m, ok := matchBracket(text, 1); !ok || text[m] != ')' {
		t.Fatalf("matchBracket: %d %v", m, ok)
	}
	if s, e, ok := enclosing(text, 7, 7); !ok || text[s] != '[' || text[e] != ']' {
		t.Fatalf("enclosing: %d %d %v", s, e, ok)
	}
	if got := reindent("if x {\n\t\ty()\n\t}", "    "); got != "if x {\n    \ty()\n    }" {
		t.Fatalf("reindent: %q", got)
	}
	d := &Doc{M: editorModel("// one two three four five six seven eight nine ten")}
	d.M.TabSize = 4
	d.wrapParagraphs(20)
	for i := 0; i < d.M.Buf.LineCount(); i++ {
		l := d.M.Buf.Line(i)
		if len(l) > 20 || !strings.HasPrefix(l, "// ") {
			t.Fatalf("wrap line %d: %q", i, l)
		}
	}
	d = &Doc{M: editorModel("abc")}
	d.M.SetSels([]buffer.Range{buffer.Cursor(buffer.Pos{Col: 1})}, 0)
	d.transpose()
	if got := d.M.Buf.Line(0); got != "bac" {
		t.Fatalf("transpose: %q", got)
	}
}

func TestFileHelpers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(p, []byte("hi"), 0o644)
	if got := freeName(p); filepath.Base(got) != "a copy.txt" {
		t.Fatalf("freeName: %s", got)
	}
	_ = os.WriteFile(filepath.Join(dir, "a copy.txt"), nil, 0o644)
	if got := freeName(p); filepath.Base(got) != "a copy 2.txt" {
		t.Fatalf("freeName 2: %s", got)
	}
	sub := filepath.Join(dir, "sub", "deep")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "x.go"), []byte("package x"), 0o644)
	if err := copyPath(filepath.Join(dir, "sub"), filepath.Join(dir, "sub2")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sub2", "deep", "x.go")); err != nil || string(b) != "package x" {
		t.Fatalf("copyPath: %v %q", err, b)
	}
	m := trimSeparators([]menuEntry{menuSep, {label: "A"}, menuSep, menuSep, {label: "B"}, menuSep})
	if len(m) != 3 || m[0].label != "A" || m[1].label != "" || m[2].label != "B" {
		t.Fatalf("trimSeparators: %+v", m)
	}
}

func TestLinkCLI(t *testing.T) {
	dir := t.TempDir()
	if err := linkCLI(dir, "/Applications/inks.app/Contents/Resources/bin/inks"); err != nil {
		t.Fatal(err)
	}
	// Replacing an earlier link is fine.
	if err := linkCLI(dir, "/Other/inks.app/Contents/Resources/bin/inks"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(dir, "inks")); got != "/Other/inks.app/Contents/Resources/bin/inks" {
		t.Errorf("link points at %q", got)
	}
	// A real file named inks is left alone.
	other := t.TempDir()
	_ = os.WriteFile(filepath.Join(other, "inks"), []byte("#!/bin/sh\n"), 0o755)
	if err := linkCLI(other, "/x"); err == nil {
		t.Error("replaced a file that isn't a link")
	}
}
