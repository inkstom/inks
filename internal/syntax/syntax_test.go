package syntax

import "testing"

func TestDetectAndHighlight(t *testing.T) {
	cases := map[string]string{"main.tf": "Terraform", "a.go": "Go", "x.py": "Python", "notes.txt": PlainText}
	for file, want := range cases {
		if got := Detect(file, ""); got != want {
			t.Errorf("%s: got %q want %q", file, got, want)
		}
	}
	if got := Detect("", "#!/usr/bin/env python3"); got != "Python" {
		t.Errorf("shebang: %q", got)
	}
	spans := Highlight("Go", "package main\n// hi\nfunc f() { return 42 }")
	roles := map[string]bool{}
	for _, line := range spans {
		for _, s := range line {
			roles[s.Role] = true
		}
	}
	for _, want := range []string{"storage", "comment", "number"} {
		if !roles[want] {
			t.Errorf("missing role %q in %v", want, roles)
		}
	}
	tf := Highlight("Terraform", "resource \"aws_s3_bucket\" \"x\" {\n  bucket = \"b\" # note\n}")
	if len(tf) != 3 || len(tf[1]) == 0 {
		t.Fatalf("terraform spans: %+v", tf)
	}
}
