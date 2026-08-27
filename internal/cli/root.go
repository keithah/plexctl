package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/health"
	"github.com/keithah/plexctl/internal/pms"
	"github.com/spf13/cobra"
	"net/url"
	"os"
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
	root.AddCommand(configCmd(), serverCmd(o), libraryCmd(o), metadataCmd(o), sessionsCmd(o), playlistsCmd(o), healthCmd(o), apiCmd(o))
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
	_, s, e := c.Resolve(o.server)
	if e != nil {
		return nil, e
	}
	token := os.Getenv(s.TokenEnv)
	if s.TokenEnv != "" && token == "" {
		return nil, fmt.Errorf("token environment variable %q is not set", s.TokenEnv)
	}
	a, e := api.New(s.URL, token, nil)
	if e != nil {
		return nil, e
	}
	if s.InsecureTLS {
		a.SetInsecureTLS(true)
	}
	return pms.New(a), nil
}
func commandContext(o *options) (context.Context, context.CancelFunc) {
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
