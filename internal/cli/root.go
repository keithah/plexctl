package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/authstore"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/connectioncache"
	"github.com/keithah/plexctl/internal/containeraudit"
	"github.com/keithah/plexctl/internal/health"
	"github.com/keithah/plexctl/internal/historyreport"
	"github.com/keithah/plexctl/internal/libraryintegrity"
	"github.com/keithah/plexctl/internal/librarymaintenance"
	"github.com/keithah/plexctl/internal/maintenancestatus"
	"github.com/keithah/plexctl/internal/monitor"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/keithah/plexctl/internal/pms"
	"github.com/keithah/plexctl/internal/sessiondiagnostics"
	"github.com/spf13/cobra"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type options struct {
	server  string
	jsonOut bool
	timeout time.Duration
}

func NewRoot() *cobra.Command {
	o := &options{timeout: 30 * time.Second}
	root := &cobra.Command{Use: "plexctl", Short: "Unofficial Plex Media Server CLI", SilenceUsage: true}
	root.PersistentFlags().StringVar(&o.server, "server", "", "configured server name")
	root.PersistentFlags().BoolVar(&o.jsonOut, "json", false, "print JSON")
	root.PersistentFlags().DurationVar(&o.timeout, "timeout", o.timeout, "request timeout")
	root.AddCommand(configCmd(), authCmd(), accountsCmd(), serversCmd(), serverCmd(o), libraryCmd(o), metadataCmd(o), sessionsCmd(o), historyCmd(o), playlistsCmd(o), collectionsCmd(o), queuesCmd(o), transcodeCmd(o), healthCmd(o), serveCmd(o), sharingCmd(o), apiCmd(o))
	return root
}
func Execute() {
	if e := NewRoot().Execute(); e != nil {
		fmt.Fprintln(os.Stderr, "error:", e)
		os.Exit(1)
	}
}
func configured(o *options) (*pms.Client, error) {
	c, e := config.Load(config.Path())
	if e != nil {
		return nil, e
	}
	var s config.Server
	var token string
	if len(c.ServersV2) > 0 && (o.server != "" || c.CurrentServer != "") {
		name := o.server
		if name == "" {
			name = c.CurrentServer
		}
		p, ok := c.ServersV2[name]
		if ok {
			if a, ok := c.Accounts[p.Account]; ok {
				key := p.TokenKey
				if key == "" {
					key = a.TokenKey
				}
				token, e = authstore.Get(key)
			} else {
				e = fmt.Errorf("account %q is not configured", p.Account)
			}
			if e != nil {
				return nil, e
			}
			s = config.Server{URL: p.URL, InsecureTLS: p.InsecureTLS}
		} else {
			_, s, e = c.Resolve(name)
			if e != nil {
				return nil, e
			}
			token, e = tokenFromEnv(s)
			if e != nil {
				return nil, e
			}
		}
	} else {
		_, s, e = c.Resolve(o.server)
		if e != nil {
			return nil, e
		}
		token, e = tokenFromEnv(s)
		if e != nil {
			return nil, e
		}
	}
	return newPMSClient(s, token)
}

func tokenFromEnv(s config.Server) (string, error) {
	token := os.Getenv(s.TokenEnv)
	if s.TokenEnv != "" && token == "" {
		return "", fmt.Errorf("token environment variable %q is not set", s.TokenEnv)
	}
	return token, nil
}

func newPMSClient(s config.Server, token string) (*pms.Client, error) {
	a, e := api.New(s.URL, token, nil)
	if e != nil {
		return nil, e
	}
	a.SetInsecureTLS(s.InsecureTLS)
	return pms.New(a), nil
}
func commandContext(o *options) (context.Context, context.CancelFunc) {
	if o.timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), o.timeout)
}
func printValue(v any, jsonOut bool) {
	if jsonOut {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal output: %v\n", err)
			fmt.Printf("%+v\n", v)
			return
		}
		fmt.Println(string(b))
		return
	}
	fmt.Printf("%+v\n", v)
}
func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage configured Plex servers"}
	cmd.AddCommand(&cobra.Command{Use: "init", RunE: func(*cobra.Command, []string) error {
		p := config.Path()
		c, e := config.Load(p)
		if e != nil {
			return e
		}
		if e = config.Save(p, c); e != nil {
			return e
		}
		fmt.Println(p)
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		names := make([]string, 0, len(c.Servers))
		for n := range c.Servers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Printf("%s	%s\n", n, c.Servers[n].URL)
		}
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "set NAME URL TOKEN_ENV", Args: cobra.ExactArgs(3), RunE: func(_ *cobra.Command, a []string) error {
		p := config.Path()
		c, e := config.Load(p)
		if e != nil {
			return e
		}
		c.Servers[a[0]] = config.Server{URL: a[1], TokenEnv: a[2]}
		if c.Current == "" {
			c.Current = a[0]
		}
		return config.Save(p, c)
	}})
	cmd.AddCommand(&cobra.Command{Use: "use NAME", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		p := config.Path()
		c, e := config.Load(p)
		if e != nil {
			return e
		}
		if _, ok := c.Servers[a[0]]; !ok {
			return fmt.Errorf("server %q is not configured", a[0])
		}
		c.Current = a[0]
		return config.Save(p, c)
	}})
	return cmd
}

func authCmd() *cobra.Command {
	var accountName string
	cmd := &cobra.Command{Use: "auth", Short: "Authenticate Plex accounts"}
	login := &cobra.Command{Use: "login", Short: "Authenticate an account and discover its servers", RunE: func(*cobra.Command, []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		p := plexauth.New("https://plex.tv", "plexctl", nil)
		p.OnWarning = func(msg string) { fmt.Fprintln(os.Stderr, "warning:", msg) }
		p.OnPIN = func(link string) {
			fmt.Printf("Open %s to authorize plexctl.\n", link)
			if runtime.GOOS == "darwin" {
				_ = exec.Command("open", link).Start()
			}
		}
		result, err := p.Login(ctx)
		if err != nil {
			return err
		}
		u, err := p.User(ctx, result.Token)
		if err != nil {
			return err
		}
		name := accountName
		if name == "" {
			name = u.Username
		}
		if name == "" {
			return errors.New("plex account has no username; provide --name")
		}
		resources, err := p.Resources(ctx, result.Token)
		if err != nil {
			return err
		}
		c, err := config.Load(config.Path())
		if err != nil {
			return err
		}
		key := "account/" + name
		if err := authstore.Set(key, result.Token); err != nil {
			return fmt.Errorf("store Plex token: %w", err)
		}
		c.Accounts[name] = config.Account{Username: u.Username, Email: u.Email, PlexID: u.ID, TokenKey: key}
		savedServers := 0
		for i, r := range resources {
			conn, err := validatedConnection(ctx, r, result.Token)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Skipping %s: %v\n", r.Name, err)
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
					return fmt.Errorf("store Plex server token: %w", err)
				}
			}
			normalized := normalizeDiscoveredConnection(conn)
			c.ServersV2[id] = config.ServerProfile{Account: name, Name: r.Name, MachineIdentifier: r.ClientIdentifier, TokenKey: tokenKey, URL: normalized.URL, InsecureTLS: normalized.InsecureTLS, Local: conn.Local, Relay: conn.Relay}
			if c.CurrentServer == "" {
				c.CurrentServer = id
			}
			savedServers++
		}
		if c.CurrentAccount == "" {
			c.CurrentAccount = name
		}
		if err := config.Save(config.Path(), c); err != nil {
			return err
		}
		fmt.Printf("Authenticated %s; discovered %d Plex servers.\n", name, savedServers)
		return nil
	}}
	login.Flags().StringVar(&accountName, "name", "", "local account name (defaults to Plex username)")
	cmd.AddCommand(login)
	cmd.AddCommand(importCmd())
	cmd.AddCommand(&cobra.Command{Use: "logout ACCOUNT", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		ac, ok := c.Accounts[a[0]]
		if !ok {
			return fmt.Errorf("account %q is not configured", a[0])
		}
		var errs []string
		if err := authstore.Delete(ac.TokenKey); err != nil {
			errs = append(errs, fmt.Sprintf("account token %q: %v", ac.TokenKey, err))
		} else {
			delete(c.Accounts, a[0])
		}
		for id, s := range c.ServersV2 {
			if s.Account == a[0] {
				if s.TokenKey != "" && s.TokenKey != ac.TokenKey {
					if err := authstore.Delete(s.TokenKey); err != nil {
						errs = append(errs, fmt.Sprintf("server %q token %q: %v", id, s.TokenKey, err))
						continue
					}
				}
				delete(c.ServersV2, id)
			}
		}
		if len(errs) > 0 {
			// Persist partial deletions so remaining profiles reflect what was actually removed,
			// but surface the failure instead of reporting success with orphaned credentials.
			_ = config.Save(config.Path(), c)
			return fmt.Errorf("logout partially failed: %s", strings.Join(errs, "; "))
		}
		if c.CurrentAccount == a[0] {
			c.CurrentAccount = ""
			c.CurrentServer = ""
		}
		return config.Save(config.Path(), c)
	}})
	return cmd
}

type normalizedConnection struct {
	URL         string
	InsecureTLS bool
}

func normalizeDiscoveredConnection(conn plexauth.Connection) normalizedConnection {
	normalizedURL := conn.URI
	insecureTLS := false
	if !conn.Local && !conn.Relay {
		if strings.HasPrefix(normalizedURL, "http://") {
			normalizedURL = "https://" + strings.TrimPrefix(normalizedURL, "http://")
		}
		// A certificate can never match a bare IP literal, so verification must be
		// disabled for IP-literal remote endpoints regardless of the discovered
		// scheme. Hostname endpoints (including *.plex.direct) keep verification.
		if strings.HasPrefix(normalizedURL, "https://") {
			if parsed, err := url.Parse(normalizedURL); err == nil {
				insecureTLS = net.ParseIP(parsed.Hostname()) != nil
			}
		}
	}
	return normalizedConnection{URL: normalizedURL, InsecureTLS: insecureTLS}
}

func validatedConnection(ctx context.Context, resource plexauth.Resource, accountToken string) (plexauth.Connection, error) {
	candidates := append([]plexauth.Connection(nil), resource.Connections...)
	ordered := orderedConnections(candidates)
	if len(ordered) == 0 {
		return plexauth.Connection{}, fmt.Errorf("no connections discovered")
	}
	token := resource.AccessToken
	if token == "" {
		token = accountToken
	}
	for _, candidate := range ordered {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		normalized := normalizeDiscoveredConnection(candidate)
		a, err := api.New(normalized.URL, token, nil)
		if err != nil {
			cancel()
			continue
		}
		a.SetInsecureTLS(normalized.InsecureTLS)
		identity, err := pms.New(a).Identity(probeCtx)
		cancel()
		if err == nil && identity.MediaContainer.MachineIdentifier == resource.ClientIdentifier {
			return candidate, nil
		}
	}
	return plexauth.Connection{}, fmt.Errorf("no reachable connection matched machine identifier")
}

// orderedConnections ranks every candidate by the documented preference:
// local direct, then remote direct, then relay. Ranking the whole slice (rather
// than only picking a single preferred entry) keeps the fallback chain in
// preference order, so a relay is never probed before an untried direct
// connection. Order within a tier is preserved as discovered.
func orderedConnections(conns []plexauth.Connection) []plexauth.Connection {
	tiers := [3][]plexauth.Connection{}
	for _, c := range conns {
		if c.URI == "" {
			continue
		}
		switch {
		case c.Relay:
			tiers[2] = append(tiers[2], c)
		case c.Local:
			tiers[0] = append(tiers[0], c)
		default:
			tiers[1] = append(tiers[1], c)
		}
	}
	ordered := make([]plexauth.Connection, 0, len(conns))
	for _, tier := range tiers {
		ordered = append(ordered, tier...)
	}
	return ordered
}

// profileKey identifies a discovered server profile. Plex's machine identifier
// is used whenever it is present. The fallback must not depend on discovery
// order, because a reordered resources response would otherwise overwrite an
// unrelated profile on the next login.
func profileKey(account string, r plexauth.Resource, _ int) string {
	if r.ClientIdentifier != "" {
		return r.ClientIdentifier
	}
	seed := account + "\x00" + r.Name
	for _, c := range r.Connections {
		seed += "\x00" + c.URI
	}
	sum := sha256.Sum256([]byte(seed))
	return fmt.Sprintf("%s-%s", account, hex.EncodeToString(sum[:])[:12])
}

func printAccounts(c config.Config) {
	names := make([]string, 0, len(c.Accounts))
	for name := range c.Accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := c.Accounts[name]
		mark := ""
		if name == c.CurrentAccount {
			mark = " *"
		}
		fmt.Printf("%s\t%s%s\n", name, a.Email, mark)
	}
}

func accountsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "accounts", Short: "List and select authenticated Plex accounts"}
	cmd.RunE = func(*cobra.Command, []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		printAccounts(c)
		return nil
	}
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		printAccounts(c)
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "use ACCOUNT", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		if _, ok := c.Accounts[a[0]]; !ok {
			return fmt.Errorf("account %q is not configured", a[0])
		}
		c.CurrentAccount = a[0]
		var ids []string
		for id, s := range c.ServersV2 {
			if s.Account == a[0] {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			c.CurrentServer = ids[0]
		}
		return config.Save(config.Path(), c)
	}})
	return cmd
}
func printServers(c config.Config) {
	ids := make([]string, 0, len(c.ServersV2))
	for id := range c.ServersV2 {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := c.ServersV2[id]
		mark := ""
		if id == c.CurrentServer {
			mark = " *"
		}
		fmt.Printf("%s\t%s\t%s\taccount=%s%s\n", id, s.Name, s.URL, s.Account, mark)
	}
}

func serversCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "servers", Short: "List and select discovered Plex servers"}
	cmd.RunE = func(*cobra.Command, []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		printServers(c)
		return nil
	}
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		printServers(c)
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "use SERVER", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		s, ok := c.ServersV2[a[0]]
		if !ok {
			return fmt.Errorf("server %q is not configured", a[0])
		}
		c.CurrentServer = a[0]
		c.CurrentAccount = s.Account
		return config.Save(config.Path(), c)
	}})
	return cmd
}

func serverCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "server"}
	cmd.AddCommand(&cobra.Command{Use: "info", Short: "Show server configuration and capabilities", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Info(ctx)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}})
	cmd.AddCommand(serverMaintenanceCmd(o))
	cmd.AddCommand(&cobra.Command{Use: "identity", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Identity(ctx)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}})
	return cmd
}
func libraryCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "library"}
	var searchLimit, recentLimit int
	var section string
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Sections(ctx)
		if e == nil {
			if o.jsonOut {
				printValue(v, o.jsonOut)
			} else {
				for _, d := range v.MediaContainer.Directory {
					fmt.Printf("%s\t%s\t%s\n", d.Key, d.Type, d.Title)
				}
			}
		}
		return e
	}})
	var itemSort string
	var itemLimit int
	items := &cobra.Command{Use: "items SECTION_KEY", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		q := url.Values{}
		if itemSort != "" {
			q.Set("sort", itemSort)
		}
		if itemLimit > 0 {
			q.Set("limit", fmt.Sprint(itemLimit))
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Items(ctx, a[0], q)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}}
	items.Flags().StringVar(&itemSort, "sort", "", "sort expression, for example titleSort:asc")
	items.Flags().IntVar(&itemLimit, "limit", 0, "maximum number of items")
	cmd.AddCommand(items)
	search := &cobra.Command{Use: "search TERM", Short: "Search libraries via the documented hubs search endpoint", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Search(ctx, section, a[0], searchLimit)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}}
	search.Flags().StringVar(&section, "section", "", "restrict the search to one library section key")
	search.Flags().IntVar(&searchLimit, "limit", 20, "maximum number of items")
	cmd.AddCommand(search)
	recent := &cobra.Command{Use: "recently-added SECTION_KEY", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.RecentlyAdded(ctx, a[0], recentLimit)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}}
	recent.Flags().IntVar(&recentLimit, "limit", 20, "maximum number of items")
	cmd.AddCommand(recent)
	cmd.AddCommand(libraryMaintenanceCmd(o), libraryIntegrityCmd(o))
	return cmd
}

func libraryMaintenanceCmd(o *options) *cobra.Command {
	var mode, section string
	cmd := &cobra.Command{Use: "maintenance", Short: "Preview read-only library maintenance candidates"}
	preview := &cobra.Command{Use: "preview", Short: "Preview library maintenance candidates", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("json") {
			return errors.New("library maintenance preview does not support --json")
		}
		if err := validateLibraryMaintenanceMode(mode); err != nil {
			return err
		}
		client, err := configured(o)
		if err != nil {
			return fmt.Errorf("configure library maintenance preview: %w", err)
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		sections, err := libraryMaintenanceSections(ctx, client, section)
		if err != nil {
			return err
		}
		rows, err := libraryMaintenanceCandidates(ctx, client, mode, sections)
		if err != nil {
			return err
		}
		printLibraryMaintenanceCandidates(mode, rows)
		return nil
	}}
	preview.Flags().StringVar(&mode, "mode", "", "preview mode: empty-collections, duplicates, missing-posters, or unmatched")
	preview.Flags().StringVar(&section, "section", "", "restrict to an exact library section key")
	cmd.AddCommand(preview)
	return cmd
}

func validateLibraryMaintenanceMode(mode string) error {
	switch mode {
	case "empty-collections", "duplicates", "missing-posters", "unmatched":
		return nil
	default:
		return errors.New("--mode must be one of empty-collections, duplicates, missing-posters, or unmatched")
	}
}

func libraryMaintenanceSections(ctx context.Context, client *pms.Client, selected string) ([]pms.Directory, error) {
	if selected != "" {
		// Resolve exactly the selected section so scoped output retains its required title
		// without listing unrelated sections.
		resolved, err := client.Section(ctx, selected)
		if err != nil {
			return nil, fmt.Errorf("get library section %q: %w", selected, err)
		}
		if resolved.MediaContainer.Title == "" {
			return nil, fmt.Errorf("get library section %q: missing title", selected)
		}
		return []pms.Directory{{Key: selected, Title: resolved.MediaContainer.Title, Type: resolved.MediaContainer.Type}}, nil
	}
	sections, err := client.Sections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list library sections: %w", err)
	}
	result := make([]pms.Directory, 0, len(sections.MediaContainer.Directory))
	for _, candidate := range sections.MediaContainer.Directory {
		if libraryMaintenanceSectionType(candidate.Type) {
			result = append(result, candidate)
		}
	}
	return result, nil
}

// PMS uses these types for media-library sections; directories and unknown types
// are intentionally skipped during unscoped scans.
func libraryMaintenanceSectionType(kind string) bool {
	switch kind {
	case "movie", "show", "artist", "photo":
		return true
	default:
		return false
	}
}

func libraryMaintenanceCandidates(ctx context.Context, client *pms.Client, mode string, sections []pms.Directory) ([]librarymaintenance.Candidate, error) {
	if mode == "empty-collections" {
		collections := make([]librarymaintenance.Collection, 0)
		for _, section := range sections {
			listed, err := client.ListCollections(ctx, section.Key)
			if err != nil {
				return nil, fmt.Errorf("list collections in section %q: %w", section.Key, err)
			}
			if listed.MediaContainer.Size != len(listed.MediaContainer.Metadata) {
				return nil, fmt.Errorf("list collections in section %q: declared size %d but decoded %d collections", section.Key, listed.MediaContainer.Size, len(listed.MediaContainer.Metadata))
			}
			for _, collection := range listed.MediaContainer.Metadata {
				if collection.RatingKey == "" {
					return nil, fmt.Errorf("list collections in section %q: collection missing rating key", section.Key)
				}
				items, err := client.ListCollectionItems(ctx, collection.RatingKey)
				if err != nil {
					return nil, fmt.Errorf("list items in collection: %w", err)
				}
				if items.MediaContainer.Size != len(items.MediaContainer.Metadata) {
					return nil, fmt.Errorf("list items in collection: declared size %d but decoded %d items", items.MediaContainer.Size, len(items.MediaContainer.Metadata))
				}
				collections = append(collections, librarymaintenance.Collection{SectionKey: section.Key, SectionTitle: section.Title, RatingKey: collection.RatingKey, Title: collection.Title, ItemCount: len(items.MediaContainer.Metadata), ItemCountKnown: true})
			}
		}
		return librarymaintenance.EmptyCollections(collections), nil
	}

	items := make([]librarymaintenance.Item, 0)
	for _, section := range sections {
		listed, err := client.ListSectionItems(ctx, section.Key)
		if err != nil {
			return nil, fmt.Errorf("list items in section %q: %w", section.Key, err)
		}
		for _, item := range listed.MediaContainer.Metadata {
			value := libraryMaintenanceItem(section, item)
			if mode == "missing-posters" && librarymaintenance.IsNormalMedia(item.Type) && item.RatingKey != "" && item.Thumb != "" {
				if !pms.IsInternalThumbPath(item.Thumb) {
					return nil, errors.New("invalid thumbnail path")
				}
				value.Thumb = librarymaintenance.ThumbPresent
				if err := client.ProbeThumb(ctx, item.Thumb); err != nil {
					value.Probe = librarymaintenance.ProbeFailed
				} else {
					value.Probe = librarymaintenance.ProbeSucceeded
				}
			}
			items = append(items, value)
		}
	}
	switch mode {
	case "duplicates":
		return librarymaintenance.Duplicates(items), nil
	case "missing-posters":
		return librarymaintenance.MissingPosters(items), nil
	case "unmatched":
		return librarymaintenance.Unmatched(items), nil
	default:
		return nil, errors.New("invalid library maintenance mode")
	}
}

func libraryMaintenanceItem(section pms.Directory, item pms.Metadata) librarymaintenance.Item {
	guids := make([]string, 0, len(item.GUID))
	for _, guid := range item.GUID {
		guids = append(guids, guid.ID)
	}
	thumb := librarymaintenance.ThumbPresent
	if item.Thumb == "" {
		thumb = librarymaintenance.ThumbMissing
	}
	return librarymaintenance.Item{SectionKey: section.Key, SectionTitle: section.Title, RatingKey: item.RatingKey, Title: item.Title, MediaType: item.Type, Year: item.Year, Thumb: thumb, GUIDs: guids}
}

func libraryMaintenanceTSVField(value string) string {
	var escaped strings.Builder
	for _, r := range value {
		switch r {
		case 0x09:
			escaped.WriteRune(0x5c)
			escaped.WriteRune('t')
		case 0x0a:
			escaped.WriteRune(0x5c)
			escaped.WriteRune('n')
		case 0x0d:
			escaped.WriteRune(0x5c)
			escaped.WriteRune('r')
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&escaped, "\\u%04X", r)
				continue
			}
			escaped.WriteRune(r)
		}
	}
	return escaped.String()
}

func libraryMaintenanceTSVRow(fields ...string) string {
	for i := range fields {
		fields[i] = libraryMaintenanceTSVField(fields[i])
	}
	return strings.Join(fields, "\t")
}

func printLibraryMaintenanceCandidates(mode string, rows []librarymaintenance.Candidate) {
	switch mode {
	case "empty-collections":
		fmt.Println("section_key	section_title	collection_rating_key	collection_title")
		for _, row := range rows {
			fmt.Println(libraryMaintenanceTSVRow(row.SectionKey, row.SectionTitle, row.RatingKey, row.Title))
		}
	case "duplicates":
		fmt.Println("section_key	section_title	normalized_title	rating_key	title	media_type	year")
		for _, row := range rows {
			fmt.Println(libraryMaintenanceTSVRow(row.SectionKey, row.SectionTitle, row.GroupTitle, row.RatingKey, row.Title, row.MediaType, strconv.Itoa(row.Year)))
		}
	case "missing-posters":
		fmt.Println("section_key	section_title	rating_key	title	media_type	reason")
		for _, row := range rows {
			fmt.Println(libraryMaintenanceTSVRow(row.SectionKey, row.SectionTitle, row.RatingKey, row.Title, row.MediaType, row.Reason))
		}
	case "unmatched":
		fmt.Println("section_key	section_title	rating_key	title	media_type	year")
		for _, row := range rows {
			fmt.Println(libraryMaintenanceTSVRow(row.SectionKey, row.SectionTitle, row.RatingKey, row.Title, row.MediaType, strconv.Itoa(row.Year)))
		}
	}
}
func readOnlyAuditRejectJSON(cmd *cobra.Command) error {
	if cmd.Flags().Changed("json") {
		return errors.New("read-only audit reports do not support --json")
	}
	return nil
}

type readOnlyAuditFailure struct{ err error }

func (e *readOnlyAuditFailure) Error() string {
	var statusErr *api.HTTPError
	if errors.As(e.err, &statusErr) {
		return fmt.Sprintf("read-only audit request failed: HTTP %d: %s", statusErr.StatusCode, http.StatusText(statusErr.StatusCode))
	}
	return "read-only audit failed"
}

func (e *readOnlyAuditFailure) Unwrap() error { return e.err }

func readOnlyAuditError(err error) error {
	if err == nil {
		return nil
	}
	var existing *readOnlyAuditFailure
	if errors.As(err, &existing) {
		return err
	}
	return &readOnlyAuditFailure{err: err}
}

func readOnlyAuditCommand(cmd *cobra.Command) *cobra.Command {
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		return readOnlyAuditError(run(c, args))
	}
	return cmd
}

func libraryIntegrityCmd(o *options) *cobra.Command {
	var mode string
	cmd := &cobra.Command{Use: "integrity"}
	report := &cobra.Command{Use: "report", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := readOnlyAuditRejectJSON(cmd); err != nil {
			return err
		}
		if mode != "storage" && mode != "unavailable" && mode != "duplicates" && mode != "suspicious" {
			return errors.New("--mode must be one of storage, unavailable, duplicates, or suspicious")
		}
		client, err := configured(o)
		if err != nil {
			return err
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		items, err := libraryIntegrityItems(ctx, client)
		if err != nil {
			return err
		}
		if _, err := libraryintegrity.Storage(items); err != nil {
			return err
		}
		if mode == "unavailable" {
			probeFailed := false
			for i := range items {
				for j := range items[i].Media {
					for k := range items[i].Media[j].Parts {
						if err := client.ProbeMediaPart(ctx, items[i].Media[j].Parts[k].Reference); err != nil {
							items[i].Media[j].Parts[k].Probe = libraryintegrity.ProbeFailed
							probeFailed = true
						} else {
							items[i].Media[j].Parts[k].Probe = libraryintegrity.ProbeSucceeded
						}
					}
				}
			}
			if probeFailed {
				return errors.New("media part probe failed")
			}
		}
		switch mode {
		case "storage":
			rows, e := libraryintegrity.Storage(items)
			if e != nil {
				return e
			}
			printIntegrityStorage(rows)
		case "unavailable":
			rows, e := libraryintegrity.UnavailableParts(items)
			if e != nil {
				return e
			}
			printIntegrityCandidates(rows)
		case "duplicates":
			rows, e := libraryintegrity.DuplicateParts(items)
			if e != nil {
				return e
			}
			printIntegrityCandidates(rows)
		case "suspicious":
			rows, e := libraryintegrity.SuspiciousParts(items)
			if e != nil {
				return e
			}
			printIntegrityCandidates(rows)
		}
		return nil
	}}
	report.Flags().StringVar(&mode, "mode", "", "storage, unavailable, duplicates, or suspicious")
	cmd.AddCommand(readOnlyAuditCommand(report))
	return cmd
}
func libraryIntegrityItems(ctx context.Context, client *pms.Client) ([]libraryintegrity.Item, error) {
	sections, err := client.Sections(ctx)
	if err != nil {
		return nil, err
	}
	var out []libraryintegrity.Item
	for _, section := range sections.MediaContainer.Directory {
		if !libraryMaintenanceSectionType(section.Type) {
			continue
		}
		if strings.TrimSpace(section.Key) == "" {
			return nil, errors.New("library section has blank identifier")
		}
		listed, err := client.ListSectionItems(ctx, section.Key)
		if err != nil {
			return nil, err
		}
		for _, item := range listed.MediaContainer.Metadata {
			record := libraryintegrity.Item{Identity: libraryintegrity.Identity{SectionKey: section.Key, SectionTitle: section.Title, RatingKey: item.RatingKey, Title: item.Title}}
			for _, media := range item.Media {
				parts := make([]libraryintegrity.Part, 0, len(media.Part))
				for _, part := range media.Part {
					parts = append(parts, libraryintegrity.Part{Reference: part.Key, DeclaredBytes: part.Size})
				}
				record.Media = append(record.Media, libraryintegrity.Media{Parts: parts})
			}
			out = append(out, record)
		}
	}
	return out, nil
}
func printIntegrityStorage(rows []libraryintegrity.StorageSummary) {
	fmt.Println("section_key\tsection_title\tknown_part_count\tunknown_part_count\tknown_bytes")
	for _, row := range rows {
		fmt.Println(libraryMaintenanceTSVRow(row.SectionKey, row.SectionTitle, strconv.Itoa(row.KnownPartCount), strconv.Itoa(row.UnknownPartCount), strconv.FormatInt(row.KnownBytes, 10)))
	}
}
func printIntegrityCandidates(rows []libraryintegrity.Candidate) {
	fmt.Println("section_key\tsection_title\trating_key\ttitle\tpart_fingerprint\tstatus")
	for _, row := range rows {
		fmt.Println(libraryMaintenanceTSVRow(row.SectionKey, row.SectionTitle, row.RatingKey, row.Title, row.Fingerprint, string(row.Status)))
	}
}

func sessionsDiagnosticsCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "diagnostics", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := readOnlyAuditRejectJSON(cmd); err != nil {
			return err
		}
		client, err := configured(o)
		if err != nil {
			return err
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		listed, err := client.Sessions(ctx)
		if err != nil {
			return err
		}
		sessions := make([]sessiondiagnostics.Session, 0, len(listed.MediaContainer.Metadata))
		for _, s := range listed.MediaContainer.Metadata {
			d := sessiondiagnostics.DecisionUnknown
			if s.TranscodeSession.Key != "" {
				d = sessiondiagnostics.DecisionTranscode
			}
			sessions = append(sessions, sessiondiagnostics.Session{SessionID: s.Session.ID, Title: s.Title, GrandparentTitle: s.GrandparentTitle, ParentTitle: s.ParentTitle, UserID: s.User.ID, UserTitle: s.User.Title, ClientID: s.Player.MachineIdentifier, ClientTitle: s.Player.Title, ClientPlatform: s.Player.Platform, Decision: d})
		}
		report, err := sessiondiagnostics.Analyze(sessions)
		if err != nil {
			return err
		}
		printSessionDiagnostics(report)
		return nil
	}}
	return readOnlyAuditCommand(cmd)
}
func printSessionDiagnostics(r sessiondiagnostics.Report) {
	fmt.Println("session_id\ttitle\tgrandparent_title\tparent_title\tuser_id\tuser_title\tclient_id\tclient_title\tclient_platform\tdecision")
	for _, x := range r.Rows {
		fmt.Println(libraryMaintenanceTSVRow(x.SessionID, x.Title, x.GrandparentTitle, x.ParentTitle, x.UserID, x.UserTitle, x.ClientID, x.ClientTitle, x.ClientPlatform, string(x.Decision)))
	}
	fmt.Println()
	fmt.Println("decision\tcount")
	for _, x := range r.DecisionCounts {
		fmt.Println(libraryMaintenanceTSVRow(string(x.Decision), strconv.Itoa(x.Count)))
	}
}

func serverMaintenanceCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "maintenance"}
	status := &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := readOnlyAuditRejectJSON(c); err != nil {
			return err
		}
		client, err := configured(o)
		if err != nil {
			return err
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		a, err := client.Activities(ctx)
		if err != nil {
			return err
		}
		b, err := client.ButlerTasks(ctx)
		if err != nil {
			return err
		}
		u, err := client.UpdaterStatus(ctx)
		if err != nil {
			return err
		}
		s := maintenancestatus.Snapshot{Updater: maintenancestatus.Updater{CanInstall: u.MediaContainer.CanInstall, Version: u.MediaContainer.Version, ReleaseDate: u.MediaContainer.ReleaseDate}}
		for _, x := range a.MediaContainer.Activity {
			s.Activities = append(s.Activities, maintenancestatus.Activity{ID: x.UUID, Type: x.Type, Title: x.Title, Progress: x.Progress, Cancellable: x.Cancellable})
		}
		for _, x := range b.MediaContainer.ButlerTask {
			s.Tasks = append(s.Tasks, maintenancestatus.Task{ID: x.Name, Title: x.Title, Schedule: x.Schedule, Enabled: x.Enabled, Interval: x.Interval})
		}
		r, err := maintenancestatus.Analyze(s)
		if err != nil {
			return err
		}
		printMaintenanceStatus(r)
		return nil
	}}
	cmd.AddCommand(readOnlyAuditCommand(status))
	return cmd
}
func optionalAuditValue[T any](v *T) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(*v)
}
func printMaintenanceStatus(r maintenancestatus.Report) {
	fmt.Println("activity_id\ttype\ttitle\tprogress\tcancellable")
	for _, x := range r.Activities {
		fmt.Println(libraryMaintenanceTSVRow(x.ID, x.Type, x.Title, optionalAuditValue(x.Progress), optionalAuditValue(x.Cancellable)))
	}
	fmt.Println()
	fmt.Println("task_id\ttitle\tschedule\tenabled\tinterval")
	for _, x := range r.Tasks {
		fmt.Println(libraryMaintenanceTSVRow(x.ID, x.Title, x.Schedule, optionalAuditValue(x.Enabled), optionalAuditValue(x.Interval)))
	}
	fmt.Println()
	fmt.Println("can_install\tversion\trelease_date")
	fmt.Println(libraryMaintenanceTSVRow(optionalAuditValue(r.Updater.CanInstall), r.Updater.Version, r.Updater.ReleaseDate))
}

func playlistsAuditCmd(o *options) *cobra.Command {
	return containerAuditCommand(o, "audit", func(ctx context.Context, c *pms.Client) ([]containeraudit.Container, error) {
		listed, err := c.ListPlaylists(ctx)
		if err != nil {
			return nil, err
		}
		var out []containeraudit.Container
		for _, p := range listed.MediaContainer.Metadata {
			items, err := c.ListPlaylistItems(ctx, p.RatingKey)
			if err != nil {
				return nil, err
			}
			v := containeraudit.Container{ID: p.RatingKey, Title: p.Title, Kind: containeraudit.KindPlaylist, ItemsComplete: true}
			for _, x := range items.MediaContainer.Metadata {
				v.Items = append(v.Items, containeraudit.Item{RatingKey: x.RatingKey})
			}
			out = append(out, v)
		}
		return out, nil
	})
}
func collectionsAuditCmd(o *options) *cobra.Command {
	var section string
	cmd := containerAuditCommand(o, "audit", func(ctx context.Context, c *pms.Client) ([]containeraudit.Container, error) {
		listed, err := c.ListCollections(ctx, section)
		if err != nil {
			return nil, err
		}
		var out []containeraudit.Container
		for _, p := range listed.MediaContainer.Metadata {
			if strings.TrimSpace(p.RatingKey) == "" {
				return nil, errors.New("collection has blank identifier")
			}
			items, err := c.ListCollectionItems(ctx, p.RatingKey)
			if err != nil {
				return nil, err
			}
			v := containeraudit.Container{ID: p.RatingKey, Title: p.Title, Kind: containeraudit.KindCollection, ItemsComplete: true}
			for _, x := range items.MediaContainer.Metadata {
				v.Items = append(v.Items, containeraudit.Item{RatingKey: x.RatingKey})
			}
			out = append(out, v)
		}
		return out, nil
	})
	cmd.Flags().StringVar(&section, "section", "", "exact library section key")
	original := cmd.RunE
	cmd.RunE = func(c *cobra.Command, a []string) error {
		if strings.TrimSpace(section) == "" {
			return errors.New("--section is required")
		}
		return original(c, a)
	}
	return cmd
}
func containerAuditCommand(o *options, use string, list func(context.Context, *pms.Client) ([]containeraudit.Container, error)) *cobra.Command {
	cmd := &cobra.Command{Use: use, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := readOnlyAuditRejectJSON(cmd); err != nil {
			return err
		}
		client, err := configured(o)
		if err != nil {
			return err
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		containers, err := list(ctx, client)
		if err != nil {
			return err
		}
		r, err := containeraudit.Analyze(containers)
		if err != nil {
			return err
		}
		printContainerAudit(r)
		return nil
	}}
	return readOnlyAuditCommand(cmd)
}
func printContainerAudit(r containeraudit.Report) {
	fmt.Println("container_id\ttitle\tkind\titems_complete\tempty\titem_rating_keys")
	for _, x := range r.Candidates {
		fmt.Println(libraryMaintenanceTSVRow(x.ID, x.Title, string(x.Kind), strconv.FormatBool(x.ItemsComplete), strconv.FormatBool(x.Empty), strings.Join(x.ItemRatingKeys, ",")))
	}
}

func metadataCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "metadata"}
	cmd.AddCommand(&cobra.Command{Use: "get RATING_KEY", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Metadata(ctx, a[0])
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}})
	cmd.AddCommand(&cobra.Command{Use: "children RATING_KEY", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Children(ctx, a[0])
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}})
	return cmd
}
func sessionsCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "sessions"}
	cmd.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Sessions(ctx)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}})
	var accountID, viewedAt, librarySectionID, metadataItemID, sortExpr string
	history := &cobra.Command{Use: "history", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		q := url.Values{}
		for key, value := range map[string]string{"accountID": accountID, "viewedAt": viewedAt, "librarySectionID": librarySectionID, "metadataItemID": metadataItemID, "sort": sortExpr} {
			if value != "" {
				q.Set(key, value)
			}
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.History(ctx, q)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}}
	history.Flags().StringVar(&accountID, "account-id", "", "filter by Plex account ID")
	history.Flags().StringVar(&viewedAt, "viewed-at", "", "filter by viewed-at timestamp")
	history.Flags().StringVar(&librarySectionID, "section-id", "", "filter by library section ID")
	history.Flags().StringVar(&metadataItemID, "metadata-id", "", "filter by metadata item ID")
	history.Flags().StringVar(&sortExpr, "sort", "", "sort expression, for example viewedAt:desc")
	cmd.AddCommand(history, sessionsDiagnosticsCmd(o))
	return cmd
}

// historyReportNow is a command-local seam for deterministic inactive cutoffs.
var historyReportNow = time.Now

func historyCmd(o *options) *cobra.Command {
	var mode, section, accountID, olderThan, output string
	cmd := &cobra.Command{Use: "history", Short: "Analyze Plex watch history"}
	report := &cobra.Command{Use: "report", Short: "Generate a read-only watch-history report", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("json") {
			return errors.New("history report does not support --json")
		}
		if err := validateHistoryReport(mode, olderThan, output); err != nil {
			return err
		}

		client, err := configured(o)
		if err != nil {
			return fmt.Errorf("configure history report: %w", err)
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		query := url.Values{}
		if accountID != "" {
			query.Set("accountID", accountID)
		}
		if section != "" {
			query.Set("librarySectionID", section)
		}
		history, err := client.HistoryAll(ctx, query)
		if err != nil {
			return fmt.Errorf("read watch history: %w", err)
		}
		views := historyReportViews(history.MediaContainer.Metadata)

		switch mode {
		case "export":
			if err := historyreport.Export(output, views); err != nil {
				return fmt.Errorf("write history export: %w", err)
			}
			return nil
		case "summary":
			printHistorySummaries(historyreport.SummarizeViews(views))
			return nil
		}

		sections, err := client.Sections(ctx)
		if err != nil {
			return fmt.Errorf("list library sections: %w", err)
		}
		items, err := historyReportItems(ctx, client, sections.MediaContainer.Directory, section)
		if err != nil {
			return err
		}
		if mode == "unwatched" {
			printHistoryItems(historyreport.UnwatchedItems(items, views))
			return nil
		}
		duration, _ := time.ParseDuration(olderThan)
		printInactiveHistoryItems(historyreport.InactiveItems(items, views, historyReportNow().Add(-duration)))
		return nil
	}}
	report.Flags().StringVar(&mode, "mode", "", "report mode: export, summary, unwatched, or inactive")
	report.Flags().StringVar(&section, "section", "", "restrict to an exact library section key")
	report.Flags().StringVar(&accountID, "account-id", "", "filter by Plex account ID")
	report.Flags().StringVar(&olderThan, "older-than", "", "inactive cutoff duration")
	report.Flags().StringVar(&output, "output", "", "append export to .csv or .jsonl")
	cmd.AddCommand(report)
	return cmd
}

func validateHistoryReport(mode, olderThan, output string) error {
	switch mode {
	case "export", "summary", "unwatched", "inactive":
	default:
		return fmt.Errorf("--mode must be one of export, summary, unwatched, or inactive")
	}
	if mode == "export" && output == "" {
		return errors.New("--output is required for --mode export")
	}
	if mode != "export" && output != "" {
		return errors.New("--output is supported only for --mode export")
	}
	if output != "" && !strings.HasSuffix(output, ".csv") && !strings.HasSuffix(output, ".jsonl") {
		return fmt.Errorf("unsupported export output extension for %q: use .csv or .jsonl", output)
	}
	if olderThan != "" && mode != "inactive" {
		return errors.New("--older-than is supported only for --mode inactive")
	}
	if mode == "inactive" {
		duration, err := time.ParseDuration(olderThan)
		if err != nil || duration <= 0 {
			return errors.New("--older-than must be a positive duration for --mode inactive")
		}
	}
	return nil
}

func historyReportViews(metadata []pms.Metadata) []historyreport.View {
	source := make([]historyreport.SourceView, 0, len(metadata))
	for _, item := range metadata {
		value := historyreport.SourceView{RatingKey: item.RatingKey, Title: item.Title, ParentTitle: item.ParentTitle, GrandparentTitle: item.GrandparentTitle, MediaType: item.Type, SectionID: item.LibrarySectionID, SectionTitle: item.LibrarySectionTitle, AccountTitle: item.AccountTitle}
		if item.AccountID != 0 {
			value.AccountID = strconv.FormatInt(item.AccountID, 10)
		}
		if item.ViewedAt != nil {
			value.ViewedAt = time.Unix(*item.ViewedAt, 0).UTC()
		}
		if item.Duration != nil {
			duration := time.Duration(*item.Duration) * time.Millisecond
			value.Duration = &duration
		}
		source = append(source, value)
	}
	return historyreport.NormalizeViews(source)
}

func historyReportItems(ctx context.Context, client *pms.Client, sections []pms.Directory, selected string) ([]historyreport.LibraryItem, error) {
	var result []historyreport.LibraryItem
	found := selected == ""
	for _, section := range sections {
		if selected != "" && section.Key != selected {
			continue
		}
		found = true
		page, err := client.ListSectionItems(ctx, section.Key)
		if err != nil {
			return nil, fmt.Errorf("list items in section %q: %w", section.Key, err)
		}
		for _, item := range page.MediaContainer.Metadata {
			result = append(result, historyreport.LibraryItem{RatingKey: item.RatingKey, Title: item.Title, SectionID: section.Key, SectionTitle: section.Title, MediaType: item.Type})
		}
	}
	if !found {
		return nil, fmt.Errorf("library section %q was not found", selected)
	}
	return result, nil
}

func printHistorySummaries(rows []historyreport.Summary) {
	fmt.Println("account_id	account_title	section_id	section_title	view_count	first_viewed_at	last_viewed_at	total_duration")
	for _, row := range rows {
		duration := ""
		if row.TotalDuration != nil {
			duration = row.TotalDuration.String()
		}
		fmt.Printf("%s	%s	%s	%s	%d	%s	%s	%s\n", row.AccountID, row.AccountTitle, row.SectionID, row.SectionTitle, row.ViewCount, historyReportTime(row.FirstViewedAt), historyReportTime(row.LastViewedAt), duration)
	}
}

func printHistoryItems(rows []historyreport.LibraryItem) {
	fmt.Println("rating_key	title	section_id	section_title	media_type")
	for _, row := range rows {
		fmt.Printf("%s	%s	%s	%s	%s\n", row.RatingKey, row.Title, row.SectionID, row.SectionTitle, row.MediaType)
	}
}

func printInactiveHistoryItems(rows []historyreport.InactiveItem) {
	fmt.Println("rating_key	title	section_id	section_title	media_type	last_viewed_at")
	for _, row := range rows {
		fmt.Printf("%s	%s	%s	%s	%s	%s\n", row.RatingKey, row.Title, row.SectionID, row.SectionTitle, row.MediaType, historyReportTime(row.LastViewedAt))
	}
}

func historyReportTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func playlistsCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "playlists"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List playlists", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		v, e := c.Playlists(ctx)
		if e == nil {
			printValue(v, o.jsonOut)
		}
		return e
	}})
	for _, spec := range []struct {
		use, short string
		run        func(*pms.Client, context.Context, string) (any, error)
	}{
		{"get PLAYLIST_ID", "Get a playlist", func(c *pms.Client, ctx context.Context, id string) (any, error) { return c.Playlist(ctx, id) }},
		{"items PLAYLIST_ID", "List playlist items", func(c *pms.Client, ctx context.Context, id string) (any, error) { return c.PlaylistItems(ctx, id) }},
	} {
		s := spec
		cmd.AddCommand(&cobra.Command{Use: s.use, Short: s.short, Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			c, e := configured(o)
			if e != nil {
				return e
			}
			ctx, cancel := commandContext(o)
			defer cancel()
			v, e := s.run(c, ctx, a[0])
			if e == nil {
				printValue(v, o.jsonOut)
			}
			return e
		}})
	}
	cmd.AddCommand(playlistsAuditCmd(o))
	return cmd
}
func collectionsCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "collections"}
	for _, spec := range []struct {
		use, short string
		run        func(*pms.Client, context.Context, string) (any, error)
	}{
		{"list SECTION_ID", "List collections in a library section", func(c *pms.Client, ctx context.Context, id string) (any, error) { return c.Collections(ctx, id) }},
		{"items COLLECTION_ID", "List items in a collection", func(c *pms.Client, ctx context.Context, id string) (any, error) { return c.CollectionItems(ctx, id) }},
	} {
		s := spec
		cmd.AddCommand(&cobra.Command{Use: s.use, Short: s.short, Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			c, e := configured(o)
			if e != nil {
				return e
			}
			ctx, cancel := commandContext(o)
			defer cancel()
			v, e := s.run(c, ctx, a[0])
			if e == nil {
				printValue(v, o.jsonOut)
			}
			return e
		}})
	}
	cmd.AddCommand(collectionsAuditCmd(o))
	return cmd
}
func queuesCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "download-queues"}
	for _, spec := range []struct {
		use, short string
		run        func(*pms.Client, context.Context, []string) (any, error)
	}{
		{"get QUEUE_ID", "Get a download queue", func(c *pms.Client, x context.Context, a []string) (any, error) { return c.DownloadQueue(x, a[0]) }},
		{"items QUEUE_ID", "List download queue items", func(c *pms.Client, x context.Context, a []string) (any, error) { return c.DownloadQueueItems(x, a[0]) }},
		{"item QUEUE_ID ITEM_ID", "Get one download queue item", func(c *pms.Client, x context.Context, a []string) (any, error) {
			return c.DownloadQueueItem(x, a[0], a[1])
		}},
		{"decision QUEUE_ID ITEM_ID", "Get a queue item decision", func(c *pms.Client, x context.Context, a []string) (any, error) {
			return c.DownloadQueueDecision(x, a[0], a[1])
		}},
	} {
		s := spec
		n := len(strings.Fields(s.use)) - 1
		cmd.AddCommand(&cobra.Command{Use: s.use, Short: s.short, Args: cobra.ExactArgs(n), RunE: func(_ *cobra.Command, a []string) error {
			c, e := configured(o)
			if e != nil {
				return e
			}
			x, cancel := commandContext(o)
			defer cancel()
			v, e := s.run(c, x, a)
			if e == nil {
				printValue(v, o.jsonOut)
			}
			return e
		}})
	}
	return cmd
}
func transcodeCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "transcode"}
	for _, name := range []string{"decision", "subtitles"} {
		n := name
		var params []string
		c := &cobra.Command{Use: n + " TYPE SESSION_ID", Short: "Read universal transcode " + n, Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, a []string) error {
			q := url.Values{}
			for _, p := range params {
				k, v, ok := strings.Cut(p, "=")
				if !ok || k == "" {
					return fmt.Errorf("parameter must be key=value: %q", p)
				}
				q.Add(k, v)
			}
			client, e := configured(o)
			if e != nil {
				return e
			}
			x, cancel := commandContext(o)
			defer cancel()
			if n == "decision" {
				v, e := client.TranscodeDecision(x, a[0], a[1], q)
				if e == nil {
					printValue(v, o.jsonOut)
				}
				return e
			}
			v, e := client.TranscodeSubtitles(x, a[0], a[1], q)
			if e == nil && v != "" {
				// Subtitles are WebVTT text, so print verbatim rather than
				// through the JSON/struct formatter.
				fmt.Println(strings.TrimRight(v, "\n"))
			}
			return e
		}}
		c.Flags().StringArrayVar(&params, "param", nil, "transcode query parameter key=value (repeatable)")
		cmd.AddCommand(c)
	}
	return cmd
}
func healthCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "health"}
	cmd.AddCommand(&cobra.Command{Use: "ping", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		r := health.Ping(ctx, c)
		printValue(r, o.jsonOut)
		if !r.OK {
			return fmt.Errorf("health check failed: %s", r.Detail)
		}
		return nil
	}})
	cmd.AddCommand(&cobra.Command{Use: "check", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		r := health.Check(ctx, c)
		printValue(r, o.jsonOut)
		if !r.OK {
			return fmt.Errorf("health check failed: %s", r.Detail)
		}
		return nil
	}})
	return cmd
}

const plexResourceCacheTTL = 10 * time.Minute

func serveCmd(o *options) *cobra.Command {
	var listen string
	resources := plexauth.NewResourceCache()
	connections := connectioncache.New(connectioncache.Path())
	cmd := &cobra.Command{Use: "serve", Short: "Serve HTTP health endpoints for Uptime Kuma", RunE: func(*cobra.Command, []string) error {
		h := monitor.Handler{Timeout: o.timeout, Resolve: func(account, server string) (*pms.Client, error) {
			return resolveServeTargetCached(o, account, server, resources, connections)
		}}
		s := &http.Server{Addr: listen, Handler: h}
		fmt.Fprintf(os.Stderr, "plexctl monitoring adapter listening on %s\n", listen)
		return s.ListenAndServe()
	}}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:3002", "HTTP listen address")
	return cmd
}

func resolveServeTarget(o *options, account, server string) (*pms.Client, error) {
	return resolveServeTargetCached(o, account, server, nil, nil)
}

func resolveServeTargetCached(o *options, account, server string, resources *plexauth.ResourceCache, connections *connectioncache.Store) (*pms.Client, error) {
	c, err := config.Load(config.Path())
	if err != nil {
		return nil, err
	}
	// Profiles provide stable identity and account binding only. Their URL is
	// deliberately ignored: Plex.tv may advertise a different connection later.
	var profile config.ServerProfile
	if p, ok := c.ServersV2[server]; ok {
		if p.Account != account {
			return nil, fmt.Errorf("server %q belongs to account %q", server, p.Account)
		}
		profile = p
	} else {
		var candidates []string
		for id, prof := range c.ServersV2 {
			if prof.Account == account && strings.EqualFold(prof.Name, server) {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			for id, prof := range c.ServersV2 {
				if prof.Account == account && strings.EqualFold(id, server) {
					candidates = append(candidates, id)
				}
			}
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("server %q is not configured", server)
		}
		sort.Strings(candidates)
		if len(candidates) > 1 {
			return nil, fmt.Errorf("server %q matches multiple profiles: %s", server, strings.Join(candidates, ", "))
		}
		profile = c.ServersV2[candidates[0]]
	}
	return resolveFreshServeTarget(o, c, account, server, profile, resources, connections)
}

func cachedServeToken(profile config.ServerProfile, accountToken string) (string, error) {
	if profile.TokenKey == "" {
		return accountToken, nil
	}
	if token, err := authstore.Get(profile.TokenKey); err == nil && token != "" {
		return token, nil
	}
	return accountToken, nil
}

func resolveCachedServeTarget(ctx context.Context, connections *connectioncache.Store, account string, profile config.ServerProfile, accountToken string) (*pms.Client, bool, error) {
	if profile.MachineIdentifier == "" {
		return nil, false, nil
	}
	token, err := cachedServeToken(profile, accountToken)
	if err != nil {
		return nil, false, err
	}
	var candidates []plexauth.Connection
	if connections != nil {
		connection, ok, err := connections.Get(account, profile.MachineIdentifier)
		if err == nil && ok {
			candidates = append(candidates, connection)
		}
	}
	if profile.URL != "" {
		candidates = append(candidates, plexauth.Connection{URI: profile.URL, Local: profile.Local, Relay: profile.Relay})
	}
	for _, candidate := range candidates {
		validated, err := validatedConnection(ctx, plexauth.Resource{
			ClientIdentifier: profile.MachineIdentifier,
			Connections:      []plexauth.Connection{candidate},
		}, token)
		if err != nil {
			continue
		}
		normalized := normalizeDiscoveredConnection(validated)
		client, err := newPMSClient(config.Server{URL: normalized.URL, InsecureTLS: normalized.InsecureTLS}, token)
		if err != nil {
			return nil, false, err
		}
		if connections != nil {
			_ = connections.Put(account, profile.MachineIdentifier, validated)
		}
		return client, true, nil
	}
	return nil, false, nil
}

// resolveFreshServeTarget uses a previously validated endpoint whenever it is
// still the expected PMS. Plex.tv discovery occurs only after a cache miss or
// an endpoint validation failure, and a fresh discovery is persisted only once
// the advertised connection has passed the same identity check.
func resolveFreshServeTarget(o *options, c config.Config, account, requested string, profile config.ServerProfile, resourceCache *plexauth.ResourceCache, connections *connectioncache.Store) (*pms.Client, error) {
	a, ok := c.Accounts[account]
	if !ok {
		return nil, fmt.Errorf("account %q is not configured", account)
	}
	accountToken, err := authstore.Get(a.TokenKey)
	if err != nil {
		return nil, err
	}
	ctx, cancel := commandContext(o)
	defer cancel()
	if cached, ok, err := resolveCachedServeTarget(ctx, connections, account, profile, accountToken); err != nil {
		return nil, fmt.Errorf("read cached connection for %s/%s: %w", account, requested, err)
	} else if ok {
		return cached, nil
	}
	plex := plexauth.New("https://plex.tv", "plexctl", nil)
	resources, err := resourceCache.Resources(ctx, plex, accountToken, plexResourceCacheTTL)
	if err != nil {
		return nil, fmt.Errorf("refresh Plex connections for %s: %w", account, err)
	}
	var matches []plexauth.Resource
	for _, resource := range resources {
		if profile.MachineIdentifier != "" && resource.ClientIdentifier == profile.MachineIdentifier {
			matches = append(matches, resource)
		} else if profile.MachineIdentifier == "" && strings.EqualFold(resource.Name, requested) {
			matches = append(matches, resource)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("server %q is not currently advertised by Plex.tv", requested)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("server %q has ambiguous Plex.tv identity", requested)
	}
	connection, err := validatedConnection(ctx, matches[0], accountToken)
	if err != nil {
		return nil, fmt.Errorf("refresh connection for %s/%s: %w", account, requested, err)
	}
	normalized := normalizeDiscoveredConnection(connection)
	if connections != nil && profile.MachineIdentifier != "" {
		// The cache is an availability optimization. A successful live probe must
		// remain usable even if its optional persistence layer is unavailable.
		_ = connections.Put(account, profile.MachineIdentifier, connection)
	}
	token := matches[0].AccessToken
	if token == "" {
		token = accountToken
	}
	return newPMSClient(config.Server{URL: normalized.URL, InsecureTLS: normalized.InsecureTLS}, token)
}
func apiCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "api METHOD PATH", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, a []string) error {
		method := a[0]
		if method != "GET" && method != "HEAD" {
			return fmt.Errorf("raw API mutations require a typed command; %s rejected", method)
		}
		c, e := configured(o)
		if e != nil {
			return e
		}
		var out any
		ctx, cancel := commandContext(o)
		defer cancel()
		e = c.API.Do(ctx, method, a[1], url.Values{}, nil, &out)
		if e == nil {
			printValue(out, true)
		}
		return e
	}}
	return cmd
}
