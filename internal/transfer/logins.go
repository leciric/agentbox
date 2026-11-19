package transfer

import (
	"bytes"
	"fmt"

	"agentbox/internal/portable"
)

// login is one account AgentBox keeps: a GitHub or Claude Code token, or a
// Codex, OpenCode or Cursor login file (OpenCode's for one provider each).
type login struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Default  bool   `json:"default,omitempty"`
	// Token is a GitHub or Claude Code token; Auth a tool's login file.
	Token string `json:"token,omitempty"`
	Auth  []byte `json:"auth,omitempty"`
	// Login is the GitHub user a GitHub token belongs to.
	Login string `json:"login,omitempty"`
}

func (in Instance) logins(kind string) ([]login, error) {
	c := in.Creds
	var out []login
	switch kind {
	case LoginGitHub:
		accounts, err := c.GitHubAccounts()
		if err != nil {
			return nil, err
		}
		for _, a := range accounts {
			token, err := c.GitHubToken(a.Name)
			if err != nil {
				return nil, err
			}
			out = append(out, login{Name: a.Name, Default: a.Default, Token: token, Login: a.Login})
		}
	case LoginClaude:
		accounts, err := c.ClaudeAccounts()
		if err != nil {
			return nil, err
		}
		for _, a := range accounts {
			token, err := c.ClaudeToken(a.Name)
			if err != nil {
				return nil, err
			}
			out = append(out, login{Name: a.Name, Default: a.Default, Token: token})
		}
	default:
		accounts, err := c.ToolAccounts(kind)
		if err != nil {
			return nil, err
		}
		for _, a := range accounts {
			auth, _, err := c.ToolAuth(kind, a.Provider, a.Name)
			if err != nil {
				return nil, err
			}
			if auth != nil {
				out = append(out, login{Name: a.Name, Provider: a.Provider, Default: a.Default, Auth: auth})
			}
		}
	}
	return out, nil
}

func (in Instance) exportLogins(w *portable.Writer, kind string) (portable.Entry, error) {
	list, err := in.logins(kind)
	if err != nil {
		return portable.Entry{}, err
	}
	if list == nil {
		list = []login{}
	}
	return w.AddJSON(portable.Entry{ID: LoginEntryID(kind), Kind: kind, Items: len(list)}, list)
}

// importLogins adds the accounts this instance doesn't have. One it has under
// the same name with another login is a conflict: its own stays. Which account
// is the default isn't changed, beyond what saving the first one does.
func (in Instance) importLogins(kind string, list []login, c *Counts) error {
	cr := in.Creds
	for _, l := range list {
		var have []byte
		var err error
		switch kind {
		case LoginGitHub:
			var token string
			if ok, _ := cr.HasGitHubAccount(l.Name); ok {
				if token, err = cr.GitHubToken(l.Name); err == nil {
					have = []byte(token)
				}
			}
		case LoginClaude:
			var token string
			if ok, _ := cr.HasClaudeAccount(l.Name); ok {
				if token, err = cr.ClaudeToken(l.Name); err == nil {
					have = []byte(token)
				}
			}
		default:
			have, _, err = cr.ToolAuth(kind, l.Provider, l.Name)
		}
		if err != nil {
			return fmt.Errorf("%s account %s: %w", kind, l.Name, err)
		}
		want := l.Auth
		if kind == LoginGitHub || kind == LoginClaude {
			want = []byte(l.Token)
		}
		switch {
		case have == nil:
		case bytes.Equal(have, want):
			c.Skipped++
			continue
		default:
			c.conflict(fmt.Sprintf("%s account %s: this instance has another login under that name, kept", kind, accountName(l)))
			continue
		}
		switch kind {
		case LoginGitHub:
			err = cr.SaveGitHubToken(l.Name, l.Token)
			if err == nil && l.Login != "" {
				err = cr.SaveGitHubLogin(l.Name, l.Login)
			}
		case LoginClaude:
			err = cr.SaveClaudeToken(l.Name, l.Token)
		default:
			err = cr.SaveToolAccount(kind, l.Provider, l.Name, l.Auth)
		}
		if err != nil {
			return fmt.Errorf("%s account %s: %w", kind, accountName(l), err)
		}
		c.Added++
	}
	return nil
}

func accountName(l login) string {
	if l.Provider != "" {
		return l.Provider + "/" + l.Name
	}
	return l.Name
}
