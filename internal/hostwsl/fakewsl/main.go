// Command fakewsl stands in for wsl.exe, so the Windows front end
// (package hostwsl) can be tested on Linux, and on a Windows machine without
// WSL, like CI's. It understands the wsl.exe commands the front end runs, and
// runs a distro's commands here, with a directory standing in for its disk:
//
//	FAKEWSL_ROOT     the fake's state: which distros exist, and their files
//	FAKEWSL_SOCKET   the daemon's socket, which `agentbox wsl-bridge` connects to
//	FAKEWSL_AGENTBOX a Linux agentbox for the rest of `agentbox`'s commands
//	FAKEWSL_UTF16    print wsl.exe's own output in UTF-16LE, as wsl.exe does
//	                 without WSL_UTF8
//
// `agentbox wsl-bridge` and the daemon the front end starts run in-process, so
// the relay can be run end to end with no Linux: `fakewsl serve <socket>` is
// that daemon, answering GET /v1/version, echoing a websocket at /v1/echo, and
// stopping at POST /v1/shutdown.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"agentbox/internal/hostwsl"
)

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "serve" {
		if err := serve(context.Background(), args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "fakewsl serve:", err)
			os.Exit(1)
		}
		return
	}
	os.Exit(run(args))
}

func root() string {
	if r := os.Getenv("FAKEWSL_ROOT"); r != "" {
		return r
	}
	return filepath.Join(os.TempDir(), "fakewsl")
}

func marker(name string) string { return filepath.Join(root(), "distros", strings.ToLower(name)) }

func say(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	if os.Getenv("FAKEWSL_UTF16") != "" {
		u := utf16.Encode([]rune(s))
		b := make([]byte, 2*len(u))
		for i, c := range u {
			b[2*i], b[2*i+1] = byte(c), byte(c>>8)
		}
		_, _ = os.Stdout.Write(b)
		return
	}
	fmt.Print(s)
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	return 1
}

func run(args []string) int {
	if len(args) == 0 {
		return fail("fakewsl: no arguments")
	}
	switch args[0] {
	case "--version":
		say("WSL version: 2.4.13.0\r\nKernel version: 6.6.87.2-1\r\n")
		return 0
	case "--list":
		entries, _ := os.ReadDir(filepath.Join(root(), "distros"))
		if len(entries) == 0 {
			return fail("Windows Subsystem for Linux has no installed distributions.")
		}
		say("  NAME        STATE           VERSION\r\n")
		for _, e := range entries {
			v, _ := os.ReadFile(filepath.Join(root(), "distros", e.Name()))
			say("  %-11s Running         %s\r\n", e.Name(), strings.TrimSpace(string(v)))
		}
		return 0
	case "--import":
		if len(args) < 4 {
			return fail("fakewsl: --import Name Dir Tarball")
		}
		if _, err := os.Stat(args[3]); err != nil {
			return fail("fakewsl: %v", err)
		}
		_ = os.MkdirAll(filepath.Join(root(), "distros"), 0o755)
		_ = os.WriteFile(marker(args[1]), []byte("2"), 0o644)
		return 0
	case "--terminate":
		return 0
	case "--unregister":
		if err := os.Remove(marker(args[1])); err != nil {
			return fail("There is no distribution with the supplied name.")
		}
		return 0
	case "--distribution":
		return distro(args[1], args[2:])
	}
	return fail("fakewsl: can't fake %q", strings.Join(args, " "))
}

func distro(name string, args []string) int {
	if _, err := os.Stat(marker(name)); err != nil {
		return fail("There is no distribution with the supplied name.")
	}
	dir := ""
	for len(args) > 0 && args[0] != "--exec" {
		switch args[0] {
		case "--user":
			args = args[2:]
		case "--cd":
			dir, args = args[1], args[2:]
		default:
			return fail("fakewsl: can't fake %q", args[0])
		}
	}
	if len(args) < 2 {
		return fail("fakewsl: an interactive shell can't be faked")
	}
	command := args[1:]
	for i, a := range command {
		command[i] = path(a)
	}
	if dir != "" && dir != "~" {
		if err := os.Chdir(path(dir)); err != nil {
			return fail("fakewsl: --cd %s: %v", dir, err)
		}
	}
	return execute(command)
}

// path is where a distro path is here: the distro's own files are under
// FAKEWSL_ROOT, and a Windows drive's are where they are.
func path(p string) string {
	if p == "/usr/local/bin/agentbox" {
		return filepath.Join(root(), "usr", "local", "bin", "agentbox")
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(p, "/mnt/") && len(p) >= 6 {
		return strings.ToUpper(p[5:6]) + ":" + filepath.FromSlash(orSlash(p[6:]))
	}
	return p
}

func orSlash(s string) string {
	if s == "" {
		return "/"
	}
	return s
}

// execute runs a command in the fake distro. The front end runs a handful,
// and each is done here, so the fake needs nothing but itself on Windows.
func execute(command []string) int {
	installed := filepath.Join(root(), "usr", "local", "bin", "agentbox")
	if command[0] == "env" {
		command = command[1:]
		for len(command) > 0 && strings.Contains(command[0], "=") {
			k, v, _ := strings.Cut(command[0], "=")
			_ = os.Setenv(k, v)
			command = command[1:]
		}
	}
	switch {
	case command[0] == "sha256sum":
		b, err := os.ReadFile(command[1])
		if err != nil {
			return fail("sha256sum: %s: No such file or directory", command[1])
		}
		sum := sha256.Sum256(b)
		fmt.Printf("%s  %s\n", hex.EncodeToString(sum[:]), command[1])
		return 0
	case command[0] == "sh" && len(command) == 6 && command[1] == "-c" && strings.Contains(command[2], `cat >"$t"`):
		// Distro.writeFile: stdin into a file, in one rename.
		_ = os.MkdirAll(filepath.Dir(command[4]), 0o755)
		b, _ := io.ReadAll(os.Stdin)
		if err := os.WriteFile(command[4]+".new", b, 0o755); err != nil {
			return fail("%v", err)
		}
		if err := os.Rename(command[4]+".new", command[4]); err != nil {
			return fail("%v", err)
		}
		return 0
	case command[0] == "sh" && len(command) > 5 && strings.HasPrefix(command[2], `cd "$1"`):
		// Distro.Forward, in a working directory.
		if err := os.Chdir(path(command[4])); err != nil {
			fmt.Fprintf(os.Stderr, "note: %s isn't in the distro, so this runs in your Linux home folder\n", command[4])
		}
		return execute(command[5:])
	case command[0] == "sh" && len(command) == 3 && strings.Contains(command[2], "exec agentbox daemon"):
		// Distro.StartDaemon: the daemon, until wsl.exe is killed.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := serve(ctx, os.Getenv("FAKEWSL_SOCKET")); err != nil {
			return fail("%v", err)
		}
		return 0
	case command[0] == installed || command[0] == "agentbox":
		if _, err := os.Stat(installed); err != nil {
			return fail("sh: 1: agentbox: not found")
		}
		if len(command) > 1 && command[1] == "wsl-bridge" {
			return bridge(command[2:])
		}
		if bin := os.Getenv("FAKEWSL_AGENTBOX"); bin != "" {
			return execLocal(append([]string{bin}, command[1:]...))
		}
		fmt.Printf("agentbox %s\n", strings.Join(command[1:], " "))
		return 0
	}
	return execLocal(command)
}

func execLocal(command []string) int {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return fail("%v", err)
	}
	return 0
}

// bridge is `agentbox wsl-bridge`, as internal/cli has it.
func bridge(args []string) int {
	socket := os.Getenv("FAKEWSL_SOCKET")
	if len(args) == 2 && args[0] == "--wait" {
		wait, _ := time.ParseDuration(args[1])
		deadline := time.Now().Add(wait)
		for {
			if c, err := net.Dial("unix", socket); err == nil {
				_ = c.Close()
				return 0
			}
			if time.Now().After(deadline) {
				return fail("the daemon isn't answering on %s", socket)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := hostwsl.Bridge(stdio{os.Stdin, os.Stdout}, socket); err != nil {
		return fail("%v", err)
	}
	return 0
}

type stdio struct {
	io.Reader
	io.WriteCloser
}

// serve is a daemon to reach: GET /v1/version, and a websocket at /v1/echo
// that echoes the frames it gets, which is enough to see HTTP and a
// long-lived upgraded connection through the relay.
func serve(ctx context.Context, socket string) error {
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(socket) }()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"version":"fake"}`)
	})
	mux.HandleFunc("POST /v1/echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(bytes.ToUpper(b))
	})
	mux.HandleFunc("GET /v1/echo", echo)
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		go func() { _ = srv.Close() }()
	})
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
