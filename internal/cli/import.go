package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/keithah/plexctl/internal/authstore"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

func importCmd() *cobra.Command {
	var file string
	var account string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import tokens from legacy plex-monitor setup",
		Long: `Import legacy Plex account tokens into plexctl.

Supports:
  --account NAME   Import one account; read its token from stdin
  --dry-run        Parse without writing

Each token is validated against plex.tv, its owned servers are discovered,
connections are probed in preference order, and the resulting profiles are
stored like 'auth login' does. Tokens are never printed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var tokens map[string]string
			var err error
			switch {
			case file != "":
				tokens, err = parseLegacyFile(file)
				if err != nil {
					return err
				}
			case account != "":
				token, readErr := readImportToken(cmd.InOrStdin())
				if readErr != nil {
					return fmt.Errorf("read token from stdin: %w", readErr)
				}
				tokens = map[string]string{account: token}
			default:
				// Try env PLEX_TOKEN_* (legacy) and PLEXCTL_TOKENS_FILE companion
				tokens = tokensFromEnv()
				if len(tokens) == 0 {
					return fmt.Errorf("no tokens found: provide --file or --account, or set PLEX_TOKEN_*")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			failed := 0
			completed := 0
			for name, tok := range tokens {
				if strings.TrimSpace(tok) == "" {
					failed++
					fmt.Fprintf(os.Stderr, "Skipping %s: empty token\n", name)
					continue
				}
				fmt.Fprintf(os.Stderr, "Importing %s...\n", name)
				if dryRun {
					fmt.Fprintf(os.Stderr, "  (dry-run) would discover servers for %s\n", name)
					continue
				}
				count, err := importOne(ctx, name, tok)
				if err != nil {
					failed++
					fmt.Fprintf(os.Stderr, "  warning: %s: %v\n", name, err)
					continue
				}
				completed++
				fmt.Fprintf(os.Stderr, "  %s: discovered %d server(s)\n", name, count)
			}
			if failed > 0 {
				return importFailure(failed, len(tokens), completed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path to legacy plex_multi_account.py")
	cmd.Flags().StringVar(&account, "account", "", "single account name; token is read from stdin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	return cmd
}

func importFailure(failed, total, completed int) error {
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("import failed for %d of %d account(s) (%d succeeded)", failed, total, completed)
}

func readImportToken(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, 16<<10))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("stdin contained an empty token")
	}
	return token, nil
}

func tokensFromEnv() map[string]string {
	out := map[string]string{}
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k, v := parts[0], parts[1]
		if !strings.HasPrefix(k, "PLEX_TOKEN_") || v == "" {
			continue
		}
		acc := strings.ToLower(strings.TrimPrefix(k, "PLEX_TOKEN_"))
		if acc == "" {
			continue
		}
		out[acc] = v
	}
	return out
}

var legacyTokenRe = regexp.MustCompile(`'([A-Za-z0-9_.-]+)'\s*:\s*'([^']*)'`)

func parseLegacyFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := string(b)
	// Isolate DEFAULT_TOKENS = { ... } to avoid matching unrelated dicts in the file.
	start := strings.Index(s, "DEFAULT_TOKENS")
	if start == -1 {
		return nil, fmt.Errorf("no DEFAULT_TOKENS found in %s", path)
	}
	open := strings.Index(s[start:], "{")
	end := strings.Index(s[start:], "}")
	if open == -1 || end == -1 || end <= open {
		return nil, fmt.Errorf("malformed DEFAULT_TOKENS block in %s", path)
	}
	block := s[start+open : start+end+1]
	matches := legacyTokenRe.FindAllStringSubmatch(block, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no tokens found in %s (expected DEFAULT_TOKENS dict)", path)
	}
	out := map[string]string{}
	for _, m := range matches {
		out[m[1]] = m[2]
	}
	return out, nil
}

func importOne(ctx context.Context, name, tok string) (int, error) {
	p := plexauth.New("https://plex.tv", "plexctl", nil)
	p.OnWarning = func(msg string) { fmt.Fprintln(os.Stderr, "warning:", msg) }
	u, err := p.User(ctx, tok)
	if err != nil {
		return 0, fmt.Errorf("plex user lookup: %w", err)
	}
	resources, err := p.Resources(ctx, tok)
	if err != nil {
		return 0, fmt.Errorf("plex resource discovery: %w", err)
	}
	c, err := config.Load(config.Path())
	if err != nil {
		return 0, err
	}
	key := "account/" + name
	profiles := make(map[string]config.ServerProfile)
	credentials := map[string]string{key: tok}
	currentServer := c.CurrentServer
	for i, r := range resources {
		conn, err := validatedConnection(ctx, r, tok)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Skipping %s: %v\n", r.Name, err)
			continue
		}
		if conn.URI == "" {
			continue
		}
		id := profileKey(name, r, i)
		tokenKey := key
		if r.AccessToken != "" {
			tokenKey = "server/" + name + "/" + id
			credentials[tokenKey] = r.AccessToken
		}
		normalized := normalizeDiscoveredConnection(conn)
		profiles[id] = config.ServerProfile{Account: name, Name: r.Name, MachineIdentifier: r.ClientIdentifier, TokenKey: tokenKey, URL: normalized.URL, InsecureTLS: normalized.InsecureTLS, Local: conn.Local, Relay: conn.Relay}
		if currentServer == "" {
			currentServer = id
		}
	}

	// Validation is complete before any persistent state changes. Credential
	// writes are journaled so a later write or config failure restores prior values.
	type prior struct {
		key, value string
		existed    bool
	}
	journal := make([]prior, 0, len(credentials))
	rollback := func() {
		for i := len(journal) - 1; i >= 0; i-- {
			p := journal[i]
			if p.existed {
				_ = authstore.Set(p.key, p.value)
			} else {
				_ = authstore.Delete(p.key)
			}
		}
	}
	for credentialKey, credential := range credentials {
		old, oldErr := authstore.Get(credentialKey)
		if oldErr == nil {
			journal = append(journal, prior{credentialKey, old, true})
		} else if errors.Is(oldErr, keyring.ErrNotFound) {
			journal = append(journal, prior{key: credentialKey})
		} else {
			rollback()
			return 0, fmt.Errorf("read prior credential %s: %w", credentialKey, oldErr)
		}
		if err := authstore.Set(credentialKey, credential); err != nil {
			rollback()
			return 0, fmt.Errorf("store Plex credential: %w", err)
		}
	}
	previous := c
	previous.Accounts = maps.Clone(c.Accounts)
	previous.ServersV2 = maps.Clone(c.ServersV2)
	c.Accounts[name] = config.Account{Username: u.Username, Email: u.Email, PlexID: u.ID, TokenKey: key}
	for id, profile := range profiles {
		c.ServersV2[id] = profile
	}
	c.CurrentAccount = name
	c.CurrentServer = currentServer
	if err := config.Save(config.Path(), c); err != nil {
		rollback()
		_ = config.Save(config.Path(), previous)
		return 0, fmt.Errorf("save imported configuration: %w", err)
	}
	return len(profiles), nil
}
