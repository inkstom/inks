package editor

import (
	"strings"
	"testing"

	"inks/internal/buffer"
)

func model(text string) *Model {
	m := NewModel(buffer.New(text))
	m.UseSpaces = true
	m.TabSize = 2
	m.LineComment = "#"
	return m
}

func at(line, col int) Range { return buffer.Cursor(Pos{Line: line, Col: col}) }

func text(m *Model) string { return m.Buf.Text("\n") }

func TestMultiCursorTyping(t *testing.T) {
	m := model("foo\nbar\nbaz")
	m.SetSels([]Range{at(0, 3), at(1, 3), at(2, 3)}, 2)
	m.Insert("!")
	if text(m) != "foo!\nbar!\nbaz!" {
		t.Fatalf("got %q", text(m))
	}
	for i, s := range m.Sels {
		if s.Head != (Pos{Line: i, Col: 4}) {
			t.Fatalf("caret %d at %+v", i, s.Head)
		}
	}
	m.Backspace(CharLeft)
	if text(m) != "foo\nbar\nbaz" {
		t.Fatalf("backspace: %q", text(m))
	}
}

func TestNewlineIndents(t *testing.T) {
	m := model(`resource "x" "y" {}`)
	m.SetSels([]Range{at(0, 18)}, 0)
	m.Newline()
	if text(m) != "resource \"x\" \"y\" {\n  \n}" {
		t.Fatalf("got %q", text(m))
	}
	if m.Sels[0].Head != (Pos{Line: 1, Col: 2}) {
		t.Fatalf("caret %+v", m.Sels[0].Head)
	}
}

func TestAutoPairAndOvertype(t *testing.T) {
	m := model("")
	m.Insert("(")
	m.Insert("a")
	m.Insert(")")
	if text(m) != "(a)" || m.Sels[0].Head.Col != 3 {
		t.Fatalf("got %q caret %+v", text(m), m.Sels[0].Head)
	}
	m2 := model("")
	m2.Insert("[")
	m2.Backspace(CharLeft)
	if text(m2) != "" {
		t.Fatalf("pair delete: %q", text(m2))
	}
}

func TestLineCommands(t *testing.T) {
	m := model("one\ntwo\nthree")
	m.SetSels([]Range{at(0, 1)}, 0)
	m.DuplicateLine()
	if text(m) != "one\none\ntwo\nthree" || m.Sels[0].Head != (Pos{Line: 1, Col: 1}) {
		t.Fatalf("duplicate: %q %+v", text(m), m.Sels[0].Head)
	}
	m.DeleteLine()
	if text(m) != "one\ntwo\nthree" {
		t.Fatalf("delete line: %q", text(m))
	}
	m.SetSels([]Range{at(0, 0)}, 0)
	m.MoveLines(1)
	if text(m) != "two\none\nthree" || m.Sels[0].Head.Line != 1 {
		t.Fatalf("move down: %q %+v", text(m), m.Sels[0])
	}
	m.SetSels([]Range{at(0, 0)}, 0)
	m.JoinLines()
	if text(m) != "two one\nthree" {
		t.Fatalf("join: %q", text(m))
	}
}

func TestToggleComment(t *testing.T) {
	m := model("  a = 1\n  b = 2")
	m.SetSels([]Range{{Anchor: Pos{Line: 0, Col: 0}, Head: Pos{Line: 1, Col: 3}}}, 0)
	m.ToggleComment()
	if text(m) != "  # a = 1\n  # b = 2" {
		t.Fatalf("comment: %q", text(m))
	}
	m.ToggleComment()
	if text(m) != "  a = 1\n  b = 2" {
		t.Fatalf("uncomment: %q", text(m))
	}
}

func TestWordMovement(t *testing.T) {
	m := model("fooBar baz_qux")
	m.Move(WordRight, false, 0)
	if m.Sels[0].Head.Col != 6 {
		t.Fatalf("word right: %+v", m.Sels[0].Head)
	}
	m.Sels = []Range{at(0, 0)}
	m.Move(SubwordRight, false, 0)
	if m.Sels[0].Head.Col != 3 {
		t.Fatalf("subword right: %+v", m.Sels[0].Head)
	}
	m.Sels = []Range{at(0, 0)}
	m.Move(LineEnd, true, 0)
	if m.Buf.Slice(m.Sels[0].Start(), m.Sels[0].End()) != "fooBar baz_qux" {
		t.Fatal("select to line end")
	}
}

func TestVerticalMovementKeepsGoal(t *testing.T) {
	m := model("long line here\nx\nanother long line")
	m.SetSels([]Range{at(0, 10)}, 0)
	m.Move(LineDown, false, 0)
	m.Move(LineDown, false, 0)
	if m.Sels[0].Head != (Pos{Line: 2, Col: 10}) {
		t.Fatalf("goal column lost: %+v", m.Sels[0].Head)
	}
}

func TestUndoRestoresSelections(t *testing.T) {
	m := model("abc")
	m.SetSels([]Range{at(0, 1), at(0, 3)}, 1)
	m.Insert("X")
	sels, _ := m.Buf.Undo()
	if text(m) != "abc" || len(sels) != 2 {
		t.Fatalf("undo: %q %d", text(m), len(sels))
	}
}

func TestTransformAndReplaceLines(t *testing.T) {
	m := model("hello world")
	m.SetSels([]Range{at(0, 2)}, 0)
	m.TransformSelections(strings.ToUpper)
	if text(m) != "HELLO world" {
		t.Fatalf("word transform: %q", text(m))
	}
	m2 := model("b\na\nc")
	_ = m2.ReplaceLinesOrAll(func(s string) (string, error) { return "a\nb\nc", nil })
	if text(m2) != "a\nb\nc" {
		t.Fatalf("replace all: %q", text(m2))
	}
}
