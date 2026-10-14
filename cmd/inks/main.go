// inks is a fast, keyboard-driven text editor.
package main

import (
	"fmt"
	"log"
	"os"
	"sync"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/unit"

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
	var (
		mu      sync.Mutex
		inst    *ui.App
		pending []string
	)
	go func() {
		w := new(app.Window)
		w.Option(app.Title("inks"), app.Size(unit.Dp(1280), unit.Dp(820)), app.MinSize(unit.Dp(400), unit.Dp(260)))
		a := ui.New(w, args)
		if os.Getenv("INKS_SELFTEST") != "" {
			dir, _ := os.MkdirTemp("", "inks-selftest-")
			a.Test = ui.SelfTest(dir)
		}
		mu.Lock()
		inst = a
		if len(pending) > 0 {
			a.OpenLater(pending...)
		}
		mu.Unlock()
		if err := a.Run(); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	// Files opened from Finder or dropped on the Dock icon arrive as URLs.
	app.Events(func(e event.Event) bool {
		if u, ok := e.(app.URLEvent); ok && u.URL != nil && u.URL.Scheme == "file" {
			mu.Lock()
			if inst != nil {
				inst.OpenLater(u.URL.Path)
			} else {
				pending = append(pending, u.URL.Path)
			}
			mu.Unlock()
		}
		return true
	})
}
