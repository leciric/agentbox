package api

import "time"

// The secrets API: the keys and tokens you hand to agents. In its own file
// because of the one rule that shapes all of it — **no response here ever
// carries a value**. A secret's value is written, once, into the agent that
// gets it; it is never read back, by this API or by the app
// (D52).

// Secret is one stored secret, without its value: its name, where it lives,
// when the value was last written, and which agents hold it now.
type Secret struct {
	Name string `json:"name"`
	// Scope is "project" (every agent of the project gets it) or "agent".
	Scope   string `json:"scope"`
	Project string `json:"project"`
	// Agent is empty for a project secret.
	Agent string `json:"agent,omitempty"`
	// UpdatedAt is when the value was last written.
	UpdatedAt time.Time `json:"updatedAt"`
	// Agents are the agents this secret is delivered into, by ref. A project
	// secret lists every agent of the project that has it now; an agent secret
	// lists that one agent. An agent created later gets it too.
	Agents []string `json:"agents"`
}

// SetSecretRequest is the body of PUT .../secrets/{name}. Value is the only
// place a value appears in this API, and only ever on the way in.
type SetSecretRequest struct {
	Value string `json:"value"`
}

// BrowserCookies is a project's browser cookie import, as the app and the
// command line see it: the domains, how many cookies and when, never a cookie.
// The cookies come from an export the user made (cookies.txt, or a cookie
// extension's or Playwright's JSON), are sealed as a project secret, and are
// set into the Chromium of agents created after the import, the first time
// it starts.
type BrowserCookies struct {
	Imported   bool       `json:"imported"`
	Domains    []string   `json:"domains"`
	Cookies    int        `json:"cookies"`
	Format     string     `json:"format,omitempty"`
	ImportedAt *time.Time `json:"importedAt,omitempty"`
}

// BrowserCookiesPreviewRequest is an export to look into before importing it.
type BrowserCookiesPreviewRequest struct {
	Export string `json:"export"`
}

// BrowserCookiesPreview is what an export holds, by site, for the user to
// pick from: counts only.
type BrowserCookiesPreview struct {
	Format  string         `json:"format"`
	Cookies int            `json:"cookies"`
	Domains []CookieDomain `json:"domains"`
}

type CookieDomain struct {
	Domain  string `json:"domain"`
	Cookies int    `json:"cookies"`
}

// ImportBrowserCookiesRequest is the body of PUT .../browser-cookies: the
// export, and the domains to keep of it. Cookies of other domains are
// dropped before anything is stored.
type ImportBrowserCookiesRequest struct {
	Export  string   `json:"export"`
	Domains []string `json:"domains"`
}
