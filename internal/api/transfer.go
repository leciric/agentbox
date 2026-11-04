package api

import "time"

// Export and import of an instance's data (package transfer): the CLI's
// `agentbox export`/`agentbox import` and the app's Settings → Export / Import
// both preview with a plain request and run the job.

// TransferCategory is one kind of data in an export: how many things it
// holds and, once written or estimated, how many bytes.
type TransferCategory struct {
	Kind  string `json:"kind"`
	Items int    `json:"items"`
	Size  int64  `json:"size"`
}

// InstanceExportPreview is what there is to export, before choosing.
type InstanceExportPreview struct {
	Projects []InstanceExportProjectPreview `json:"projects"`
	Logins   []InstanceExportLoginPreview   `json:"logins"`
}

type InstanceExportProjectPreview struct {
	Name        string             `json:"name"`
	DisplayName string             `json:"displayName"`
	Categories  []TransferCategory `json:"categories"`
	Remote      string             `json:"remote"`
	Branch      string             `json:"branch"`
	// BundleSize is about how big the repository would be as a git bundle.
	BundleSize int64 `json:"bundleSize"`
}

type InstanceExportLoginPreview struct {
	Kind     string   `json:"kind"`
	Accounts []string `json:"accounts"`
}

// InstanceExportRequest starts an export job.
type InstanceExportRequest struct {
	// Path is the file to write, a full path.
	Path       string `json:"path"`
	Passphrase string `json:"passphrase,omitempty"`
	// NoEncrypt writes it in the clear, refused when it holds secrets or logins.
	NoEncrypt bool                          `json:"noEncrypt,omitempty"`
	Projects  []InstanceExportProjectChoice `json:"projects"`
	Logins    []string                      `json:"logins"`
}

type InstanceExportProjectChoice struct {
	Name       string   `json:"name"`
	Categories []string `json:"categories"`
	// Bundle puts the repository in as a git bundle of every branch.
	Bundle bool `json:"bundle,omitempty"`
}

// InstanceExportResult is a finished export job's result.
type InstanceExportResult struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Encrypted bool   `json:"encrypted"`
	Entries   int    `json:"entries"`
}

// InstanceImportInspectRequest opens an export to show what it holds.
type InstanceImportInspectRequest struct {
	Path       string `json:"path"`
	Passphrase string `json:"passphrase,omitempty"`
}

// InstanceImportContents is what an export holds, and what importing it would do.
type InstanceImportContents struct {
	Path      string                          `json:"path"`
	Encrypted bool                            `json:"encrypted"`
	Schema    int                             `json:"schema"`
	App       string                          `json:"app"`
	ID        string                          `json:"id"`
	CreatedAt time.Time                       `json:"createdAt"`
	Projects  []InstanceImportProjectContents `json:"projects"`
	Logins    []TransferCategory              `json:"logins"`
}

type InstanceImportProjectContents struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	// Exists is a project of that name already here, which the import merges
	// into, leaving its repository where it is.
	Exists     bool               `json:"exists"`
	Categories []TransferCategory `json:"categories"`
	Remote     string             `json:"remote"`
	Branch     string             `json:"branch"`
	Bundled    bool               `json:"bundled"`
	// Path is where its repository would go here, and Plan how it would get
	// there: "use" (a checkout is there), "clone" or "bundle". PlanError says
	// why it can't, when it can't.
	Path      string `json:"path"`
	Plan      string `json:"plan"`
	PlanError string `json:"planError,omitempty"`
}

// InstanceImportRequest starts an import job.
type InstanceImportRequest struct {
	Path       string                        `json:"path"`
	Passphrase string                        `json:"passphrase,omitempty"`
	Projects   []InstanceImportProjectChoice `json:"projects"`
	Logins     []string                      `json:"logins"`
}

type InstanceImportProjectChoice struct {
	Name       string   `json:"name"`
	Categories []string `json:"categories"`
	// Path is where its repository goes, when the project is new here; ""
	// takes InstanceImportProjectContents.Path.
	Path string `json:"path,omitempty"`
}

// InstanceImportReport is a finished import job's result: per entry, what was added,
// what was there already (skipped) and what was there with other values
// (conflicted, where this instance's own was kept).
type InstanceImportReport struct {
	Entries       []InstanceImportEntryReport `json:"entries"`
	Added         int                         `json:"added"`
	Skipped       int                         `json:"skipped"`
	Conflicted    int                         `json:"conflicted"`
	ProjectsAdded []string                    `json:"projectsAdded"`
	// ReportPath is the report written out as text.
	ReportPath string `json:"reportPath"`
}

type InstanceImportEntryReport struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Project    string   `json:"project,omitempty"`
	Added      int      `json:"added"`
	Skipped    int      `json:"skipped"`
	Conflicted int      `json:"conflicted"`
	Notes      []string `json:"notes,omitempty"`
}

// Codes of an export that can't be opened (Error.Code), so the app can ask
// for a passphrase, or say why, in its own words.
const (
	ErrorPassphraseNeeded = "passphrase-needed"
	ErrorWrongPassphrase  = "wrong-passphrase"
	ErrorNewerExport      = "newer-export"
)
