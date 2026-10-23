package main

import (
	"testing"

	"inks/internal/remote"
)

func TestSplitPosition(t *testing.T) {
	for in, want := range map[string]remote.File{
		"a.go":         {Path: "a.go"},
		"a.go:12":      {Path: "a.go", Line: 12},
		"a.go:12:3":    {Path: "a.go", Line: 12, Col: 3},
		"x:y.go:7":     {Path: "x:y.go", Line: 7},
		"a.go:":        {Path: "a.go:"},
		"/tmp/a:1:2:3": {Path: "/tmp/a:1", Line: 2, Col: 3},
	} {
		if got := splitPosition(in); got != want {
			t.Errorf("splitPosition(%q) = %+v, want %+v", in, got, want)
		}
	}
}
