package buffer

import "testing"

func TestEditsAndUndo(t *testing.T) {
	b := New("hello world\nsecond line\nthird")
	sels := []Range{Cursor(Pos{0, 5}), Cursor(Pos{1, 6})}
	// Type at two cursors.
	edits := []Edit{{Start: sels[0].Head, End: sels[0].Head, Text: "!"}, {Start: sels[1].Head, End: sels[1].Head, Text: "!"}}
	sels = b.Apply(edits, sels, ApplyOptions{Kind: "type", Merge: true})
	if got := b.Text("\n"); got != "hello! world\nsecond! line\nthird" {
		t.Fatalf("multi-cursor insert: %q", got)
	}
	if sels[0].Head != (Pos{0, 6}) || sels[1].Head != (Pos{1, 7}) {
		t.Fatalf("carets not moved: %+v", sels)
	}
	// Multi-line replace.
	b.Apply([]Edit{{Start: Pos{0, 0}, End: Pos{1, 7}, Text: "A\nB"}}, sels, ApplyOptions{})
	if got := b.Text("\n"); got != "A\nB line\nthird" {
		t.Fatalf("multi-line replace: %q", got)
	}
	if _, ok := b.Undo(); !ok || b.Text("\n") != "hello! world\nsecond! line\nthird" {
		t.Fatalf("undo 1: %q", b.Text("\n"))
	}
	restored, _ := b.Undo()
	if b.Text("\n") != "hello world\nsecond line\nthird" || restored[1].Head != (Pos{1, 6}) {
		t.Fatalf("undo 2: %q %+v", b.Text("\n"), restored)
	}
	b.Redo()
	b.Redo()
	if b.Text("\n") != "A\nB line\nthird" {
		t.Fatalf("redo: %q", b.Text("\n"))
	}
}

func TestTypingMerges(t *testing.T) {
	b := New("")
	sels := []Range{Cursor(Pos{})}
	for _, c := range "abc" {
		h := sels[0].Head
		sels = b.Apply([]Edit{{Start: h, End: h, Text: string(c)}}, sels, ApplyOptions{Kind: "type", Merge: true})
	}
	b.Undo()
	if b.Text("\n") != "" {
		t.Fatalf("typing should undo as one step, got %q", b.Text("\n"))
	}
}

func TestOffsetsAndUnicode(t *testing.T) {
	b := New("héllo\nwörld")
	if p := b.Clamp(Pos{0, 2}); p != (Pos{0, 1}) {
		t.Fatalf("clamp inside rune: %+v", p)
	}
	if off := b.Offset(Pos{1, 0}); off != len("héllo")+1 {
		t.Fatalf("offset: %d", off)
	}
	if p := b.PosAt(len("héllo") + 2); p != (Pos{1, 1}) {
		t.Fatalf("posAt: %+v", p)
	}
	if s := b.Slice(Pos{0, 1}, Pos{1, 1}); s != "éllo\nw" {
		t.Fatalf("slice: %q", s)
	}
}
