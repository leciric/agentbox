package cookieimport

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// Chromium keeps each cookie's value in encrypted_value, sealed with a key
// derived from a passphrase. On Linux and macOS that passphrase is the
// browser's entry in the OS keyring (Secret Service / Keychain); older
// profiles, and ones the browser wrote before a keyring was available, use
// the constant "peanuts". The scheme is the documented one that curl, yt-dlp
// and t3code all read: AES-128-CBC with a 16-byte space IV, PBKDF2-SHA1 key
// derivation, and a "v10"/"v11" prefix naming which passphrase was used. For
// profiles whose metadata version is 24 or newer the decrypted value is
// prefixed with SHA256(host_key), which is stripped.
//
// The host's keyring can't be reached from the VM, so the passphrase is
// fetched on the host by the desktop app and passed in here; with none, only
// v10 ("peanuts") and unencrypted values decode, which still covers many
// Linux profiles.

const (
	chromiumSalt       = "saltysalt"
	chromiumV10Pass    = "peanuts"
	chromiumIterMac    = 1003
	chromiumIterLinux  = 1
	chromiumKeyLen     = 16
	chromiumEpochToUTC = 11644473600 // seconds between 1601-01-01 and 1970-01-01
)

// chromiumKeys holds the AES keys a profile's values may be sealed with,
// keyed by the "v10"/"v11" version prefix.
type chromiumKeys struct {
	v10, v11 []byte
}

// deriveChromiumKeys builds the candidate keys. keyringSecret is the browser's
// keyring passphrase (empty when it couldn't be fetched); goos chooses the
// PBKDF2 iteration count Chromium uses on that platform.
func deriveChromiumKeys(keyringSecret, goos string) chromiumKeys {
	iter := chromiumIterLinux
	if goos == "darwin" {
		iter = chromiumIterMac
	}
	key := func(pass string) []byte {
		return pbkdf2.Key([]byte(pass), []byte(chromiumSalt), iter, chromiumKeyLen, sha1.New)
	}
	keys := chromiumKeys{v10: key(chromiumV10Pass)}
	if keyringSecret != "" {
		keys.v11 = key(keyringSecret)
	}
	return keys
}

// readChromium reads a Chromium profile's cookies from a snapshot of its
// database, decrypting each value with the derived keys. keyringSecret is the
// passphrase fetched from the host keyring, or "". Cookies that can't be
// decrypted (a value sealed with a key we don't have) are skipped, not an
// error: a profile often mixes v10 and v11.
func readChromium(db, keyringSecret, goos string, now time.Time) ([]Cookie, int, error) {
	keys := deriveChromiumKeys(keyringSecret, goos)
	conn, cleanup, err := openSnapshot(db)
	if err != nil {
		return nil, 0, err
	}
	defer cleanup()

	// meta.version tells us whether values carry a SHA256(host_key) prefix
	// (version >= 24). Absent on old databases: treat as 0.
	var metaVersion int
	_ = conn.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'version'`).Scan(&metaVersion)

	rows, err := conn.Query(`SELECT host_key, name, value, encrypted_value, path,
		expires_utc, is_secure, is_httponly, samesite
		FROM cookies WHERE top_frame_site_key = '' OR top_frame_site_key IS NULL`)
	if err != nil {
		// Older schemas have no top_frame_site_key column.
		rows, err = conn.Query(`SELECT host_key, name, value, encrypted_value, path,
			expires_utc, is_secure, is_httponly, samesite FROM cookies`)
		if err != nil {
			return nil, 0, fmt.Errorf("reading the cookies: %w", err)
		}
	}
	defer func() { _ = rows.Close() }()

	var out []Cookie
	skipped := 0
	for rows.Next() {
		var (
			host, name, plain, path          string
			enc                              []byte
			expiresUTC                       int64
			isSecure, isHTTPOnly, sameSiteID int
		)
		if err := rows.Scan(&host, &name, &plain, &enc, &path, &expiresUTC, &isSecure, &isHTTPOnly, &sameSiteID); err != nil {
			return nil, 0, err
		}
		value := plain
		if value == "" && len(enc) > 0 {
			dec, ok := keys.decrypt(enc, host, metaVersion)
			if !ok {
				skipped++
				continue
			}
			value = dec
		}
		expires := chromiumExpiry(expiresUTC)
		if expires != 0 && expires <= now.Unix() {
			continue
		}
		c := Cookie{
			Domain: strings.ToLower(host), Name: name, Value: value, Path: path,
			Expires: expires, Secure: isSecure != 0, HTTPOnly: isHTTPOnly != 0,
			SameSite: chromiumSameSite(sameSiteID),
		}
		if c.Path == "" {
			c.Path = "/"
		}
		if c.check() != nil {
			continue
		}
		out = append(out, c)
		if len(out) > MaxBrowserCookies {
			return nil, 0, fmt.Errorf("the profile has more than %d cookies", MaxBrowserCookies)
		}
	}
	return out, skipped, rows.Err()
}

// decrypt turns one encrypted_value into its plaintext. It returns ok=false
// when no key opens it, so the caller can skip that one cookie.
func (k chromiumKeys) decrypt(enc []byte, host string, metaVersion int) (string, bool) {
	if len(enc) < 3 {
		return "", false
	}
	prefix := string(enc[:3])
	body := enc[3:]
	var key []byte
	switch prefix {
	case "v10":
		key = k.v10
	case "v11":
		key = k.v11
	default:
		// No version prefix: some profiles store the value unencrypted.
		return string(enc), true
	}
	if key == nil {
		return "", false
	}
	plain, err := aesCBCDecrypt(key, body)
	if err != nil {
		return "", false
	}
	// From metadata version 24 the value is SHA256(host_key) + cookie.
	if metaVersion >= 24 {
		sum := sha256.Sum256([]byte(host))
		if len(plain) < len(sum) || !bytes.Equal(plain[:len(sum)], sum[:]) {
			return "", false
		}
		plain = plain[len(sum):]
	}
	return string(plain), true
}

// aesCBCDecrypt decrypts with the fixed 16-space IV and strips PKCS#7 padding.
func aesCBCDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%block.BlockSize() != 0 {
		return nil, errors.New("encrypted value isn't a whole number of blocks")
	}
	iv := bytes.Repeat([]byte{' '}, block.BlockSize())
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad <= 0 || pad > block.BlockSize() || pad > len(out) {
		return nil, errors.New("bad padding")
	}
	return out[:len(out)-pad], nil
}

// chromiumExpiry converts Chromium's time (microseconds since 1601) to Unix
// seconds; 0 stays 0 (a session cookie).
func chromiumExpiry(utc int64) int64 {
	if utc == 0 {
		return 0
	}
	return utc/1_000_000 - chromiumEpochToUTC
}

// chromiumSameSite maps the cookies table's samesite integer onto the
// DevTools names.
func chromiumSameSite(v int) string {
	switch v {
	case 0:
		return "None"
	case 1:
		return "Lax"
	case 2:
		return "Strict"
	}
	return ""
}
