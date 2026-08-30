package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Server struct {
	URL         string `json:"url"`
	TokenEnv    string `json:"token_env"`
	InsecureTLS bool   `json:"insecure_tls,omitempty"`
}
type Account struct {
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	PlexID   int    `json:"plex_id,omitempty"`
	TokenKey string `json:"token_key"`
}
type ServerProfile struct {
	Account           string `json:"account"`
	Name              string `json:"name"`
	MachineIdentifier string `json:"machine_identifier,omitempty"`
	TokenKey          string `json:"token_key,omitempty"`
	URL               string `json:"url"`
	InsecureTLS       bool   `json:"insecure_tls,omitempty"`
	Local             bool   `json:"local,omitempty"`
	Relay             bool   `json:"relay,omitempty"`
}
type Config struct {
	Current        string                   `json:"current,omitempty"`
	Servers        map[string]Server        `json:"servers"`
	CurrentAccount string                   `json:"current_account,omitempty"`
	CurrentServer  string                   `json:"current_server,omitempty"`
	Accounts       map[string]Account       `json:"accounts,omitempty"`
	ServersV2      map[string]ServerProfile `json:"server_profiles,omitempty"`
}

func Path() string {
	if p := os.Getenv("PLEXCTL_CONFIG"); p != "" {
		return p
	}
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "plexctl", "config.json")
}
func Load(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return Config{Servers: map[string]Server{}, Accounts: map[string]Account{}, ServersV2: map[string]ServerProfile{}}, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if c.Servers == nil {
		c.Servers = map[string]Server{}
	}
	if c.Accounts == nil {
		c.Accounts = map[string]Account{}
	}
	if c.ServersV2 == nil {
		c.ServersV2 = map[string]ServerProfile{}
	}
	return c, e
}
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
func (c Config) Resolve(name string) (string, Server, error) {
	if name == "" {
		name = c.Current
	}
	if name == "" && len(c.Servers) == 1 {
		for n := range c.Servers {
			name = n
		}
	}
	s, ok := c.Servers[name]
	if !ok {
		return name, s, fmt.Errorf("server %q is not configured", name)
	}
	if s.URL == "" {
		return name, s, fmt.Errorf("server %q has no URL", name)
	}
	return name, s, nil
}
