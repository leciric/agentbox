package cookieimport

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Firefox keeps its cookies in the clear in cookies.sqlite (moz_cookies), so
// reading a Firefox profile needs no key. Its profiles are listed in
// profiles.ini.

// firefoxProfiles reads profiles.ini under a Firefox root, keeping the
// profiles that have a cookies.sqlite. A root without the file (an older
// layout) is scanned for *.default* directories instead.
func firefoxProfiles(b browser, root, goos string) []Profile {
	var out []Profile
	seen := map[string]bool{}
	add := func(name, dir string) {
		db := filepath.Join(dir, "cookies.sqlite")
		if seen[db] {
			return
		}
		if fi, err := os.Stat(db); err != nil || fi.IsDir() {
			return
		}
		seen[db] = true
		if name == "" {
			name = filepath.Base(dir)
		}
		out = append(out, Profile{
			ID:      b.id + "\x00" + goos + "\x00" + dir,
			Browser: b.id, BrowserName: b.name, Engine: b.engine,
			Name: name, dir: dir, db: db, goos: goos,
		})
	}
	if data, err := os.ReadFile(filepath.Join(root, "profiles.ini")); err == nil {
		for _, p := range parseFirefoxINI(string(data)) {
			dir := p.path
			if p.relative {
				dir = filepath.Join(root, filepath.FromSlash(p.path))
			}
			add(p.name, dir)
		}
	}
	if len(out) == 0 {
		if entries, err := os.ReadDir(root); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					add("", filepath.Join(root, e.Name()))
				}
			}
		}
	}
	return out
}

type firefoxINIProfile struct {
	name, path string
	relative   bool
}

// parseFirefoxINI reads the [ProfileN] sections of profiles.ini: each has a
// Name, a Path and an IsRelative (1 by default). [Install...] and [General]
// sections are ignored.
func parseFirefoxINI(data string) []firefoxINIProfile {
	var out []firefoxINIProfile
	var cur *firefoxINIProfile
	flush := func() {
		if cur != nil && cur.path != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			flush()
			if strings.HasPrefix(line, "[Profile") {
				cur = &firefoxINIProfile{relative: true}
			}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			cur.name = strings.TrimSpace(val)
		case "Path":
			cur.path = strings.TrimSpace(val)
		case "IsRelative":
			cur.relative = strings.TrimSpace(val) != "0"
		}
	}
	flush()
	return out
}

// readFirefox reads a Firefox profile's cookies from a snapshot of its
// cookies.sqlite, dropping expired and partitioned ones. The expiry column
// is Unix seconds.
func readFirefox(db string, now time.Time) ([]Cookie, error) {
	conn, cleanup, err := openSnapshot(db)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	rows, err := conn.Query(`SELECT host, name, value, path, expiry, isSecure, isHttpOnly, sameSite
		FROM moz_cookies WHERE originAttributes = '' OR originAttributes IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("reading the Firefox cookies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Cookie
	for rows.Next() {
		var (
			host, name, value, path     string
			expiry                      int64
			isSecure, isHTTPOnly, samsz int
		)
		if err := rows.Scan(&host, &name, &value, &path, &expiry, &isSecure, &isHTTPOnly, &samsz); err != nil {
			return nil, err
		}
		if expiry != 0 && expiry <= now.Unix() {
			continue
		}
		c := Cookie{
			Domain: strings.ToLower(host), Name: name, Value: value, Path: path,
			Expires: expiry, Secure: isSecure != 0, HTTPOnly: isHTTPOnly != 0,
			SameSite: firefoxSameSite(samsz),
		}
		if c.Path == "" {
			c.Path = "/"
		}
		if c.check() != nil {
			continue
		}
		out = append(out, c)
		if len(out) > MaxBrowserCookies {
			return nil, fmt.Errorf("the profile has more than %d cookies", MaxBrowserCookies)
		}
	}
	return out, rows.Err()
}

// firefoxSameSite maps moz_cookies' sameSite integer onto the DevTools names.
func firefoxSameSite(v int) string {
	switch v {
	case 1:
		return "Lax"
	case 2:
		return "Strict"
	}
	return ""
}
