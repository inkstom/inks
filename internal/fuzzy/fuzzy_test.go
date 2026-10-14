package fuzzy

import "testing"

func TestMatch(t *testing.T) {
	if _, ok := Match("wkspc", "workspace.js"); !ok {
		t.Fatal("should match")
	}
	if _, ok := Match("xyz", "workspace.js"); ok {
		t.Fatal("should not match")
	}
	a, _ := MatchPath("main", "src/main.go")
	b, _ := MatchPath("main", "internal/domain/maintenance.go")
	if a.Score <= b.Score {
		t.Fatalf("file-name match should rank first: %v vs %v", a.Score, b.Score)
	}
	r, _ := Match("tsb", "View: Toggle Side Bar")
	if len(r.Indexes) != 3 {
		t.Fatal("indexes")
	}
}
