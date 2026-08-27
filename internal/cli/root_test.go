package cli

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestCommandTreeIsRegistered(t *testing.T) {
	want := []string{"auth login", "accounts list", "accounts use", "servers list", "servers use", "server info", "library search", "library recently-added", "metadata children", "sessions list", "sessions history", "playlists list", "playlists get", "playlists items", "collections list", "collections items", "download-queues get", "download-queues items", "download-queues item", "download-queues decision", "transcode decision", "transcode subtitles"}
	for _, path := range want {
		parts := strings.Split(path, " ")
		cmd, _, err := NewRoot().Find(parts)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if cmd.Name() != parts[len(parts)-1] {
			t.Fatalf("%s resolved to %q", path, cmd.CommandPath())
		}
	}
}

// search and recently-added must not share one --limit variable, otherwise
// setting the flag on one command leaks into the other.
func TestLimitFlagsAreIndependent(t *testing.T) {
	root := NewRoot()
	search, _, err := root.Find([]string{"library", "search"})
	if err != nil {
		t.Fatal(err)
	}
	recent, _, err := root.Find([]string{"library", "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if err = search.Flags().Set("limit", "3"); err != nil {
		t.Fatal(err)
	}
	if got := recent.Flags().Lookup("limit").Value.String(); got != "20" {
		t.Fatalf("recently-added --limit leaked: %s", got)
	}
	if search.Flags().Lookup("section") == nil {
		t.Fatal("search is missing --section")
	}
}

func TestRawAPIRejectsMutations(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if _, err := run(t, "api", method, "/identity"); err == nil {
			t.Fatalf("%s was not rejected", method)
		} else if !strings.Contains(err.Error(), "typed command") {
			t.Fatalf("%s: unexpected error %v", method, err)
		}
	}
}

// The raw API command previously advertised a --body-json flag that was never
// sent; a flag that silently does nothing must not exist.
func TestRawAPIHasNoInertBodyFlag(t *testing.T) {
	cmd, _, err := NewRoot().Find([]string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if f := cmd.Flags().Lookup("body-json"); f != nil {
		t.Fatal("api still exposes an inert --body-json flag")
	}
}

func TestUnknownServerIsReported(t *testing.T) {
	t.Setenv("PLEXCTL_CONFIG", t.TempDir()+"/config.json")
	_, err := run(t, "server", "identity", "--server", "missing")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestArgumentValidation(t *testing.T) {
	t.Setenv("PLEXCTL_CONFIG", t.TempDir()+"/config.json")
	for _, args := range [][]string{{"library", "search"}, {"metadata", "children"}, {"library", "recently-added"}} {
		if _, err := run(t, args...); err == nil {
			t.Fatalf("%v accepted missing arguments", args)
		}
	}
}

func TestAccountAndServerCommandsListByDefault(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"accounts", "servers"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.RunE == nil {
			t.Fatalf("%s should list when invoked without a subcommand", name)
		}
	}
}
