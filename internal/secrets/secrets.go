// Package secrets holds the API keys and tokens you hand to agents.
//
// A secret is a name, validated like an environment variable, and a value, at
// project scope (every agent of the project gets it) or for one agent. Values
// are sealed with AES-256-GCM under a key in ~/.config/agentbox/secrets.key
// before they reach SQLite, and are opened in one direction only: into the
// agent they belong to, as a file inside its machine.
//
// What the encryption is for, stated plainly: it protects a *copied* state.db —
// a backup, a synced directory, a disk someone else reads — not a compromised
// user account. The key sits next to the database, readable by the same user,
// and the Claude Code and GitHub tokens in ~/.config/agentbox/credentials are
// already plain 0600 files. Anyone who can read your home directory can read
// your secrets. See D52.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"agentbox/internal/state"
)

// MaxNameLen keeps names readable in lists; MaxValueLen is a sanity bound, well
// above any real key, so a misdirected file upload fails here rather than
// landing in every agent's shell environment.
const (
	MaxNameLen  = 64
	MaxValueLen = 64 << 10
)

// envName is an environment variable name: what a shell can export and an AI
// tool can read as $NAME.
var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// reserved are the variables AgentBox writes into every agent itself
// (agentEnv in internal/agent). A secret of the same name would be sourced
// after them and quietly replace a login, so it is refused with the command
// that really changes that thing.
var reserved = map[string]string{
	"CLAUDE_CODE_OAUTH_TOKEN": "the Claude Code login: agentbox claude-account <project|project/agent> <account>",
	"GH_TOKEN":                "the GitHub login: agentbox github-account <project|project/agent> <account>",
	"GITHUB_TOKEN":            "the GitHub login: agentbox github-account <project|project/agent> <account>",
	"COMPOSE_PROJECT_NAME":    "the Compose project every agent of a project shares",
}

// ValidateName checks a secret's name: an environment variable name, so that
// what the list says is exactly what the agent reads.
func ValidateName(n string) error {
	switch {
	case n == "":
		return errors.New("a secret needs a name, like OPENAI_API_KEY")
	case len(n) > MaxNameLen:
		return fmt.Errorf("the name %q is longer than %d characters", n, MaxNameLen)
	case !envName.MatchString(n):
		return fmt.Errorf("invalid secret name %q: use an environment variable name — capital letters, digits and underscores, not starting with a digit (like OPENAI_API_KEY)", n)
	}
	if why, ok := reserved[n]; ok {
		return fmt.Errorf("%s is written into every agent by AgentBox: change it where it comes from, %s", n, why)
	}
	return nil
}

// Secret is a stored secret without its value. It is what every list, event
// and log sees: the name, where it lives, and how old the value is.
type Secret struct {
	Project   string
	Agent     string // empty for a project secret
	Name      string
	UpdatedAt time.Time
}

// Scope is state.ScopeProject or state.ScopeAgent.
func (s Secret) Scope() string {
	if s.Agent == "" {
		return state.ScopeProject
	}
	return state.ScopeAgent
}

// Value is a secret on its way into an agent: the only place a plaintext value
// goes.
type Value struct {
	Name  string
	Value string
}

// Store is the secrets of every project, in the daemon's state, sealed under
// the key at KeyPath.
type Store struct {
	State   *state.Store
	KeyPath string // ~/.config/agentbox/secrets.key
}

// ready reports the programming error of a store built without state: the
// daemon builds the one real Store (server.go), and a Manager that reached
// here without one would otherwise deliver silently empty files.
func (s Store) ready() error {
	if s.State == nil {
		return errors.New("no secrets store: this AgentBox was built without one, which is a bug")
	}
	return nil
}

// Set stores a value under a name, replacing whatever was there. An empty agent
// is a project secret. It returns the stored secret, which carries no value.
func (s Store) Set(ctx context.Context, project, agent, secretName, value string) (Secret, error) {
	if err := s.ready(); err != nil {
		return Secret{}, err
	}
	if err := ValidateName(secretName); err != nil {
		return Secret{}, err
	}
	if err := validateValue(secretName, value); err != nil {
		return Secret{}, err
	}
	sealed, err := s.seal(value)
	if err != nil {
		return Secret{}, err
	}
	stored := state.Secret{Project: project, Agent: agent, Name: secretName, Value: sealed, UpdatedAt: time.Now().Truncate(time.Second)}
	if err := s.State.SetSecret(ctx, stored); err != nil {
		return Secret{}, err
	}
	return Secret{Project: project, Agent: agent, Name: secretName, UpdatedAt: stored.UpdatedAt}, nil
}

// validateValue refuses what could never become an environment variable, and
// says which secret it was about: the value itself must never be echoed back.
func validateValue(secretName, value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%s has no value: pipe one in, or remove the secret", secretName)
	case len(value) > MaxValueLen:
		return fmt.Errorf("the value of %s is %d bytes, more than the %d a secret may hold: it is a key or a token, not a file", secretName, len(value), MaxValueLen)
	case strings.ContainsRune(value, 0):
		return fmt.Errorf("the value of %s contains a NUL byte, which no environment variable can hold", secretName)
	}
	return nil
}

// Remove forgets one secret. Running agents lose it the next time their file is
// written, which is what the daemon does right after this.
func (s Store) Remove(ctx context.Context, project, agent, secretName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.State.RemoveSecret(ctx, project, agent, secretName)
}

// List is one scope's secrets, by name: a project's own when agent is "",
// otherwise that agent's own.
func (s Store) List(ctx context.Context, project, agent string) ([]Secret, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	stored, err := s.State.Secrets(ctx, project, agent)
	if err != nil {
		return nil, err
	}
	return without(stored), nil
}

// Project is every secret of a project, both scopes, project ones first.
func (s Store) Project(ctx context.Context, project string) ([]Secret, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	stored, err := s.State.ProjectSecrets(ctx, project)
	if err != nil {
		return nil, err
	}
	return without(stored), nil
}

// without drops the ciphertext, which is where the boundary is: everything
// above this package works on names.
func without(stored []state.Secret) []Secret {
	out := make([]Secret, 0, len(stored))
	for _, sec := range stored {
		out = append(out, Secret{Project: sec.Project, Agent: sec.Agent, Name: sec.Name, UpdatedAt: sec.UpdatedAt})
	}
	return out
}

// ForAgent is what one agent gets, opened and ready to write into it: its
// project's secrets, then its own, so an agent's own value of a name it shares
// with its project wins. Names are sorted, so an unchanged set renders to an
// unchanged file.
func (s Store) ForAgent(ctx context.Context, project, agent string) ([]Value, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	scoped, err := s.State.Secrets(ctx, project, "")
	if err != nil {
		return nil, err
	}
	own, err := s.State.Secrets(ctx, project, agent)
	if err != nil {
		return nil, err
	}
	byName := map[string]string{}
	for _, sec := range slices.Concat(scoped, own) {
		value, err := s.open(sec.Value)
		if err != nil {
			return nil, fmt.Errorf("%s of %s/%s: %w", sec.Name, project, agent, err)
		}
		byName[sec.Name] = value
	}
	values := make([]Value, 0, len(byName))
	for n, value := range byName {
		values = append(values, Value{Name: n, Value: value})
	}
	slices.SortFunc(values, func(a, b Value) int { return strings.Compare(a.Name, b.Name) })
	return values, nil
}

// NamesForAgent is the names one agent has as environment variables, sorted.
// Nothing is decrypted: the brief and the app's lists only ever need these.
func (s Store) NamesForAgent(ctx context.Context, project, agent string) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	scoped, err := s.State.Secrets(ctx, project, "")
	if err != nil {
		return nil, err
	}
	own, err := s.State.Secrets(ctx, project, agent)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sec := range slices.Concat(scoped, own) {
		if !slices.Contains(names, sec.Name) {
			names = append(names, sec.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// EnvFile renders the file written inside an agent, sourced by the per-agent
// env file. Values are single-quoted with POSIX escaping, so a value holding
// quotes, spaces, newlines or a $ arrives byte for byte.
func EnvFile(values []Value) string {
	var b strings.Builder
	b.WriteString("# Written by AgentBox: the secrets you were given (agentbox secrets).\n")
	b.WriteString("# Never print, commit or echo these values.\n")
	for _, v := range values {
		b.WriteString("export " + v.Name + "=" + quote(v.Value) + "\n")
	}
	return b.String()
}

// quote is POSIX single quoting: everything inside is literal, and a single
// quote is spliced in from outside. The same rule as shellQuote in
// internal/agent, kept here because this file is rendered without it.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// seal encrypts a value: nonce, then AES-256-GCM ciphertext.
func (s Store) seal(value string) ([]byte, error) {
	aead, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(value), nil), nil
}

// open decrypts a stored value. A value that won't open is reported as what it
// is — the key isn't the one it was sealed with — rather than as corruption,
// because a lost or replaced secrets.key is the way this really happens.
func (s Store) open(sealed []byte) (string, error) {
	aead, err := s.aead()
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("the stored value is too short to be a sealed secret")
	}
	nonce, body := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("this secret can't be opened with %s: set it again, or restore the key it was stored under", s.KeyPath)
	}
	return string(plain), nil
}

func (s Store) aead() (cipher.AEAD, error) {
	key, err := s.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// KeySize is AES-256's key length.
const KeySize = 32

// key reads the machine's secrets key, creating it on first use. The key is
// created with a link from a temporary file, which fails if the key already
// exists, so two writers can't each install a different key and make the
// other's secrets unreadable.
func (s Store) key() ([]byte, error) {
	if s.KeyPath == "" {
		return nil, errors.New("no secrets key path: the daemon builds this store with one")
	}
	key, err := readKey(s.KeyPath)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}
	if err := os.MkdirAll(filepath.Dir(s.KeyPath), 0o700); err != nil {
		return nil, err
	}
	fresh := make([]byte, KeySize)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.KeyPath), ".secrets.key-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.WriteString(base64.StdEncoding.EncodeToString(fresh) + "\n"); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmp.Name(), s.KeyPath); err != nil {
		// Somebody else created it between the read and the link: theirs is
		// the key everything is sealed under.
		return readKey(s.KeyPath)
	}
	return fresh, nil
}

func readKey(path string) ([]byte, error) {
	stored, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(stored)))
	if err != nil || len(key) != KeySize {
		return nil, fmt.Errorf("%s isn't an AgentBox secrets key (%d base64 bytes expected): move it aside to start over, and set the secrets again", path, KeySize)
	}
	return key, nil
}
