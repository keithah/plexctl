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
type Config struct {
	Current string            `json:"current,omitempty"`
	Servers map[string]Server `json:"servers"`
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
		return Config{Servers: map[string]Server{}}, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if c.Servers == nil {
		c.Servers = map[string]Server{}
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
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, append(b, '\n'), 0600); e != nil {
		return e
	}
	if e = os.Chmod(tmp, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
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
