// Package remote connects the inks command to the running app: a Unix
// socket in the data folder that takes one JSON request per connection and
// answers "ok" once it is handled (then "done" when a --wait request's files
// are closed).
package remote

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"inks/internal/settings"
)

// File is a path to open, with an optional position (inks -g file:line:col).
type File struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Col  int    `json:"col,omitempty"`
}

// Request is one run of the inks command.
type Request struct {
	Cwd       string  `json:"cwd"`
	Files     []File  `json:"files,omitempty"`
	NewWindow bool    `json:"newWindow,omitempty"`
	Add       bool    `json:"add,omitempty"`
	Wait      bool    `json:"wait,omitempty"`
	Stdin     *string `json:"stdin,omitempty"`
}

// SocketPath is the socket of the inks using this data folder.
func SocketPath() string { return filepath.Join(settings.DataDir(), "inks.sock") }

// Listen serves requests in the background. handle runs once per request
// and returns nil, or (for --wait) a function that returns when the opened
// files are closed. It fails if another inks already answers on the socket.
func Listen(handle func(Request) (wait func())) error {
	p := SocketPath()
	if c, err := net.DialTimeout("unix", p, 300*time.Millisecond); err == nil {
		c.Close()
		return errors.New("another inks is listening on " + p)
	}
	_ = os.Remove(p) // left behind by an inks that didn't quit cleanly
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	l, err := net.Listen("unix", p)
	if err != nil {
		return err
	}
	_ = os.Chmod(p, 0o600)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go serve(c, handle)
		}
	}()
	return nil
}

func serve(c net.Conn, handle func(Request) func()) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	var r Request
	if err := json.NewDecoder(c).Decode(&r); err != nil {
		fmt.Fprintf(c, "error: %v\n", err)
		return
	}
	wait := handle(r)
	fmt.Fprintln(c, "ok")
	if wait != nil {
		wait()
		fmt.Fprintln(c, "done")
	}
}

// Send hands r to the running inks. It returns once the request is handled,
// or with r.Wait, once its files are closed (or the app quits).
func Send(r Request) error {
	c, err := net.DialTimeout("unix", SocketPath(), time.Second)
	if err != nil {
		return ErrNotRunning
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(r); err != nil {
		return err
	}
	sc := bufio.NewScanner(c)
	ok := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "ok":
			ok = true
			if !r.Wait {
				return nil
			}
		case line == "done":
			return nil
		case strings.HasPrefix(line, "error: "):
			return errors.New(strings.TrimPrefix(line, "error: "))
		}
	}
	if ok {
		return nil // waiting, and the app quit
	}
	return errors.New("inks closed the connection")
}

// ErrNotRunning means no inks answered on the socket.
var ErrNotRunning = errors.New("inks is not running")
