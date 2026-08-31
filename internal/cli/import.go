package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/keithah/plexctl/internal/authstore"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/spf13/cobra"
)

func importCmd() *cobra.Command {
	var file string
	var account string
	var token string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import tokens from legacy plex-monitor setup",
		Long: `Import legacy Plex account tokens into plexctl.

Supports:
  --file PATH   Parse DEFAULT_TOKENS from a legacy plex_multi_account.py
  --account NAME --token TOKEN   Import a single account token
  --dry-run     Parse and validate without writing

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
			case token != "":
				if account == "" {
					return fmt.Errorf("--account is required with --token")
				}
				tokens = map[string]string{account: token}
			default:
				// Try env PLEX_TOKEN_* (legacy) and PLEXCTL_TOKENS_FILE companion
				tokens = tokensFromEnv()
				if len(tokens) == 0 {
					return fmt.Errorf("no tokens found: provide --file or --account/--token or set PLEX_TOKEN_*")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			for name, tok := range tokens {
				if strings.TrimSpace(tok) == "" {
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
					fmt.Fprintf(os.Stderr, "  warning: %s: %v\n", name, err)
					continue
				}
				fmt.Fprintf(os.Stderr, "  %s: discovered %d server(s)\n", name, count)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "path to legacy plex_multi_account.py")
	cmd.Flags().StringVar(&account, "account", "", "single account name (with --token)")
	cmd.Flags().StringVar(&token, "token", "", "single Plex token (with --account)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing")
	return cmd
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
	close := strings.Index(s[start:], "}")
	if open == -1 || close == -1 || close <= open {
		return nil, fmt.Errorf("malformed DEFAULT_TOKENS block in %s", path)
	}
	block := s[start+open : start+close+1]
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
	if err := authstore.Set(key, tok); err != nil {
		return 0, fmt.Errorf("store Plex token: %w", err)
	}
	c.Accounts[name] = config.Account{Username: u.Username, Email: u.Email, PlexID: u.ID, TokenKey: key}
	saved := 0
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
			if err := authstore.Set(tokenKey, r.AccessToken); err != nil {
				return saved, fmt.Errorf("store Plex server token: %w", err)
			}
		}
		normalized := normalizeDiscoveredConnection(conn)
		c.ServersV2[id] = config.ServerProfile{Account: name, Name: r.Name, MachineIdentifier: r.ClientIdentifier, TokenKey: tokenKey, URL: normalized.URL, InsecureTLS: normalized.InsecureTLS, Local: conn.Local, Relay: conn.Relay}
		if c.CurrentServer == "" {
			c.CurrentServer = id
		}
		saved++
	}
	if c.CurrentAccount == "" {
		c.CurrentAccount = name
	}
	if err := config.Save(config.Path(), c); err != nil {
		return saved, err
	}
	return saved, nil
}
