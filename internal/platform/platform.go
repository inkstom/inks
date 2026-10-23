// Package platform wraps macOS services: native dialogs (via AppleScript),
// the Trash, Finder and opening URLs.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func osa(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// OpenFiles shows the system "choose file" dialog (multiple selection).
func OpenFiles(prompt, dir string) []string {
	script := fmt.Sprintf(`set fs to choose file with prompt %s with multiple selections allowed`, quote(prompt))
	if dir != "" {
		script += " default location (POSIX file " + quote(dir) + ")"
	}
	script += "\nset out to \"\"\nrepeat with f in fs\nset out to out & POSIX path of f & linefeed\nend repeat\nreturn out"
	out, err := osa(script)
	if err != nil {
		return nil
	}
	return splitLines(out)
}

// OpenFolder shows the "choose folder" dialog.
func OpenFolder(prompt, dir string) string {
	script := fmt.Sprintf(`POSIX path of (choose folder with prompt %s`, quote(prompt))
	if dir != "" {
		script += " default location (POSIX file " + quote(dir) + ")"
	}
	script += ")"
	out, err := osa(script)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(out, "/")
}

// SaveFile shows the "choose file name" dialog.
func SaveFile(prompt, defaultPath string) string {
	script := fmt.Sprintf(`POSIX path of (choose file name with prompt %s`, quote(prompt))
	if defaultPath != "" {
		script += " default name " + quote(filepath.Base(defaultPath)) + " default location (POSIX file " + quote(filepath.Dir(defaultPath)) + ")"
	}
	script += ")"
	out, err := osa(script)
	if err != nil {
		return ""
	}
	return out
}

// Ask shows a dialog and returns the chosen button's index (cancelIndex
// if dismissed). Buttons are listed left to right as macOS shows them.
func Ask(message, detail string, buttons []string, defaultIndex, cancelIndex int) int {
	var qb []string
	for _, b := range buttons {
		qb = append(qb, quote(b))
	}
	text := message
	if detail != "" {
		text += "\n\n" + detail
	}
	script := fmt.Sprintf(`button returned of (display dialog %s buttons {%s} default button %s cancel button %s with icon caution)`,
		quote(text), strings.Join(qb, ", "), quote(buttons[defaultIndex]), quote(buttons[cancelIndex]))
	out, err := osa(script)
	if err != nil {
		return cancelIndex // Escape / Cancel raises an error in AppleScript
	}
	for i, b := range buttons {
		if b == out {
			return i
		}
	}
	return cancelIndex
}

// Message shows an informational alert.
func Message(message, detail string) {
	_, _ = osa(fmt.Sprintf(`display alert %s message %s`, quote(message), quote(detail)))
}

// Trash moves a file or folder to the Trash.
func Trash(path string) error {
	_, err := osa(fmt.Sprintf(`tell application "Finder" to delete (POSIX file %s as alias)`, quote(path)))
	return err
}

// Reveal shows a file in Finder.
func Reveal(path string) error { return exec.Command("open", "-R", path).Run() }

// Open opens a file or URL with its default application.
func Open(target string) error { return exec.Command("open", target).Run() }

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// HasITerm reports whether iTerm2 is installed.
func HasITerm() bool {
	for _, p := range []string{"/Applications/iTerm.app", filepath.Join(os.Getenv("HOME"), "Applications/iTerm.app")} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// OpenTerminal opens a terminal tab in dir and, if command isn't empty,
// runs it there. app is "iTerm" or "Terminal" ("" picks iTerm2 when it is
// installed). The tab opens in the front window, or a new window.
func OpenTerminal(dir, command, app string) error {
	line := "cd " + ShellQuote(dir)
	if command != "" {
		line += " && " + command
	}
	if app == "" {
		app = "Terminal"
		if HasITerm() {
			app = "iTerm"
		}
	}
	var script string
	if app == "iTerm" {
		script = `tell application id "com.googlecode.iterm2"
	activate
	if (count of windows) = 0 then
		create window with default profile
	else
		tell current window to create tab with default profile
	end if
	tell current session of current window to write text ` + quote(line) + `
end tell`
	} else {
		script = `tell application "Terminal"
	activate
	do script ` + quote(line) + `
end tell`
	}
	_, err := osa(script)
	return err
}
