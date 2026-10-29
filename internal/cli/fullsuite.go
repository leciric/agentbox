package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Whole test suites. An agent runs the tests its change touches and leaves
// the whole suite to CI: a full `go test ./...` or a bare `npm test` takes the
// VM's memory from every other agent at once, and agents ran them anyway,
// because a repository's own instructions ("go test ./... before you finish")
// outweighed the user's memory saying not to. So, with the installation's
// "Block full test suites" on (state.SettingBlockFullSuite) and the project
// not allowing them (Project.FullTestSuite), heavy-hook refuses one before it
// runs and says what to run instead, from the files the agent changed. A
// command naming AGENTBOX_FULL_SUITE=1 runs anyway: that is how an agent
// whose task or a message asks for the full suite in so many words runs it
// (the brief says when). Only Claude Code has the hook; Codex and OpenCode
// have the brief alone.

// fullSuiteOverride is the assignment that runs a whole suite anyway, as a
// command's prefix or exported before it.
var fullSuiteOverride = regexp.MustCompile(`(?:^|[\s;&|(])AGENTBOX_FULL_SUITE=["']?(?:1|true|yes)\b`)

// suiteRun is one simple command that runs a whole test suite.
type suiteRun struct {
	Runner  string // go, vitest, jest, node, pytest, cargo, turbo, nx, lerna, pnpm, playwright, make, gradle, mvn, bazel
	Command string // its words, as the agent wrote them
	Dir     string // the directory it runs in: absolute, or "" when the hook didn't say
	Cwd     string // the Bash tool's working directory, which a suggestion starts from
}

// fullSuite finds the first simple command of a Bash command that runs a
// whole test suite, in cwd (the Bash tool's working directory, "" if
// unknown). Here-documents' bodies are data, and `bash -c "…"` is looked
// into.
func fullSuite(command, cwd string) (suiteRun, bool) {
	if fullSuiteOverride.MatchString(command) {
		return suiteRun{}, false
	}
	dir := cwd
	for _, words := range simpleCommands(withoutHeredocs(command), 0) {
		words = withoutPrefixes(words)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "cd", "pushd":
			if len(words) > 1 && words[1] != "-" && dir != "" {
				dir = within(dir, words[1])
			}
			continue
		}
		if run, ok := wholeSuite(words, dir); ok {
			run.Command, run.Cwd = strings.Join(words, " "), cwd
			return run, true
		}
	}
	return suiteRun{}, false
}

func within(dir, p string) string {
	if p == "" || strings.HasPrefix(p, "~") || strings.Contains(p, "$") {
		return dir
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, p)
}

// simpleCommands splits a command line into its simple commands' words:
// at ;, &, |, newlines, ( and ), $( and backticks, outside quotes. Quotes
// are taken off, comments and redirections left out, and the command a
// shell's -c runs is split in turn.
func simpleCommands(line string, depth int) [][]string {
	var cmds [][]string
	var words []string
	var word strings.Builder
	inWord, skipNext := false, false
	endWord := func() {
		if inWord {
			if skipNext {
				skipNext = false
			} else {
				words = append(words, word.String())
			}
		}
		word.Reset()
		inWord = false
	}
	endCmd := func() {
		endWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
		}
		words = nil
		skipNext = false
	}
	r := []rune(line)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r):
			i++
			if r[i] != '\n' {
				word.WriteRune(r[i])
				inWord = true
			}
		case c == '\'':
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			word.WriteString(string(r[i+1 : min(j, len(r))]))
			inWord, i = true, j
		case c == '"':
			j := i + 1
			for j < len(r) && r[j] != '"' {
				if r[j] == '\\' && j+1 < len(r) {
					j++
				}
				j++
			}
			word.WriteString(strings.ReplaceAll(string(r[i+1:min(j, len(r))]), `\"`, `"`))
			inWord, i = true, j
		case c == '#' && !inWord:
			for i < len(r) && r[i] != '\n' {
				i++
			}
			endCmd()
		case c == '>' || c == '<':
			// A redirection: the fd before it isn't a word, nor is its
			// target, nor &1 after it.
			if inWord && strings.Trim(word.String(), "0123456789") == "" {
				word.Reset()
				inWord = false
			}
			endWord()
			for i+1 < len(r) && (r[i+1] == '>' || r[i+1] == '<' || r[i+1] == '|') {
				i++
			}
			if i+1 < len(r) && r[i+1] == '&' {
				i++
				for i+1 < len(r) && (r[i+1] >= '0' && r[i+1] <= '9' || r[i+1] == '-') {
					i++
				}
				continue
			}
			skipNext = true
		case c == '&' && i+1 < len(r) && r[i+1] == '>':
			endWord()
			i++
			for i+1 < len(r) && r[i+1] == '>' {
				i++
			}
			skipNext = true
		case c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')' || c == '`' || c == '{' && !inWord || c == '}' && !inWord:
			if c == '(' && inWord && word.String() == "$" {
				word.Reset()
				inWord = false
			}
			endCmd()
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	endCmd()
	if depth > 2 {
		return cmds
	}
	// What a shell's -c runs is commands too.
	var out [][]string
	for _, words := range cmds {
		out = append(out, words)
		ws := withoutPrefixes(words)
		if len(ws) == 0 || !slices.Contains([]string{"bash", "sh", "zsh", "dash"}, path.Base(ws[0])) {
			continue
		}
		for j := 1; j+1 < len(ws); j++ {
			if a := ws[j]; strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
				out = append(out, simpleCommands(ws[j+1], depth+1)...)
				break
			}
		}
	}
	return out
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// withoutPrefixes is a simple command's words from the command it runs on:
// past VAR=x, sudo, time, timeout 60, env, npx and `agentbox heavy --`, and
// with the command's path taken off (./node_modules/.bin/vitest is vitest).
func withoutPrefixes(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case assignment.MatchString(w):
			words = words[1:]
		case slices.Contains([]string{"sudo", "time", "nice", "nohup", "exec", "command", "env", "then", "do", "else", "!"}, w):
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		case w == "timeout":
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
			if len(words) > 0 {
				words = words[1:]
			}
		case slices.Contains([]string{"npx", "pnpx", "bunx"}, path.Base(w)):
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		case path.Base(w) == "agentbox" && len(words) > 1 && words[1] == "heavy":
			words = words[2:]
			if len(words) > 0 && words[0] == "--" {
				words = words[1:]
			}
		default:
			if strings.Contains(w, "/") {
				words = append([]string{path.Base(w)}, words[1:]...)
			}
			return words
		}
	}
	return words
}

// flags is what a runner's arguments say: the positional ones (files,
// packages, test names) and the flags, each with its value if it takes one.
type flags struct {
	pos   []string
	flags map[string]string
}

// parseFlags reads args, where valued are the flags that take the next word
// as their value. Everything after -- is positional.
func parseFlags(args []string, valued ...string) flags {
	f := flags{flags: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			f.pos = append(f.pos, args[i+1:]...)
			return f
		case strings.HasPrefix(a, "-") && len(a) > 1:
			name, value, hasValue := strings.Cut(a, "=")
			if !hasValue && slices.Contains(valued, name) && i+1 < len(args) {
				i++
				value = args[i]
			}
			f.flags[name] = value
		default:
			f.pos = append(f.pos, a)
		}
	}
	return f
}

func (f flags) has(names ...string) bool {
	for _, n := range names {
		if _, ok := f.flags[n]; ok {
			return true
		}
	}
	return false
}

// wholeSuite says whether one simple command, its prefixes taken off, runs
// a whole test suite in dir.
func wholeSuite(words []string, dir string) (suiteRun, bool) {
	name, args := words[0], words[1:]
	switch name {
	case "go":
		f := parseFlags(args, "-C")
		if c, ok := f.flags["-C"]; ok {
			dir = within(dir, c)
		}
		if len(f.pos) == 0 || f.pos[0] != "test" {
			return suiteRun{}, false
		}
		i := slices.Index(args, "test")
		t := parseFlags(args[i+1:], "-run", "-bench", "-count", "-timeout", "-p", "-parallel", "-coverprofile", "-covermode",
			"-coverpkg", "-tags", "-o", "-cpu", "-exec", "-skip", "-shuffle", "-ldflags", "-gcflags", "-mod", "-fuzz", "-fuzztime")
		if t.has("-run", "-bench", "-fuzz") {
			return suiteRun{}, false
		}
		for _, p := range t.pos {
			if p == "all" || (p == "./..." || p == "...") && (dir == "" || fileExists(filepath.Join(dir, "go.mod"))) {
				return suiteRun{Runner: "go", Dir: dir}, true
			}
		}
	case "npm", "pnpm", "yarn", "bun":
		return packageScript(name, args, dir)
	case "vitest", "jest":
		return jsRunner(name, args, dir)
	case "pytest", "py.test":
		return pytest(args, dir)
	case "python", "python3":
		if len(args) > 1 && args[0] == "-m" && args[1] == "pytest" {
			return pytest(args[2:], dir)
		}
	case "uv", "poetry", "pipenv", "hatch", "pdm":
		if len(args) > 1 && args[0] == "run" {
			return wholeSuite(withoutPrefixes(args[1:]), dir)
		}
	case "cargo":
		if len(args) > 0 && strings.HasPrefix(args[0], "+") {
			args = args[1:]
		}
		if len(args) > 1 && args[0] == "nextest" && args[1] == "run" {
			args = args[1:]
		} else if len(args) == 0 || args[0] != "test" {
			return suiteRun{}, false
		}
		// Before --, a positional argument names the tests to run.
		before := args[1:]
		if i := slices.Index(before, "--"); i >= 0 {
			before = before[:i]
		}
		f := parseFlags(before, "-p", "--package", "--manifest-path", "--test", "--bin", "--example", "--bench",
			"-F", "--features", "-j", "--jobs", "--target", "--profile", "-E")
		if m, ok := f.flags["--manifest-path"]; ok {
			dir = within(dir, filepath.Dir(m))
		}
		if len(f.pos) > 0 || f.has("-p", "--package", "--lib", "--bins", "--test", "--bin", "--doc", "-E") {
			return suiteRun{}, false
		}
		if f.has("--workspace", "--all") || dir == "" || workspaceRoot(dir) {
			return suiteRun{Runner: "cargo", Dir: dir}, true
		}
	case "turbo":
		f := parseFlags(args, "--filter", "-F", "--concurrency")
		if len(f.pos) > 0 && f.pos[0] == "run" {
			f.pos = f.pos[1:]
		}
		if len(f.pos) > 0 && isTestTask(f.pos[0]) && !f.has("--filter", "-F", "--affected") {
			return suiteRun{Runner: "turbo", Dir: dir}, true
		}
	case "nx":
		f := parseFlags(args, "-t", "--target", "--targets", "-p", "--projects")
		if len(f.pos) > 0 && f.pos[0] == "run-many" && !f.has("-p", "--projects") {
			for _, k := range []string{"-t", "--target", "--targets"} {
				if isTestTask(f.flags[k]) {
					return suiteRun{Runner: "nx", Dir: dir}, true
				}
			}
		}
	case "lerna":
		f := parseFlags(args, "--scope", "--since", "--ignore")
		if len(f.pos) > 1 && f.pos[0] == "run" && isTestTask(f.pos[1]) && !f.has("--scope", "--since") {
			return suiteRun{Runner: "lerna", Dir: dir}, true
		}
	case "playwright":
		f := parseFlags(args, "--project", "-c", "--config", "--reporter", "-j", "--workers", "-g", "--grep")
		if len(f.pos) == 1 && f.pos[0] == "test" && !f.has("-g", "--grep", "--only-changed", "--last-failed") {
			return suiteRun{Runner: "playwright", Dir: dir}, true
		}
	case "make", "just":
		f := parseFlags(args, "-C", "-f", "-j", "--directory", "--file", "--justfile")
		for _, target := range f.pos {
			if slices.Contains([]string{"test", "tests", "check", "test-all", "tests-all", "check-all", "testall"}, target) {
				return suiteRun{Runner: "make", Dir: dir}, true
			}
		}
	case "gradle", "gradlew":
		f := parseFlags(args, "--tests", "-p", "--project-dir")
		if f.has("--tests") {
			return suiteRun{}, false
		}
		for _, task := range f.pos {
			if task == "test" || task == "check" {
				return suiteRun{Runner: "gradle", Dir: dir}, true
			}
		}
	case "mvn", "mvnw":
		f := parseFlags(args, "-pl", "--projects", "-f", "--file", "-P")
		for k := range f.flags {
			if strings.HasPrefix(k, "-Dtest") || strings.HasPrefix(k, "-Dit.test") {
				return suiteRun{}, false
			}
		}
		if f.has("-pl", "--projects") {
			return suiteRun{}, false
		}
		for _, goal := range f.pos {
			if goal == "test" || goal == "verify" {
				return suiteRun{Runner: "mvn", Dir: dir}, true
			}
		}
	case "bazel", "bazelisk":
		if len(args) > 1 && args[0] == "test" && slices.Contains(args[1:], "//...") {
			return suiteRun{Runner: "bazel", Dir: dir}, true
		}
	}
	return suiteRun{}, false
}

func isTestTask(task string) bool {
	return task == "test" || strings.HasPrefix(task, "test:") || task == "tests"
}

// packageScript is npm/pnpm/yarn/bun: a bare `npm test` (or `npm run test`)
// is the package's whole suite, as is a recursive one. Arguments after it,
// files or a name, make it a part of it; exec and dlx run the tool after
// them.
func packageScript(manager string, args []string, dir string) (suiteRun, bool) {
	valued := []string{"--prefix", "-C", "--dir", "--cwd", "--filter", "-F", "-w", "--workspace", "--reporter", "--loglevel"}
	i := 0
	recursive, filtered := false, false
	for ; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if !hasValue && slices.Contains(valued, name) && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch name {
		case "--prefix", "-C", "--dir", "--cwd":
			dir = within(dir, value)
		case "-r", "--recursive", "--workspaces", "-ws":
			recursive = true
		case "--filter", "-F":
			filtered = true
		}
	}
	if i >= len(args) {
		return suiteRun{}, false
	}
	sub, rest := args[i], args[i+1:]
	switch {
	case slices.Contains([]string{"exec", "x", "dlx"}, sub):
		r := parseFlags(rest)
		if len(r.pos) == 0 {
			return suiteRun{}, false
		}
		j := slices.Index(rest, r.pos[0])
		return wholeSuite(withoutPrefixes(rest[j:]), dir)
	case (manager == "pnpm" || manager == "yarn") && (sub == "vitest" || sub == "jest"):
		return jsRunner(sub, rest, dir)
	case manager == "yarn" && sub == "workspaces":
		r := parseFlags(rest, "--include", "--exclude", "--from")
		if r.has("--since", "--include", "--from") {
			return suiteRun{}, false
		}
		for _, w := range r.pos {
			if isTestTask(w) {
				return suiteRun{Runner: "pnpm", Dir: dir}, true
			}
		}
		return suiteRun{}, false
	case sub == "run" || sub == "run-script":
		if len(rest) == 0 || !isTestTask(rest[0]) || rest[0] != "test" && !recursive {
			return suiteRun{}, false
		}
		rest = rest[1:]
	case manager == "bun" && sub == "test":
		if f := parseFlags(rest, "-t", "--test-name-pattern", "--timeout", "--preload"); len(f.pos) > 0 || f.has("-t", "--test-name-pattern", "--changed") {
			return suiteRun{}, false
		}
		return suiteRun{Runner: "bun", Dir: dir}, true
	case sub == "test" || sub == "t" || sub == "tst":
	default:
		return suiteRun{}, false
	}
	if recursive {
		if filtered {
			return suiteRun{}, false
		}
		return suiteRun{Runner: "pnpm", Dir: dir}, true
	}
	// What follows -- goes to the runner: its flags are still flags.
	rest = slices.DeleteFunc(slices.Clone(rest), func(a string) bool { return a == "--" })
	if len(parseFlags(rest, "--reporter", "-c", "--config", "--prefix", "-C", "--dir", "--workspace", "-w").pos) > 0 {
		return suiteRun{}, false
	}
	return suiteRun{Runner: scriptRunner(dir), Dir: dir}, true
}

// scriptRunner is the test runner a package's test script runs: vitest,
// jest or node (its own --test), from package.json in dir; "npm" when it
// can't tell.
func scriptRunner(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if dir == "" || err != nil {
		return "npm"
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	_ = json.Unmarshal(b, &pkg)
	script := pkg.Scripts["test"]
	switch {
	case strings.Contains(script, "vitest") || strings.Contains(script, "vp test"):
		return "vitest"
	case strings.Contains(script, "jest"):
		return "jest"
	case strings.Contains(script, "node ") && strings.Contains(script, "--test"):
		return "node"
	}
	return "npm"
}

// jsRunner is vitest or jest with no files: their whole suite. `vitest
// related`, a file or a name filter, and --changed, --findRelatedTests or
// --onlyChanged are parts of it.
func jsRunner(name string, args []string, dir string) (suiteRun, bool) {
	f := parseFlags(args, "-c", "--config", "--reporter", "--reporters", "--root", "-r", "--dir", "--pool", "--environment",
		"--maxWorkers", "-w", "--minWorkers", "--outputFile", "--rootDir", "--project", "--selectProjects", "--shard")
	pos := f.pos
	if name == "vitest" && len(pos) > 0 {
		switch pos[0] {
		case "run", "watch", "dev":
			pos = pos[1:]
		default:
			return suiteRun{}, false // related, bench, list, a file
		}
	}
	if len(pos) > 0 || f.has("--changed", "--related", "--findRelatedTests", "-o", "--onlyChanged", "--changedSince",
		"-t", "--testNamePattern", "--testPathPattern", "--testPathPatterns", "--shard", "--project", "--selectProjects") {
		return suiteRun{}, false
	}
	return suiteRun{Runner: name, Dir: dir}, true
}

// pytest with no files or test ids is the whole suite; -k, -m, --lf and a
// file narrow it.
func pytest(args []string, dir string) (suiteRun, bool) {
	f := parseFlags(args, "-c", "-p", "-n", "--maxfail", "--tb", "--durations", "-o", "--rootdir", "--junitxml", "--basetemp",
		"--cov", "--cov-report", "-W", "--confcutdir", "--log-level", "--import-mode", "--dist")
	if len(f.pos) > 0 || f.has("-k", "-m", "--lf", "--last-failed", "--sw", "--stepwise", "--ff", "--failed-first", "--co", "--collect-only") {
		return suiteRun{}, false
	}
	return suiteRun{Runner: "pytest", Dir: dir}, true
}

// workspaceRoot says whether dir is a Cargo workspace's root, or a
// repository's own crate.
func workspaceRoot(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "[workspace]") || fileExists(filepath.Join(dir, ".git"))
}

// The message. It names the command refused and what to run instead, from
// the files the agent changed since its base.

// fullSuiteMessage is what the hook tells the model when it refuses run:
// a few lines, the suggestion worked out from what changed since base.
func fullSuiteMessage(ctx context.Context, run suiteRun, base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Not run: `%s` runs the whole test suite. Agents run only the tests their change touches and leave the whole suite to CI: here it takes the VM's memory from every other agent.\n", run.Command)
	if instead := relatedTests(ctx, run, base); instead != "" {
		fmt.Fprintf(&b, "Run instead:\n\n  %s\n\n", instead)
	} else {
		b.WriteString("Run the tests of the files you changed instead, by file or package.\n")
	}
	fmt.Fprintf(&b, "Only when your task, or a message from the lead or the user, asks for the full suite in so many words, run it as `AGENTBOX_FULL_SUITE=1 %s`.\n", run.Command)
	return b.String()
}

// maxSuggested is how many packages or files a suggestion names at most.
const maxSuggested = 20

// relatedTests is the command that runs the tests of what changed, "" when
// it can't tell.
func relatedTests(ctx context.Context, run suiteRun, base string) string {
	if run.Dir == "" {
		return ""
	}
	root := gitRoot(run.Dir)
	if root == "" {
		return ""
	}
	changed := changedFiles(ctx, root, base)
	cd := ""
	from := run.Cwd
	if from == "" {
		from = root
	}
	if rel, err := filepath.Rel(from, run.Dir); err == nil && rel != "." {
		cd = "cd " + rel + " && "
	}
	// Changed files as run.Dir sees them, inside it.
	var here []string
	for _, f := range changed {
		rel, err := filepath.Rel(run.Dir, filepath.Join(root, f))
		if err == nil && !strings.HasPrefix(rel, "..") {
			here = append(here, filepath.ToSlash(rel))
		}
	}
	switch run.Runner {
	case "go":
		pkgs := goPackages(run.Dir, here)
		if len(pkgs) == 0 {
			return ""
		}
		return cd + "go test " + strings.Join(capped(pkgs), " ")
	case "vitest":
		if js := matching(here, jsFile); len(js) > 0 {
			return cd + "npx vitest related --run " + strings.Join(capped(js), " ")
		}
	case "jest":
		if js := matching(here, jsFile); len(js) > 0 {
			return cd + "npx jest --findRelatedTests " + strings.Join(capped(js), " ")
		}
	case "node", "bun", "npm":
		tests := siblingTests(run.Dir, matching(here, jsFile))
		if len(tests) == 0 {
			return ""
		}
		if run.Runner == "bun" {
			return cd + "bun test " + strings.Join(capped(tests), " ")
		}
		return cd + "node --test " + strings.Join(capped(tests), " ")
	case "pytest":
		tests := pythonTests(ctx, root, run.Dir, here)
		if len(tests) == 0 {
			return ""
		}
		return cd + "pytest " + strings.Join(capped(tests), " ")
	case "cargo":
		crates := cargoCrates(run.Dir, here)
		if len(crates) == 0 {
			return ""
		}
		var b strings.Builder
		b.WriteString(cd + "cargo test")
		for _, c := range capped(crates) {
			b.WriteString(" -p " + c)
		}
		return b.String()
	case "turbo":
		return cd + "turbo run test --affected"
	case "nx":
		return cd + "nx affected -t test"
	case "lerna":
		if base != "" {
			return cd + "lerna run test --since " + base
		}
	case "pnpm":
		if base != "" {
			return cd + `pnpm --filter "...[` + base + `]" test`
		}
	}
	return ""
}

func capped(s []string) []string {
	if len(s) > maxSuggested {
		return s[:maxSuggested]
	}
	return s
}

var jsFile = regexp.MustCompile(`\.(?:[cm]?[jt]sx?|vue|svelte)$`)

func matching(files []string, re *regexp.Regexp) []string {
	var out []string
	for _, f := range files {
		if re.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}

func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if fileExists(filepath.Join(d, ".git")) {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// changedFiles are the files, relative to root, that differ from base, or
// from HEAD without one, committed or not, and the new ones git doesn't
// ignore: those still there.
func changedFiles(ctx context.Context, root, base string) []string {
	if base == "" {
		base = "HEAD"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	git := func(args ...string) []string {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			return nil
		}
		return strings.Fields(string(out))
	}
	files := git("diff", "--name-only", base, "--")
	files = append(files, git("ls-files", "--others", "--exclude-standard")...)
	slices.Sort(files)
	files = slices.Compact(files)
	return slices.DeleteFunc(files, func(f string) bool { return !fileExists(filepath.Join(root, f)) })
}

// goPackages are the packages, as ./dir under dir, that the changed files
// (relative to dir) are in: a .go file's own, and for any other file the
// package of its directory, testdata's parent for one under testdata.
func goPackages(dir string, files []string) []string {
	var pkgs []string
	for _, f := range files {
		d := path.Dir(f)
		if i := strings.Index("/"+d+"/", "/testdata/"); i >= 0 {
			d = strings.TrimSuffix(d[:max(i-1, 0)], "/")
			if d == "" {
				d = "."
			}
		}
		if !strings.HasSuffix(f, ".go") && !hasGoFiles(filepath.Join(dir, d)) {
			continue
		}
		if d == "." {
			pkgs = append(pkgs, ".")
		} else {
			pkgs = append(pkgs, "./"+d)
		}
	}
	slices.Sort(pkgs)
	return slices.Compact(pkgs)
}

func hasGoFiles(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return len(m) > 0
}

var testFile = regexp.MustCompile(`[._-](?:test|spec)\.[cm]?[jt]sx?$`)

// siblingTests are the changed test files and the tests beside each changed
// source file (foo.test.ts by foo.ts), for a runner with no "related".
func siblingTests(dir string, files []string) []string {
	var tests []string
	for _, f := range files {
		if testFile.MatchString(f) {
			tests = append(tests, f)
			continue
		}
		ext := path.Ext(f)
		stem := strings.TrimSuffix(f, ext)
		for _, kind := range []string{".test", ".spec"} {
			if t := stem + kind + ext; fileExists(filepath.Join(dir, t)) {
				tests = append(tests, t)
			}
		}
	}
	slices.Sort(tests)
	return slices.Compact(tests)
}

// pythonTests are the changed test files and, for each changed module
// foo.py, the repository's test_foo.py and foo_test.py, as dir sees them.
func pythonTests(ctx context.Context, root, dir string, files []string) []string {
	var tests, modules []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".py") {
			continue
		}
		base := path.Base(f)
		if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
			tests = append(tests, f)
		} else if base != "__init__.py" && base != "conftest.py" {
			modules = append(modules, strings.TrimSuffix(base, ".py"))
		}
	}
	if len(modules) > 0 {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--", "*.py").Output()
		for _, f := range strings.Fields(string(out)) {
			base := path.Base(f)
			for _, m := range modules {
				if base == "test_"+m+".py" || base == m+"_test.py" {
					if rel, err := filepath.Rel(dir, filepath.Join(root, f)); err == nil && !strings.HasPrefix(rel, "..") {
						tests = append(tests, filepath.ToSlash(rel))
					}
				}
			}
		}
	}
	slices.Sort(tests)
	return slices.Compact(tests)
}

var cargoName = regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`)

// cargoCrates are the crates the changed files are in: each one's nearest
// Cargo.toml with a [package].
func cargoCrates(dir string, files []string) []string {
	var crates []string
	for _, f := range files {
		for d := path.Dir(f); ; d = path.Dir(d) {
			b, err := os.ReadFile(filepath.Join(dir, d, "Cargo.toml"))
			if err == nil {
				if _, pkg, ok := strings.Cut(string(b), "[package]"); ok {
					if m := cargoName.FindStringSubmatch(pkg); m != nil {
						crates = append(crates, m[1])
					}
					break
				}
			}
			if d == "." || d == "/" {
				break
			}
		}
	}
	slices.Sort(crates)
	return slices.Compact(crates)
}
