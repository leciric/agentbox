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
//	FAKEWSL_DAEMON_EXIT the daemon the front end starts exits at once
//	FAKEWSL_NO_MOUNT the distro can't see Windows' files (no automount)
//	FAKEWSL_LANG     pt-BR: wsl.exe's messages in Portuguese
//	FAKEWSL_STRICT_TAR an imported .tar without /etc/os-release is refused, as newer WSL does
//	FAKEWSL_IMPORT_FAIL --import fails with an error that isn't about virtualization
//	FAKEWSL_HANG     never answer a wsl.exe command line starting with this
//	FAKEWSL_EXEC_TIMEOUT a command run in the distro fails as WSL's own
//	                 timeout does under heavy CPU load (Wsl/Service/0x8007274c)
//	                 argument, as a stuck WSL doesn't
//
// `agentbox wsl-bridge` and the daemon the front end starts run in-process, so
// the relay can be run end to end with no Linux: `fakewsl serve <socket>` is
// that daemon, answering GET /v1/version, echoing a websocket at /v1/echo, and
// stopping at POST /v1/shutdown.
package main

import (
	"archive/tar"
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

// running marks a distro that runs, as WSL's are once a command has run in
// them and until --terminate: a new one is stopped.
func running(name string) string { return filepath.Join(root(), "running", strings.ToLower(name)) }

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
	if h := os.Getenv("FAKEWSL_HANG"); h != "" && args[0] == h {
		time.Sleep(time.Hour)
	}
	switch args[0] {
	case "--version":
		say("WSL version: 2.4.13.0\r\nKernel version: 6.6.87.2-1\r\n")
		return 0
	case "--list":
		entries, _ := os.ReadDir(filepath.Join(root(), "distros"))
		if len(entries) == 0 {
			// As wsl.exe says it, to stdout, in the language of Windows
			// (FAKEWSL_LANG=pt-BR), above its error code.
			if os.Getenv("FAKEWSL_LANG") == "pt-BR" {
				say("O Subsistema do Windows para Linux não tem distribuições instaladas.\r\nVocê pode resolver isso instalando uma distribuição com as instruções abaixo:\r\n\r\nUse 'wsl.exe --list --online' para listar as distribuições disponíveis\r\ne 'wsl.exe --install <Distro>' para instalar.\r\nCódigo de erro: Wsl/WSL_E_DEFAULT_DISTRO_NOT_FOUND\r\n")
			} else {
				say("Windows Subsystem for Linux has no installed distributions.\r\nYou can resolve this by installing a distribution with the instructions below:\r\n\r\nUse 'wsl.exe --list --online' to list available distributions\r\nand 'wsl.exe --install <Distro>' to install.\r\nError code: Wsl/WSL_E_DEFAULT_DISTRO_NOT_FOUND\r\n")
			}
			return 1
		}
		say("  NAME        STATE           VERSION\r\n")
		for _, e := range entries {
			v, _ := os.ReadFile(filepath.Join(root(), "distros", e.Name()))
			state := "Stopped"
			if _, err := os.Stat(running(e.Name())); err == nil {
				state = "Running"
			}
			say("  %-11s %-15s %s\r\n", e.Name(), state, strings.TrimSpace(string(v)))
		}
		return 0
	case "--import":
		if os.Getenv("FAKEWSL_NO_VMP") != "" {
			say("The operation could not be started because a required feature is not installed.\r\nError code: Wsl/Service/RegisterDistro/CreateVm/HCS/HCS_E_SERVICE_NOT_AVAILABLE\r\n")
			return 1
		}
		if len(args) < 4 {
			return fail("fakewsl: --import Name Dir Tarball")
		}
		if _, err := os.Stat(args[3]); err != nil {
			return fail("fakewsl: %v", err)
		}
		if os.Getenv("FAKEWSL_IMPORT_FAIL") != "" {
			say("Disk full.\r\nError code: Wsl/Service/RegisterDistro/ERROR_DISK_FULL\r\n")
			return 1
		}
		if os.Getenv("FAKEWSL_STRICT_TAR") != "" && strings.HasSuffix(args[3], ".tar") && !validTar(args[3]) {
			say("O arquivo importado não é uma distribuição válida do Linux.\r\nError code: Wsl/Service/RegisterDistro/WSL_E_IMPORT_FAILED\r\n")
			return 1
		}
		_ = os.MkdirAll(filepath.Join(root(), "distros"), 0o755)
		_ = os.WriteFile(marker(args[1]), []byte("2"), 0o644)
		return 0
	case "--terminate":
		if len(args) < 2 {
			return fail("fakewsl: --terminate Name")
		}
		_ = os.Remove(running(args[1]))
		return 0
	case "--unregister":
		_ = os.Remove(running(args[1]))
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
	_ = os.MkdirAll(filepath.Dir(running(name)), 0o755)
	_ = os.WriteFile(running(name), nil, 0o644)
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
	case os.Getenv("FAKEWSL_EXEC_TIMEOUT") != "" && command[0] == "sh" && len(command) == 3 && command[2] == "cat /proc/meminfo; nproc":
		say("A connection attempt failed because the connected party did not properly respond after a period of time.\r\nError code: Wsl/Service/0x8007274c\r\n")
		return 1
	case command[0] == "sh" && len(command) == 3 && command[2] == "cat /proc/meminfo; nproc":
		// Distro.Power: 16 GiB, 9.5 of them available, and 8 CPUs.
		fmt.Print("MemTotal:       16777216 kB\nMemFree:         8388608 kB\nMemAvailable:    9961472 kB\n8\n")
		return 0
	case command[0] == "sh" && len(command) == 7 && strings.Contains(command[2], "flock 9"):
		// Distro.EnsureBinary: compare, and copy from where the distro sees
		// Windows' copy, unless FAKEWSL_NO_MOUNT says it can't.
		want, dst, src := command[4], command[5], command[6]
		if b, err := os.ReadFile(dst); err == nil && sha(b) == want {
			fmt.Println("current")
			return 0
		}
		b, err := os.ReadFile(src)
		if os.Getenv("FAKEWSL_NO_MOUNT") != "" || src == "" || err != nil || sha(b) != want {
			fmt.Println("stdin")
			return 0
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst+".new", b, 0o755); err != nil {
			return fail("%v", err)
		}
		if err := os.Rename(dst+".new", dst); err != nil {
			return fail("%v", err)
		}
		fmt.Println("installed")
		return 0
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
	case command[0] == "sh" && len(command) >= 4 && strings.Contains(command[2], `exec env "$@" agentbox daemon`):
		// Distro.StartDaemon: the daemon, until wsl.exe is killed. The
		// variables it was given are kept in daemon-env, for the test to read.
		if err := os.WriteFile(filepath.Join(root(), "daemon-env"), []byte(strings.Join(command[4:], "\n")), 0o644); err != nil {
			return fail("%v", err)
		}
		// FAKEWSL_DAEMON_EXIT: a daemon that exits as it starts.
		if os.Getenv("FAKEWSL_DAEMON_EXIT") != "" {
			return 1
		}
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
		if len(command) == 3 && command[1] == "daemon" && command[2] == "stop" {
			return shutdown()
		}
		if bin := os.Getenv("FAKEWSL_AGENTBOX"); bin != "" {
			return execLocal(append([]string{bin}, command[1:]...))
		}
		fmt.Printf("agentbox %s\n", strings.Join(command[1:], " "))
		return 0
	}
	return execLocal(command)
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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

// shutdown is `agentbox daemon stop`: POST /v1/shutdown to the daemon.
func shutdown() int {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv("FAKEWSL_SOCKET"))
		},
	}}
	resp, err := client.Post("http://agentbox/v1/shutdown", "", nil)
	if err != nil {
		return fail("no daemon is answering: %v", err)
	}
	_ = resp.Body.Close()
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

// validTar is whether the archive has an etc/os-release, which is all the
// fake asks of a distro.
func validTar(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err != nil {
			return false
		}
		if strings.TrimPrefix(h.Name, "./") == "etc/os-release" {
			return true
		}
	}
}
