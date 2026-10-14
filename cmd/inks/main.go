// inks is a fast, keyboard-driven text editor.
package main

import (
	"fmt"
	"os"

	"gioui.org/app"
	"gioui.org/io/event"

	"inks/internal/ui"
)

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

const usage = `inks — a fast, keyboard-driven text editor

Usage: inks [file or folder ...]

Files open in tabs and folders in the side bar. To open them in the
running inks, use the bin/inks launcher or "open -a inks <paths>".

  -h, --help     show this help
  -v, --version  print the version
`

func main() {
	args := os.Args[1:]
	for _, a := range args {
		switch a {
		case "-v", "--version":
			fmt.Println("inks " + version)
			return
		case "-h", "--help":
			fmt.Print(usage)
			return
		}
	}
	s := ui.NewShared()
	if os.Getenv("INKS_SELFTEST") != "" {
		dir, _ := os.MkdirTemp("", "inks-selftest-")
		s.Test = ui.SelfTest(dir)
	}
	go s.Start(args)
	// Files opened from Finder or dropped on the Dock icon arrive as URLs.
	app.Events(func(e event.Event) bool {
		if u, ok := e.(app.URLEvent); ok && u.URL != nil && u.URL.Scheme == "file" {
			s.OpenLater(u.URL.Path)
		}
		return true
	})
}
