package cookieimport

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/pbkdf2"

	_ "modernc.org/sqlite"
)

// chromiumSeal encrypts a value as Chromium's os_crypt does on Linux/macOS:
// PBKDF2(pass, "saltysalt") → AES-128-CBC with a 16-space IV, the version
// prefix, and (for metaVersion >= 24) a SHA256(host) prefix on the plaintext.
// It is the inverse of chromium.go, so the test exercises the real decryptor.
func chromiumSeal(t *testing.T, prefix, pass, host, value string, iter, metaVersion int) []byte {
	t.Helper()
	key := pbkdf2.Key([]byte(pass), []byte(chromiumSalt), iter, chromiumKeyLen, sha1.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(value)
	if metaVersion >= 24 {
		sum := sha256.Sum256([]byte(host))
		plain = append(sum[:], plain...)
	}
	pad := block.BlockSize() - len(plain)%block.BlockSize()
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := bytes.Repeat([]byte{' '}, block.BlockSize())
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, plain)
	return append([]byte(prefix), out...)
}

func chromiumMicros(t time.Time) int64 {
	return (t.Unix() + chromiumEpochToUTC) * 1_000_000
}

// writeChromiumProfile builds a Chromium user-data directory with one Default
// profile and a Network/Cookies database holding the given rows.
func writeChromiumProfile(t *testing.T, root string, metaVersion int, insert func(exec func(host, name string, plain string, enc []byte, expiresUTC int64, secure, httpOnly, samesite int, topFrame string))) {
	t.Helper()
	profile := filepath.Join(root, "Default", "Network")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Local State"),
		[]byte(`{"profile":{"info_cache":{"Default":{"name":"Person 1"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(profile, "Cookies")
	conn, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB,
			path TEXT, expires_utc INTEGER, is_secure INTEGER, is_httponly INTEGER,
			samesite INTEGER, top_frame_site_key TEXT DEFAULT '')`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(`INSERT INTO meta (key, value) VALUES ('version', ?)`, metaVersion); err != nil {
		t.Fatal(err)
	}
	exec := func(host, name, plain string, enc []byte, expiresUTC int64, secure, httpOnly, samesite int, topFrame string) {
		if _, err := conn.Exec(`INSERT INTO cookies
			(host_key, name, value, encrypted_value, path, expires_utc, is_secure, is_httponly, samesite, top_frame_site_key)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			host, name, plain, enc, "/", expiresUTC, secure, httpOnly, samesite, topFrame); err != nil {
			t.Fatal(err)
		}
	}
	insert(exec)
}

func TestReadChromiumProfile(t *testing.T) {
	const keyring = "s3cret-keyring-pass"
	home := t.TempDir()
	root := filepath.Join(home, ".config", "google-chrome")
	future := chromiumMicros(time.Now().Add(24 * time.Hour))
	past := chromiumMicros(time.Now().Add(-24 * time.Hour))
	writeChromiumProfile(t, root, 24, func(add func(host, name, plain string, enc []byte, expiresUTC int64, secure, httpOnly, samesite int, topFrame string)) {
		add("github.com", "sess_v11", "", chromiumSeal(t, "v11", keyring, "github.com", "tok-v11", chromiumIterLinux, 24), future, 1, 1, 2, "")
		add("github.com", "sess_v10", "", chromiumSeal(t, "v10", chromiumV10Pass, "github.com", "tok-v10", chromiumIterLinux, 24), future, 1, 0, 1, "")
		add("plain.example", "pt", "plainval", nil, future, 0, 0, 0, "")
		add("expired.example", "old", "", chromiumSeal(t, "v10", chromiumV10Pass, "expired.example", "x", chromiumIterLinux, 24), past, 0, 0, 0, "")
		add("partitioned.example", "part", "", chromiumSeal(t, "v10", chromiumV10Pass, "partitioned.example", "x", chromiumIterLinux, 24), future, 0, 0, 0, "https://top.example")
	})

	profiles := FindProfiles(home)
	var chrome *Profile
	for i := range profiles {
		if profiles[i].Browser == "chrome" {
			chrome = &profiles[i]
		}
	}
	if chrome == nil {
		t.Fatalf("chrome profile not found in %+v", profiles)
	}
	if chrome.Name != "Person 1" || chrome.Keyring != "chrome" || chrome.Engine != EngineChromium {
		t.Fatalf("unexpected profile %+v", *chrome)
	}

	cookies, skipped, err := ReadProfile(home, chrome.ID, keyring, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Fatalf("skipped %d cookies with the key present", skipped)
	}
	got := map[string]Cookie{}
	for _, c := range cookies {
		got[c.Name] = c
	}
	if len(got) != 3 {
		t.Fatalf("got %d cookies, want 3 (expired and partitioned dropped): %v", len(got), got)
	}
	if got["sess_v11"].Value != "tok-v11" {
		t.Errorf("v11 value = %q, want tok-v11", got["sess_v11"].Value)
	}
	if got["sess_v10"].Value != "tok-v10" {
		t.Errorf("v10 value = %q, want tok-v10", got["sess_v10"].Value)
	}
	if got["pt"].Value != "plainval" {
		t.Errorf("plaintext value = %q, want plainval", got["pt"].Value)
	}
	if got["sess_v11"].SameSite != "Strict" || got["sess_v10"].SameSite != "Lax" {
		t.Errorf("samesite mapping wrong: %q %q", got["sess_v11"].SameSite, got["sess_v10"].SameSite)
	}
	if !got["sess_v11"].HTTPOnly {
		t.Error("sess_v11 should be httpOnly")
	}
}

func TestReadChromiumWithoutKeyringSkipsV11(t *testing.T) {
	const keyring = "the-key"
	home := t.TempDir()
	root := filepath.Join(home, ".config", "chromium")
	future := chromiumMicros(time.Now().Add(24 * time.Hour))
	writeChromiumProfile(t, root, 24, func(add func(host, name, plain string, enc []byte, expiresUTC int64, secure, httpOnly, samesite int, topFrame string)) {
		add("a.example", "v11", "", chromiumSeal(t, "v11", keyring, "a.example", "secret", chromiumIterLinux, 24), future, 0, 0, 0, "")
		add("b.example", "v10", "", chromiumSeal(t, "v10", chromiumV10Pass, "b.example", "peanutval", chromiumIterLinux, 24), future, 0, 0, 0, "")
	})
	profiles := FindProfiles(home)
	if len(profiles) != 1 {
		t.Fatalf("want 1 profile, got %d", len(profiles))
	}
	// No keyring secret: the v11 cookie can't be opened, the v10 one can.
	cookies, skipped, err := ReadProfile(home, profiles[0].ID, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the v11 cookie)", skipped)
	}
	if len(cookies) != 1 || cookies[0].Value != "peanutval" {
		t.Fatalf("got %+v, want only the v10 cookie", cookies)
	}
}

func TestReadFirefoxProfile(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".mozilla", "firefox")
	profDir := filepath.Join(root, "abcd1234.default-release")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := "[Install123]\nDefault=abcd1234.default-release\n\n[Profile0]\nName=default-release\nIsRelative=1\nPath=abcd1234.default-release\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(profDir, "cookies.sqlite")
	conn, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE moz_cookies (host TEXT, name TEXT, value TEXT, path TEXT,
		expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER, sameSite INTEGER, originAttributes TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(24 * time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()
	rows := [][]any{
		{".github.com", "ff", "ffval", "/", future, 1, 1, 1, ""},
		{"old.example", "gone", "x", "/", past, 0, 0, 0, ""},
		{"container.example", "c", "x", "/", future, 0, 0, 0, "^userContextId=2"},
	}
	for _, r := range rows {
		if _, err := conn.Exec(`INSERT INTO moz_cookies (host,name,value,path,expiry,isSecure,isHttpOnly,sameSite,originAttributes) VALUES (?,?,?,?,?,?,?,?,?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.Close()

	profiles := FindProfiles(home)
	if len(profiles) != 1 || profiles[0].Engine != EngineFirefox || profiles[0].Name != "default-release" {
		t.Fatalf("firefox profile wrong: %+v", profiles)
	}
	if profiles[0].Keyring != "" {
		t.Error("firefox needs no keyring")
	}
	cookies, skipped, err := ReadProfile(home, profiles[0].ID, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Errorf("firefox skipped = %d", skipped)
	}
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1 (expired and container dropped): %+v", len(cookies), cookies)
	}
	c := cookies[0]
	if c.Domain != ".github.com" || c.Value != "ffval" || c.SameSite != "Lax" || !c.HTTPOnly {
		t.Errorf("unexpected cookie %+v", c)
	}
}

func TestChromiumExpiryAndSameSite(t *testing.T) {
	if got := chromiumExpiry(0); got != 0 {
		t.Errorf("session cookie expiry = %d, want 0", got)
	}
	when := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := chromiumExpiry(chromiumMicros(when)); got != when.Unix() {
		t.Errorf("expiry round-trip = %d, want %d", got, when.Unix())
	}
	for id, want := range map[int]string{0: "None", 1: "Lax", 2: "Strict", -1: ""} {
		if got := chromiumSameSite(id); got != want {
			t.Errorf("chromiumSameSite(%d) = %q, want %q", id, got, want)
		}
	}
}
