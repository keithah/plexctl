package authstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var fileMu sync.Mutex

func tokenFilePath() string {
	if p := os.Getenv("PLEXCTL_TOKENS_FILE"); p != "" {
		return p
	}
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
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}
	if len(b) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse token file %s: %w", path, err)
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
	cleanup := func(e error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return e
	}
	if _, err := f.Write(b); err != nil {
		return cleanup(err)
	}
	if err := f.Chmod(0600); err != nil {
		return cleanup(err)
	}
	if err := f.Sync(); err != nil {
		return cleanup(err)
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

func envTokenKey(key string) string {
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

func getFileFallback(key string) (string, bool, error) {
	if v := os.Getenv(envTokenKey(key)); v != "" {
		return v, true, nil
	}
	path := tokenFilePath()
	if path == "" {
		return "", false, nil
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := loadTokenFile(path)
	if err != nil {
		return "", false, err
	}
	v, ok := m[key]
	return v, ok, nil
}

func setFileFallback(key, token string) error {
	path := tokenFilePath()
	if path == "" {
		return errors.New("token file path is not configured")
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := loadTokenFile(path)
	if err != nil {
		return err
	}
	m[key] = token
	if err := saveTokenFile(path, m); err != nil {
		return fmt.Errorf("write token file %s: %w", path, err)
	}
	return nil
}

func deleteFileFallback(key string) (bool, error) {
	path := tokenFilePath()
	if path == "" {
		return false, nil
	}
	fileMu.Lock()
	defer fileMu.Unlock()
	m, err := loadTokenFile(path)
	if err != nil {
		return false, err
	}
	if _, ok := m[key]; !ok {
		return false, nil
	}
	delete(m, key)
	if err := saveTokenFile(path, m); err != nil {
		return true, fmt.Errorf("write token file %s: %w", path, err)
	}
	return true, nil
}
