package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/authstore"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/health"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/keithah/plexctl/internal/pms"
	"github.com/spf13/cobra"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
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
	root.AddCommand(configCmd(), authCmd(), accountsCmd(), serversCmd(), serverCmd(o), libraryCmd(o), metadataCmd(o), sessionsCmd(o), playlistsCmd(o), collectionsCmd(o), queuesCmd(o), transcodeCmd(o), healthCmd(o), apiCmd(o))
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
			token = os.Getenv(s.TokenEnv)
			if s.TokenEnv != "" && token == "" {
				return nil, fmt.Errorf("token environment variable %q is not set", s.TokenEnv)
			}
		}
	} else {
		_, s, e = c.Resolve(o.server)
		if e != nil {
			return nil, e
		}
		token = os.Getenv(s.TokenEnv)
		if s.TokenEnv != "" && token == "" {
			return nil, fmt.Errorf("token environment variable %q is not set", s.TokenEnv)
		}
	}
	return newPMSClient(s, token)
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
		b, _ := json.MarshalIndent(v, "", "  ")
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
		for n, s := range c.Servers {
			fmt.Printf("%s\t%s\n", n, s.URL)
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
			return fmt.Errorf("Plex account has no username; provide --name")
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
			id := r.ClientIdentifier
			if id == "" {
				id = fmt.Sprintf("%s-%d", name, i)
			}
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
	cmd.AddCommand(&cobra.Command{Use: "logout ACCOUNT", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		c, e := config.Load(config.Path())
		if e != nil {
			return e
		}
		ac, ok := c.Accounts[a[0]]
		if !ok {
			return fmt.Errorf("account %q is not configured", a[0])
		}
		_ = authstore.Delete(ac.TokenKey)
		delete(c.Accounts, a[0])
		for id, s := range c.ServersV2 {
			if s.Account == a[0] {
				_ = authstore.Delete("server/" + a[0] + "/" + id)
				delete(c.ServersV2, id)
			}
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
	if !conn.Local && !conn.Relay && strings.HasPrefix(normalizedURL, "http://") {
		normalizedURL = "https://" + strings.TrimPrefix(normalizedURL, "http://")
		if parsed, err := url.Parse(conn.URI); err == nil {
			insecureTLS = net.ParseIP(parsed.Hostname()) != nil
		}
	}
	return normalizedConnection{URL: normalizedURL, InsecureTLS: insecureTLS}
}

func validatedConnection(ctx context.Context, resource plexauth.Resource, accountToken string) (plexauth.Connection, error) {
	candidates := append([]plexauth.Connection(nil), resource.Connections...)
	preferred := bestConnection(candidates)
	if preferred.URI == "" {
		return plexauth.Connection{}, fmt.Errorf("no connections discovered")
	}
	ordered := []plexauth.Connection{preferred}
	for _, candidate := range candidates {
		if candidate.URI == preferred.URI {
			continue
		}
		ordered = append(ordered, candidate)
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

func bestConnection(conns []plexauth.Connection) plexauth.Connection {
	for _, c := range conns {
		if c.Local && !c.Relay {
			return c
		}
	}
	for _, c := range conns {
		if !c.Local && !c.Relay {
			return c
		}
	}
	for _, c := range conns {
		if !c.Relay {
			return c
		}
	}
	if len(conns) > 0 {
		return conns[0]
	}
	return plexauth.Connection{}
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
	return cmd
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
	var accountID, viewedAt, librarySectionID, metadataItemID, sort string
	history := &cobra.Command{Use: "history", RunE: func(*cobra.Command, []string) error {
		c, e := configured(o)
		if e != nil {
			return e
		}
		q := url.Values{}
		for key, value := range map[string]string{"accountID": accountID, "viewedAt": viewedAt, "librarySectionID": librarySectionID, "metadataItemID": metadataItemID, "sort": sort} {
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
	history.Flags().StringVar(&sort, "sort", "", "sort expression, for example viewedAt:desc")
	cmd.AddCommand(history)
	return cmd
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
			return client.TranscodeSubtitles(x, a[0], a[1], q)
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
		ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
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
		ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
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
