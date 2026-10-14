package linediff

import (
	"strings"
	"testing"
)

func apply(a, b []string, ops []Op) []string {
	var out []string
	for _, op := range ops {
		switch op.Kind {
		case '=':
			out = append(out, a[op.A])
		case '+':
			out = append(out, b[op.B])
		}
	}
	return out
}

func TestDiffReconstructs(t *testing.T) {
	cases := [][2]string{
		{"a\nb\nc", "a\nb\nc"},
		{"a\nb\nc", "a\nx\nc"},
		{"a\nb\nc", "a\nc"},
		{"a\nb\nc", "x\na\nb\nc\ny"},
		{"", "a\nb"},
		{"a\nb", ""},
		{"1\n2\n3\n4\n5\n6", "1\n3\n4\nx\n5\n6\n7"},
	}
	for _, c := range cases {
		a, b := strings.Split(c[0], "\n"), strings.Split(c[1], "\n")
		got := apply(a, b, Diff(a, b, 0))
		if strings.Join(got, "\n") != c[1] {
			t.Errorf("%q → %q: got %q", c[0], c[1], got)
		}
	}
}

func TestMarks(t *testing.T) {
	a := strings.Split("one\ntwo\nthree\nfour", "\n")
	b := strings.Split("one\nTWO\nthree\nnew\nfour", "\n")
	m := Marks(a, b)
	want := []Mark{None, Modified, None, Added, None}
	for i := range want {
		if m[i] != want[i] {
			t.Fatalf("marks %v, want %v", m, want)
		}
	}
	m = Marks(a, strings.Split("one\nthree\nfour", "\n"))
	if m[1] != Deleted || m[0] != None {
		t.Fatalf("deletion marks %v", m)
	}
}
