package icons

import "testing"

func TestFor(t *testing.T) {
	if len(Faces()) != 5 {
		t.Fatalf("faces: %d", len(Faces()))
	}
	cases := []struct {
		name     string
		dir      bool
		typeface string
	}{
		{"main.tf", false, "inks-file-icons"},
		{"main.go", false, "inks-file-icons"},
		{"Dockerfile", false, "inks-file-icons"},
		{"README.md", false, ""},
		{"node_modules", true, ""},
		{"src", true, "inks-octicons"},
		{"unknown.zzzz", false, "inks-octicons"},
	}
	for _, c := range cases {
		ic, ok := For(c.name, c.dir, false, false)
		if !ok || ic.Rune == 0 {
			t.Errorf("%s: no icon", c.name)
			continue
		}
		if c.typeface != "" && string(ic.Font.Typeface) != c.typeface {
			t.Errorf("%s: font %s, want %s", c.name, ic.Font.Typeface, c.typeface)
		}
	}
	// Different file types get different icons; light colors differ.
	tf, _ := For("main.tf", false, false, false)
	gof, _ := For("main.go", false, false, false)
	if tf.Rune == gof.Rune {
		t.Errorf("terraform and go share an icon")
	}
	// The generic folder icon has a darker variant for light schemes.
	fd, _ := For("src", true, false, false)
	fl, _ := For("src", true, false, true)
	if fd.Color == fl.Color {
		t.Errorf("light folder color matches dark: %v", fd.Color)
	}
	// Longest extension wins.
	if a, _ := For("ansible.yml", false, false, false); a.Rune == 0 {
		t.Errorf("ansible.yml")
	}
}
