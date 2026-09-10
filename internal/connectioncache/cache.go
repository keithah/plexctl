package connectioncache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/keithah/plexctl/internal/plexauth"
)

type diskCache struct {
	Connections map[string]plexauth.Connection   `json:"connections"`
	Histories   map[string][]plexauth.Connection `json:"histories,omitempty"`
}

const maxCandidates = 3

type Store struct {
	path string
	mu   sync.Mutex
}

func Path() string {
	if path := os.Getenv("PLEXCTL_CONNECTION_CACHE"); path != "" {
		return path
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "plexctl", "connections.json")
	}
	return "connections.json"
}

func New(path string) *Store {
	return &Store{path: path}
}

func cacheKey(account, machine string) string {
	return account + "\x00" + machine
}

func (s *Store) Get(account, machine string) (plexauth.Connection, bool, error) {
	if account == "" || machine == "" {
		return plexauth.Connection{}, false, fmt.Errorf("cache lookup requires account and machine identifier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cache, err := s.load()
	if err != nil {
		return plexauth.Connection{}, false, err
	}
	connection, ok := cache.Connections[cacheKey(account, machine)]
	return connection, ok, nil
}

// Candidates returns the newest validated connections first. The historical
// entries remain candidates only: callers must still validate the PMS identity
// before using one.
func (s *Store) Candidates(account, machine string) ([]plexauth.Connection, error) {
	if account == "" || machine == "" {
		return nil, fmt.Errorf("cache lookup requires account and machine identifier")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cache, err := s.load()
	if err != nil {
		return nil, err
	}
	key := cacheKey(account, machine)
	candidates := append([]plexauth.Connection(nil), cache.Histories[key]...)
	if current, ok := cache.Connections[key]; ok {
		candidates = prependUniqueConnection(current, candidates)
	}
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}
	return candidates, nil
}

func (s *Store) Put(account, machine string, connection plexauth.Connection) error {
	if account == "" || machine == "" || connection.URI == "" {
		return fmt.Errorf("cache write requires account, machine identifier, and connection URI")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cache, err := s.load()
	if err != nil {
		return err
	}
	cache.Connections[cacheKey(account, machine)] = connection
	key := cacheKey(account, machine)
	cache.Histories[key] = prependUniqueConnection(connection, cache.Histories[key])
	if len(cache.Histories[key]) > maxCandidates {
		cache.Histories[key] = cache.Histories[key][:maxCandidates]
	}
	return s.save(cache)
}

func prependUniqueConnection(connection plexauth.Connection, connections []plexauth.Connection) []plexauth.Connection {
	out := make([]plexauth.Connection, 0, len(connections)+1)
	out = append(out, connection)
	for _, candidate := range connections {
		if candidate.URI != connection.URI {
			out = append(out, candidate)
		}
	}
	return out
}

func (s *Store) load() (diskCache, error) {
	cache := diskCache{Connections: map[string]plexauth.Connection{}, Histories: map[string][]plexauth.Connection{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return cache, fmt.Errorf("read connection cache: %w", err)
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return cache, fmt.Errorf("parse connection cache: %w", err)
	}
	if cache.Connections == nil {
		cache.Connections = map[string]plexauth.Connection{}
	}
	if cache.Histories == nil {
		cache.Histories = map[string][]plexauth.Connection{}
	}
	return cache, nil
}

func (s *Store) save(cache diskCache) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create connection cache directory: %w", err)
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(s.path), "connections-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	cleanup := func(err error) error {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if _, err := file.Write(data); err != nil {
		return cleanup(err)
	}
	if err := file.Chmod(0600); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
