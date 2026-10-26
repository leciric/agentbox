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
	// Warning, for a name an AI tool picks its credential by (ANTHROPIC_API_KEY),
	// says that the tool won't use it while signed in to an account: AgentBox
	// keeps it out of the tool's own environment (internal/toolcreds).
	Warning string `json:"warning,omitempty"`
	// LeadManaged, for a project secret, means the project's lead keeps it
	// fresh and may fulfil agents' credential requests for it with the stored
	// value, by name (fulfil_credential): it never reads the value either.
	LeadManaged bool `json:"leadManaged"`
}

// SetSecretRequest is the body of PUT .../secrets/{name}. Value is the only
// place a value appears in this API, and only ever on the way in.
// LeadManaged, when set, marks a project secret as the lead's to keep
// fresh, or not; left out, a new secret isn't and a stored one stays as it
// was, so a lead refreshing the value doesn't have to say it again.
type SetSecretRequest struct {
	Value       string `json:"value"`
	LeadManaged *bool  `json:"leadManaged,omitempty"`
}

// SecretSettingsRequest is the body of PATCH .../projects/{project}/secrets/{name}:
// what can change about a project secret without giving its value again.
type SecretSettingsRequest struct {
	LeadManaged bool `json:"leadManaged"`
}

// FulfilCredentialResponse is what the lead learns from fulfilling an
// agent's request for a lead-managed secret: the answered request and when
// the value the agent now has was stored. Never the value.
type FulfilCredentialResponse struct {
	Question   Question  `json:"question"`
	ValueSetAt time.Time `json:"valueSetAt"`
}

// BrowserCookies is a project's browser cookie import, as the app and the
// command line see it: how many cookies, the sites they cover and when, never
// a cookie. The cookies come either from the user's own installed browser,
// read and decrypted directly (Source names the browser and profile), or from
// an export they made (cookies.txt, or a cookie extension's or Playwright's
// JSON; Source is then empty and Format names the kind). Either way they are
// sealed as a project secret and set into the Chromium of agents created after
// the import, the first time it starts.
type BrowserCookies struct {
	Imported bool `json:"imported"`
	// Domains is what was imported: the sites picked for a file export, or
	// every site for a browser import. Sites is the per-site cookie count.
	Domains    []string       `json:"domains"`
	Sites      []CookieDomain `json:"sites,omitempty"`
	Cookies    int            `json:"cookies"`
	Format     string         `json:"format,omitempty"`
	Source     string         `json:"source,omitempty"`
	ImportedAt *time.Time     `json:"importedAt,omitempty"`
}

// BrowserProfile is one profile of a browser installed on the host, for the
// user to import from. Keyring, when set, names the browser's entry in the
// host keyring whose secret the desktop app must fetch (the Go daemon in the
// VM can't reach it) and pass to the import.
type BrowserProfile struct {
	ID          string `json:"id"`
	Browser     string `json:"browser"`
	BrowserName string `json:"browserName"`
	Engine      string `json:"engine"`
	Name        string `json:"name"`
	Keyring     string `json:"keyring,omitempty"`
}

// BrowserProfiles is the browsers found installed on the host.
type BrowserProfiles struct {
	Profiles []BrowserProfile `json:"profiles"`
	// Unreadable are browsers installed on Windows whose cookies only the
	// browser itself can open (Chrome, Edge, Brave): the user exports instead.
	Unreadable []UnreadableBrowser `json:"unreadable,omitempty"`
}

// UnreadableBrowser is an installed browser AgentBox can't import from.
type UnreadableBrowser struct {
	Browser     string `json:"browser"`
	BrowserName string `json:"browserName"`
}

// ImportFromBrowserRequest imports every cookie of one installed browser
// profile. KeyringSecret is the browser's keyring passphrase, fetched on the
// host by the desktop app for a Chromium profile with a Keyring; empty for
// Firefox, or when it couldn't be fetched (then only unencrypted and
// legacy-keyed cookies are read). The secret is used to decrypt and is never
// stored or logged.
type ImportFromBrowserRequest struct {
	ProfileID     string `json:"profileId"`
	KeyringSecret string `json:"keyringSecret"`
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
