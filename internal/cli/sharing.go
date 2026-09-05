package cli

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/keithah/plexctl/internal/authstore"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/keithah/plexctl/internal/sharinghistory"
	"github.com/spf13/cobra"
)

var sharingPlexClient = func() *plexauth.Client {
	return plexauth.New("https://plex.tv", "plexctl", nil)
}

var sharingHistoryNow = time.Now

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

// sharingRemovedOutput is the CLI representation of a locally recorded
// successful external-share revocation.
type sharingRemovedOutput struct {
	RemovedAt              time.Time `json:"removed_at"`
	PlexUserID             int64     `json:"plex_user_id"`
	Username               string    `json:"username"`
	Email                  *string   `json:"email,omitempty"`
	ShareID                int64     `json:"share_id"`
	ServerName             string    `json:"server_name"`
	ServerClientIdentifier string    `json:"server_client_identifier"`
	AllLibraries           bool      `json:"all_libraries"`
	Pending                bool      `json:"pending"`
	LibrarySectionIDs      []int     `json:"library_section_ids"`
}

func sharingCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "sharing", Short: "Inspect Plex library sharing"}
	cmd.AddCommand(sharingUsersCmd(o), sharingLibrariesCmd(o), sharingInviteCmd(o), sharingUpdateCmd(o), sharingRemoveCmd(o), sharingRemovedCmd(o))
	return cmd
}

func sharingRemovedCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{Use: "removed", Short: "List locally recorded removed external shares", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		ctx, cancel := commandContext(o)
		defer cancel()
		records, err := sharinghistory.Open(sharinghistory.Path()).List(ctx)
		if err != nil {
			return err
		}
		out := make([]sharingRemovedOutput, 0, len(records))
		for _, record := range records {
			out = append(out, sharingRemovedOutput{
				RemovedAt:              record.RemovedAt,
				PlexUserID:             record.PlexUserID,
				Username:               record.Username,
				Email:                  record.Email,
				ShareID:                record.ShareID,
				ServerName:             record.ServerName,
				ServerClientIdentifier: record.ServerClientIdentifier,
				AllLibraries:           record.AllLibraries,
				Pending:                record.Pending,
				LibrarySectionIDs:      record.LibrarySectionIDs,
			})
		}
		if o.jsonOut {
			printValue(out, true)
		} else {
			printSharingRemoved(out)
		}
		return nil
	}}
	cmd.AddCommand(sharingRemovedPurgeCmd(o))
	return cmd
}

func sharingRemovedPurgeCmd(o *options) *cobra.Command {
	var olderThan string
	var yes bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "purge --older-than DURATION --yes [--dry-run]",
		Short: "Purge aged locally recorded removed external shares",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("json") {
				return fmt.Errorf("sharing removed purge does not support --json")
			}
			duration, err := time.ParseDuration(olderThan)
			if err != nil || duration <= 0 {
				return fmt.Errorf("--older-than must be a strictly positive Go duration (for example, 2160h)")
			}
			if !dryRun && !yes {
				return fmt.Errorf("sharing removed purge requires explicit --yes confirmation")
			}

			ctx, cancel := commandContext(o)
			defer cancel()
			history := sharinghistory.Open(sharinghistory.Path())
			cutoff := sharingHistoryNow().Add(-duration)
			if dryRun {
				count, err := history.CountBeforeReadOnly(ctx, cutoff)
				if err != nil {
					return err
				}
				fmt.Printf("dry run: %d locally recorded removed share would be purged\n", count)
				return nil
			}
			deleted, err := history.PurgeBefore(ctx, cutoff)
			if err != nil {
				return err
			}
			fmt.Printf("purged %d locally recorded removed share\n", deleted)
			return nil
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "", "purge records older than this Go duration (for example, 2160h)")
	cmd.Flags().Bool("json", false, "")
	_ = cmd.Flags().MarkHidden("json")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm local history deletion")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "count matching local records without deleting them")
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		fmt.Fprintf(c.OutOrStdout(), "%s\n\nUsage:\n  %s\n\nFlags:\n", c.Short, c.UseLine())
		flags := c.Flags()
		flags.SetOutput(c.OutOrStdout())
		flags.PrintDefaults()
	})
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
				if !share.Owned {
					continue
				}
				resource, err := plexauth.ResolveOwnedResource(resources, share.MachineIdentifier)
				if err != nil {
					// Plex can retain a stale share after the corresponding server is no
					// longer advertised to this account. It cannot safely be queried for
					// grants, so omit it rather than failing the complete read-only list.
					continue
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
		selector := server
		if profile, ok := c.ServersV2[server]; ok && profile.MachineIdentifier != "" {
			selector = profile.MachineIdentifier
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

func sharingRemoveCmd(o *options) *cobra.Command {
	var yes bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "remove <share-id>",
		Short: "Revoke one external Plex server share",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			shareID, err := strconv.Atoi(args[0])
			if err != nil || shareID <= 0 {
				return fmt.Errorf("share ID must be a positive integer, got %q", args[0])
			}
			if o.server == "" {
				return fmt.Errorf("sharing remove requires --server")
			}
			server, profile, err := sharingInviteProfile(o.server)
			if err != nil {
				return err
			}
			selector := profile.MachineIdentifier
			if selector == "" {
				selector = server
			}
			if !yes {
				return fmt.Errorf("sharing remove requires explicit --yes confirmation")
			}
			if dryRun {
				fmt.Printf("dry run: would revoke share %d on server %s (%s)\n", shareID, server, selector)
				return nil
			}

			_, account, token, err := sharingAccountToken(server)
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
			resource, err := plexauth.ResolveOwnedResource(resources, selector)
			if err != nil {
				return err
			}
			users, err := plex.SharedUsers(ctx, token)
			if err != nil {
				return err
			}
			matchedUser, matchedShare, err := plexauth.FindExternalOwnedShare(users, resource.ClientIdentifier, shareID)
			if err != nil {
				return err
			}
			grants, err := plex.SharedServerSections(ctx, token, resource.ClientIdentifier, shareID)
			if err != nil {
				return err
			}
			if err := plex.RemoveShare(ctx, token, resource.ClientIdentifier, shareID); err != nil {
				return err
			}
			grantIDs := make([]int, 0, len(grants))
			for _, grant := range grants {
				grantIDs = append(grantIDs, grant.ID)
			}
			if err := sharinghistory.Open(sharinghistory.Path()).Append(ctx, sharinghistory.Record{
				RemovedAt:              time.Now(),
				PlexUserID:             int64(matchedUser.ID),
				Username:               matchedUser.Username,
				Email:                  matchedUser.Email,
				ShareID:                int64(matchedShare.ID),
				ServerName:             resource.Name,
				ServerClientIdentifier: resource.ClientIdentifier,
				AllLibraries:           matchedShare.AllLibraries,
				Pending:                matchedShare.Pending,
				LibrarySectionIDs:      grantIDs,
			}); err != nil {
				return fmt.Errorf("Plex share revocation succeeded but local history recording failed: %w", err)
			}
			fmt.Printf("REVOKED share %d on server %s (%s)\n", shareID, resource.Name, resource.ClientIdentifier)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm revocation of this exact share")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the exact revocation target without making network requests")
	return cmd
}

func sharingUpdateCmd(o *options) *cobra.Command {
	var librariesValue string
	var allLibraries bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "update <share-id>",
		Short: "Replace an external Plex share's library grants",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shareID, err := strconv.Atoi(args[0])
			if err != nil || shareID <= 0 {
				return fmt.Errorf("share ID must be a positive integer, got %q", args[0])
			}
			requested, err := inviteLibrarySelection(cmd, librariesValue, allLibraries)
			if err != nil {
				return fmt.Errorf("update %w", err)
			}
			if o.server == "" {
				return fmt.Errorf("sharing update requires --server")
			}
			server, profile, err := sharingInviteProfile(o.server)
			if err != nil {
				return err
			}
			if dryRun {
				grants := strings.Join(requested, ",")
				if allLibraries {
					grants = "all current Plex.tv libraries"
				}
				fmt.Printf("dry run: REPLACE grants for share %d on server %s (%s) with %s\n", shareID, server, profile.MachineIdentifier, grants)
				return nil
			}

			_, account, token, err := sharingAccountToken(server)
			if err != nil {
				return err
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
			users, err := plex.SharedUsers(ctx, token)
			if err != nil {
				return err
			}
			if err := plexauth.ValidateExternalOwnedShare(users, resource.ClientIdentifier, shareID); err != nil {
				return err
			}
			libraries, err := plex.ServerLibraries(ctx, token, resource.ClientIdentifier)
			if err != nil {
				return err
			}
			if allLibraries {
				requested = make([]string, 0, len(libraries))
				for _, library := range libraries {
					requested = append(requested, strconv.Itoa(library.ID))
				}
			}
			if err := plexauth.ValidateLibraryIDs(libraries, requested); err != nil {
				return err
			}
			sectionIDs, err := parseInviteLibraryIDs(requested)
			if err != nil {
				return err
			}
			if err := plex.UpdateShare(ctx, token, resource.ClientIdentifier, shareID, sectionIDs); err != nil {
				return err
			}
			fmt.Printf("REPLACED grants for share %d on server %s (%s) with %s\n", shareID, resource.Name, resource.ClientIdentifier, strings.Join(requested, ","))
			return nil
		},
	}
	cmd.Flags().StringVar(&librariesValue, "libraries", "", "comma-separated global Plex.tv library section IDs")
	cmd.Flags().BoolVar(&allLibraries, "all-libraries", false, "replace grants with every library currently reported by Plex.tv")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the full replacement plan without making network requests")
	return cmd
}

func sharingInviteCmd(o *options) *cobra.Command {
	var librariesValue string
	var allLibraries bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "invite <email-or-username>",
		Short: "Invite an external Plex account to selected libraries",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requested, err := inviteLibrarySelection(cmd, librariesValue, allLibraries)
			if err != nil {
				return err
			}
			server, profile, err := sharingInviteProfile(o.server)
			if err != nil {
				return err
			}
			if dryRun {
				grants := strings.Join(requested, ",")
				if allLibraries {
					grants = "all Plex.tv libraries"
				}
				fmt.Printf("dry run: invite %s to server %s (%s) with grants %s\n", args[0], server, profile.MachineIdentifier, grants)
				return nil
			}

			_, account, token, err := sharingAccountToken(server)
			if err != nil {
				return err
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
			if allLibraries {
				requested = make([]string, 0, len(libraries))
				for _, library := range libraries {
					requested = append(requested, strconv.Itoa(library.ID))
				}
			}
			if err := plexauth.ValidateLibraryIDs(libraries, requested); err != nil {
				return err
			}
			sectionIDs, err := parseInviteLibraryIDs(requested)
			if err != nil {
				return err
			}
			if err := plex.Invite(ctx, token, plexauth.InviteRequest{MachineIdentifier: resource.ClientIdentifier, InvitedEmail: args[0], LibrarySectionIDs: sectionIDs}); err != nil {
				var statusErr *plexauth.HTTPError
				if errors.As(err, &statusErr) && (statusErr.StatusCode == 409 || statusErr.StatusCode == 422) {
					return fmt.Errorf("invite %q to %q conflicts with an existing or pending share (HTTP %d): %w", args[0], resource.Name, statusErr.StatusCode, err)
				}
				return err
			}
			fmt.Printf("invited %s to server %s (%s) with grants %s\n", args[0], resource.Name, resource.ClientIdentifier, strings.Join(requested, ","))
			return nil
		},
	}
	cmd.Flags().StringVar(&librariesValue, "libraries", "", "comma-separated global Plex.tv library section IDs")
	cmd.Flags().BoolVar(&allLibraries, "all-libraries", false, "grant every library currently reported by Plex.tv")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the invitation plan without making a network request")
	return cmd
}

func inviteLibrarySelection(cmd *cobra.Command, librariesValue string, allLibraries bool) ([]string, error) {
	librariesSet := cmd.Flags().Changed("libraries")
	if librariesSet == allLibraries {
		return nil, fmt.Errorf("invite requires exactly one of --libraries or --all-libraries")
	}
	if allLibraries {
		return nil, nil
	}
	parts := strings.Split(librariesValue, ",")
	requested := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			return nil, fmt.Errorf("--libraries must contain at least one non-empty global Plex.tv library section ID")
		}
		if _, err := strconv.Atoi(id); err != nil {
			return nil, fmt.Errorf("invalid Plex.tv library ID %q", id)
		}
		requested = append(requested, id)
	}
	return requested, nil
}

func parseInviteLibraryIDs(requested []string) ([]int, error) {
	ids := make([]int, 0, len(requested))
	for _, requestedID := range requested {
		id, err := strconv.Atoi(requestedID)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid Plex.tv library ID %q", requestedID)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func sharingInviteProfile(selector string) (string, config.ServerProfile, error) {
	c, err := config.Load(config.Path())
	if err != nil {
		return "", config.ServerProfile{}, err
	}
	if selector == "" {
		selector = c.CurrentServer
	}
	if selector == "" {
		return "", config.ServerProfile{}, fmt.Errorf("sharing invite requires --server or a current configured server")
	}
	return selector, c.ServersV2[selector], nil
}

func sharingAccountToken(server string) (config.Config, string, string, error) {
	c, err := config.Load(config.Path())
	if err != nil {
		return config.Config{}, "", "", err
	}
	account := c.CurrentAccount
	if profile, ok := c.ServersV2[server]; ok {
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
		fmt.Printf("%d	%d	%s	%s	shared=%t\n", library.ID, library.Key, library.Title, library.Type, library.Shared)
	}
}

func printSharingRemoved(records []sharingRemovedOutput) {
	for _, record := range records {
		email := ""
		if record.Email != nil {
			email = *record.Email
		}
		grants := make([]string, 0, len(record.LibrarySectionIDs))
		for _, id := range record.LibrarySectionIDs {
			grants = append(grants, strconv.Itoa(id))
		}
		fmt.Printf("%s	%s	%s	share_id=%d	server=%s	%s	all_libraries=%t	pending=%t	grants=%s\n",
			record.RemovedAt.Format(time.RFC3339Nano), record.Username, email, record.ShareID,
			record.ServerName, record.ServerClientIdentifier, record.AllLibraries, record.Pending,
			strings.Join(grants, ","))
	}
}
