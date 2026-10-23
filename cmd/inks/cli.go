package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"inks/internal/remote"
)

const cliUsage = `inks — open files and folders in inks

Usage: inks [options] [file or folder ...]

With no paths, opens the current folder in a window (or switches to the
window that already shows it). Folders open in a window of their own;
files open in the window whose folder holds them, else the active one.
A file that doesn't exist yet opens as a new, unsaved file.

  -n, --new-window  open in a new window
  -a, --add         add the folders to the active window
  -g, --goto        paths may end in :line or :line:column
  -w, --wait        return only once the files are closed
                    (for git: git config --global core.editor "inks -w")
  -                 read the text to edit from standard input
  -h, --help        show this help
  -v, --version     print the version
`

// runCLI is the inks command (bin/inks runs "inks --cli …"). It hands the
// paths to the running inks, starting it first if needed.
func runCLI(args []string) int {
	r := remote.Request{}
	r.Cwd, _ = os.Getwd()
	goTo, stdin, noFlags := false, false, false
	var paths []string
	for _, a := range args {
		if noFlags || a == "-" || !strings.HasPrefix(a, "-") {
			if a == "-" && !noFlags {
				stdin = true
			} else {
				paths = append(paths, a)
			}
			continue
		}
		switch a {
		case "--":
			noFlags = true
		case "-n", "--new-window":
			r.NewWindow = true
		case "-a", "--add":
			r.Add = true
		case "-g", "--goto":
			goTo = true
		case "-w", "--wait":
			r.Wait = true
		case "-h", "--help":
			fmt.Print(cliUsage)
			return 0
		case "-v", "--version":
			fmt.Println("inks " + version)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "inks: unknown option %s\n\n%s", a, cliUsage)
			return 2
		}
	}
	for _, p := range paths {
		f := remote.File{Path: p}
		if goTo {
			f = splitPosition(p)
		}
		if !filepath.IsAbs(f.Path) {
			f.Path = filepath.Join(r.Cwd, f.Path)
		}
		f.Path = filepath.Clean(f.Path)
		if _, err := os.Stat(f.Path); err != nil {
			// A new file is fine, as long as its folder exists.
			if st, err := os.Stat(filepath.Dir(f.Path)); err != nil || !st.IsDir() {
				fmt.Fprintf(os.Stderr, "inks: %s: no such folder\n", filepath.Dir(f.Path))
				return 1
			}
		}
		r.Files = append(r.Files, f)
	}
	if stdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 256<<20))
		if err != nil {
			fmt.Fprintln(os.Stderr, "inks: reading standard input:", err)
			return 1
		}
		s := string(b)
		r.Stdin = &s
	}
	err := remote.Send(r)
	if errors.Is(err, remote.ErrNotRunning) {
		if err = launchApp(); err == nil {
			// Wait for the app to start listening.
			deadline := time.Now().Add(15 * time.Second)
			for {
				time.Sleep(150 * time.Millisecond)
				if err = remote.Send(r); !errors.Is(err, remote.ErrNotRunning) || time.Now().After(deadline) {
					break
				}
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "inks:", err)
		return 1
	}
	return 0
}

// splitPosition splits "file:12:3" into the path, line and column.
func splitPosition(p string) remote.File {
	f := remote.File{Path: p}
	var nums []int
	for len(nums) < 2 {
		i := strings.LastIndexByte(f.Path, ':')
		if i <= 0 {
			break
		}
		n, err := strconv.Atoi(f.Path[i+1:])
		if err != nil || n < 0 {
			break
		}
		nums = append([]int{n}, nums...)
		f.Path = f.Path[:i]
	}
	if len(nums) > 0 {
		f.Line = nums[0]
	}
	if len(nums) > 1 {
		f.Col = nums[1]
	}
	return f
}

// launchApp starts inks: through Launch Services when this binary is in an
// app bundle, otherwise (a development build) as a detached process.
func launchApp() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		return exec.Command("open", exe[:i+len(".app")]).Run()
	}
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
