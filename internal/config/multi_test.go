package config

import "testing"

func TestMultipleAccountsAndServersRoundTrip(t *testing.T) {
	path := t.TempDir() + "/config.json"
	want := Config{
		CurrentAccount: "alice",
		CurrentServer:  "living",
		Accounts: map[string]Account{
			"alice": {Username: "alice@example.com", TokenKey: "account/alice"},
			"bob":   {Username: "bob@example.com", TokenKey: "account/bob"},
		},
		ServersV2: map[string]ServerProfile{
			"living": {Account: "alice", Name: "Living Room", URL: "http://living:32400", Local: true, InsecureTLS: false},
			"office": {Account: "bob", Name: "Office", URL: "https://office:32400", InsecureTLS: true},
		},
	}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentAccount != "alice" || got.CurrentServer != "living" {
		t.Fatalf("selection: %+v", got)
	}
	if len(got.Accounts) != 2 || len(got.ServersV2) != 2 {
		t.Fatalf("profiles: %+v", got)
	}
	if got.ServersV2["living"].Account != "alice" || !got.ServersV2["living"].Local {
		t.Fatalf("living: %+v", got.ServersV2["living"])
	}
	if !got.ServersV2["office"].InsecureTLS {
		t.Fatalf("office insecure TLS flag was not persisted: %+v", got.ServersV2["office"])
	}
}
