package cli

import (
	"fmt"
	"sort"

	"github.com/keithah/plexctl/internal/authstore"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/spf13/cobra"
)

var sharingPlexClient = func() *plexauth.Client {
	return plexauth.New("https://plex.tv", "plexctl", nil)
}

type sharingLibraryOutput struct {
	ID     int    `json:"id"`
	Key    int    `json:"key"`
	Shared bool   `json:"shared"`
	Title  string `json:"title"`
	Type   string `json:"type"`
}

type sharingServerOutput struct {
	Pending                bool                   `json:"pending"`
	ShareID                int                    `json:"share_id"`
	ServerID               int                    `json:"server_id"`
	ServerClientIdentifier string                 `json:"server_client_identifier"`
	ServerName             string                 `json:"server_name"`
	AllLibraries           bool                   `json:"all_libraries"`
	Grants                 []sharingLibraryOutput `json:"grants"`
}

type sharingUserOutput struct {
	Username string                `json:"username"`
	Email    *string               `json:"email,omitempty"`
	Home     bool                  `json:"home"`
	Shares   []sharingServerOutput `json:"shares"`
}

func sharingCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "sharing", Short: "Inspect Plex library sharing"}
	cmd.AddCommand(sharingUsersCmd(o), sharingLibrariesCmd(o))
	return cmd
}

func sharingUsersCmd(o *options) *cobra.Command {
	return &cobra.Command{Use: "users", Short: "List external Plex users and their grants", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		_, account, token, err := sharingAccountToken("")
		if err != nil {
			return err
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		plex := sharingPlexClient()
		resources, err := plex.Resources(ctx, token)
		if err != nil {
			return fmt.Errorf("refresh Plex resources for %s: %w", account, err)
		}
		users, err := plex.SharedUsers(ctx, token)
		if err != nil {
			return err
		}
		out := make([]sharingUserOutput, 0, len(users))
		for _, user := range users {
			if user.Home {
				continue
			}
			item := sharingUserOutput{Username: user.Username, Email: user.Email, Home: user.Home, Shares: make([]sharingServerOutput, 0, len(user.ServerShares))}
			for _, share := range user.ServerShares {
				resource, err := plexauth.ResolveOwnedResource(resources, share.MachineIdentifier)
				if err != nil {
					return fmt.Errorf("resolve share %d for %s: %w", share.ID, user.Username, err)
				}
				grants, err := plex.SharedServerSections(ctx, token, resource.ClientIdentifier, share.ID)
				if err != nil {
					return err
				}
				item.Shares = append(item.Shares, sharingServerOutput{
					Pending: share.Pending, ShareID: share.ID, ServerID: share.ServerID,
					ServerClientIdentifier: resource.ClientIdentifier, ServerName: share.Name,
					AllLibraries: share.AllLibraries, Grants: sharingLibrariesOutput(grants),
				})
			}
			sort.SliceStable(item.Shares, func(i, j int) bool { return item.Shares[i].ShareID < item.Shares[j].ShareID })
			out = append(out, item)
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Username < out[j].Username })
		if o.jsonOut {
			printValue(out, true)
		} else {
			printSharingUsers(out)
		}
		return nil
	}}
}

func sharingLibrariesCmd(o *options) *cobra.Command {
	return &cobra.Command{Use: "libraries", Short: "List Plex.tv sharing libraries for an owned server", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		c, account, token, err := sharingAccountToken(o.server)
		if err != nil {
			return err
		}
		server := o.server
		if server == "" {
			server = c.CurrentServer
		}
		if server == "" {
			return fmt.Errorf("sharing libraries requires --server or a current configured server")
		}
		profile, ok := c.ServersV2[server]
		if !ok {
			return fmt.Errorf("server %q is not configured", server)
		}
		selector := profile.MachineIdentifier
		if selector == "" {
			selector = server
		}
		ctx, cancel := commandContext(o)
		defer cancel()
		plex := sharingPlexClient()
		resources, err := plex.Resources(ctx, token)
		if err != nil {
			return fmt.Errorf("refresh Plex resources for %s: %w", account, err)
		}
		resource, err := plexauth.ResolveOwnedResource(resources, selector)
		if err != nil {
			return err
		}
		libraries, err := plex.ServerLibraries(ctx, token, resource.ClientIdentifier)
		if err != nil {
			return err
		}
		out := sharingLibrariesOutput(libraries)
		if o.jsonOut {
			printValue(out, true)
		} else {
			printSharingLibraries(out)
		}
		return nil
	}}
}

func sharingAccountToken(server string) (config.Config, string, string, error) {
	c, err := config.Load(config.Path())
	if err != nil {
		return config.Config{}, "", "", err
	}
	account := c.CurrentAccount
	if server != "" {
		profile, ok := c.ServersV2[server]
		if !ok {
			return config.Config{}, "", "", fmt.Errorf("server %q is not configured", server)
		}
		account = profile.Account
	}
	if account == "" {
		return config.Config{}, "", "", fmt.Errorf("no current Plex account is configured")
	}
	configured, ok := c.Accounts[account]
	if !ok {
		return config.Config{}, "", "", fmt.Errorf("account %q is not configured", account)
	}
	token, err := authstore.Get(configured.TokenKey)
	if err != nil {
		return config.Config{}, "", "", err
	}
	return c, account, token, nil
}

func sharingLibrariesOutput(libraries []plexauth.LibrarySection) []sharingLibraryOutput {
	out := make([]sharingLibraryOutput, 0, len(libraries))
	for _, library := range libraries {
		out = append(out, sharingLibraryOutput{ID: library.ID, Key: library.Key, Shared: library.Shared, Title: library.Title, Type: library.Type})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func printSharingUsers(users []sharingUserOutput) {
	for _, user := range users {
		for _, share := range user.Shares {
			fmt.Printf("%s\t%s\tpending=%t\tshare_id=%d\tserver=%s\tgrants=%d\n", user.Username, share.ServerName, share.Pending, share.ShareID, share.ServerClientIdentifier, len(share.Grants))
		}
	}
}

func printSharingLibraries(libraries []sharingLibraryOutput) {
	for _, library := range libraries {
		fmt.Printf("%d\t%d\t%s\t%s\tshared=%t\n", library.ID, library.Key, library.Title, library.Type, library.Shared)
	}
}
