package theme

import "testing"

func TestParseColorScheme(t *testing.T) {
	s, err := ParseColorScheme("x", []byte(`{
		// comment
		"name": "Test Dark",
		"variables": {"bg": "#202020", "red": "hsl(0, 100%, 50%)", "dim": "color(var(red) alpha(0.5))"},
		"globals": {"background": "var(bg)", "foreground": "rgb(220, 220, 220)", "selection": "var(dim)"},
		"rules": [
			{"scope": "comment", "foreground": "#808080", "font_style": "italic"},
			{"scope": "string", "foreground": "var(red)"},
			{"scope": "string.quoted", "foreground": "#00ff00"},
			{"scope": "keyword, storage.type", "foreground": "#0000ff"},
			{"scope": "source.js keyword.control", "foreground": "#ff00ff"}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Test Dark" || !s.Dark || s.Fg != Hex("#dcdcdc") {
		t.Fatalf("globals: %+v", s)
	}
	if s.Token("string") != Hex("#00ff00") {
		t.Errorf("more specific rule should win: %v", s.Token("string"))
	}
	if s.Token("keyword") != Hex("#0000ff") || s.Token("storage") != Hex("#0000ff") {
		t.Errorf("keyword/storage: %v %v", s.Token("keyword"), s.Token("storage"))
	}
	if !s.ItalicComments || s.Token("comment") != Hex("#808080") {
		t.Errorf("comment: %v italic %v", s.Token("comment"), s.ItalicComments)
	}
	if s.Selection.R < 0x80 || s.Selection.A != 255 {
		t.Errorf("translucent selection not flattened: %v", s.Selection)
	}
}

func TestParseTmTheme(t *testing.T) {
	s, err := ParseTmTheme("x", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>name</key><string>Plisty</string>
<key>settings</key><array>
 <dict><key>settings</key><dict><key>background</key><string>#FAFAFA</string><key>foreground</key><string>#111111</string></dict></dict>
 <dict><key>scope</key><string>string</string><key>settings</key><dict><key>foreground</key><string>#AA0000</string></dict></dict>
</array></dict></plist>`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Plisty" || s.Dark || s.Token("string") != Hex("#aa0000") {
		t.Fatalf("tmTheme: %+v", s)
	}
	RegisterExternal(s)
	defer ClearExternal()
	if Get("Packages/Foo/Plisty.tmTheme").Name != "Plisty" {
		t.Fatal("lookup by package path")
	}
}
