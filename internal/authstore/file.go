package authstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	fileMu   sync.Mutex
	filePath string
)

func tokenFilePath() string {
	if p := os.Getenv("PLEXCTL_TOKENS_FILE"); p != "" {
		return p
	}
	// Fall back to XDG config dir tokens.json alongside config.json
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "plexctl", "tokens.json")
	}
	return ""
}

func loadTokenFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

func saveTokenFile(path string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "tokens-*.tmp")
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
	return os.Rename(tmp, path)
}

func envTokenKey(key string) string {
	// PLEXCTL_TOKEN_<normalized key> e.g. server/mseast/<id> -> PLEXCTL_TOKEN_SERVER_MSEAST_<ID>
	norm := strings.ToUpper(key)
	var sb strings.Builder
	for _, r := range norm {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return "PLEXCTL_TOKEN_" + sb.String()
}

func getFileFallback(key string) (string, bool) {
	// 1. env var per key
	if v := os.Getenv(envTokenKey(key)); v != "" {
		return v, true
	}
	// 2. JSON file
	path := tokenFilePath()
	if path == "" {
		return "", false
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := loadTokenFile(path)
	if err != nil {
		return "", false
	}
	v, ok := m[key]
	return v, ok
}

func setFileFallback(key, token string) bool {
	path := tokenFilePath()
	if path == "" {
		return false
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := loadTokenFile(path)
	if err != nil {
		return false
	}
	m[key] = token
	if err := saveTokenFile(path, m); err != nil {
		return false
	}
	return true
}

func deleteFileFallback(key string) bool {
	path := tokenFilePath()
	if path == "" {
		return false
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := loadTokenFile(path)
	if err != nil {
		return false
	}
	if _, ok := m[key]; !ok {
		return true
	}
	delete(m, key)
	if err := saveTokenFile(path, m); err != nil {
		return false
	}
	return true
}
