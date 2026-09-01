package authstore

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "github.com.keithah.plexctl"

func Set(key, token string) error {
	if err := keyring.Set(service, key, token); err == nil {
		if _, fileErr := deleteFileFallback(key); fileErr != nil {
			return fmt.Errorf("keyring set succeeded but stale file fallback remains: %w", fileErr)
		}
		return nil
	} else if fileErr := setFileFallback(key, token); fileErr == nil {
		return nil
	} else {
		return errors.Join(err, fileErr)
	}
}

func Get(key string) (string, error) {
	tok, keyErr := keyring.Get(service, key)
	if keyErr == nil {
		return tok, nil
	}
	if v, ok, err := getFileFallback(key); err != nil {
		return "", err
	} else if ok {
		return v, nil
	}
	if strings.HasPrefix(key, "account/") {
		acc := key[8:]
		if v := os.Getenv("PLEX_TOKEN_" + acc); v != "" {
			return v, nil
		}
		if v := os.Getenv("PLEX_TOKEN_" + strings.ToUpper(acc)); v != "" {
			return v, nil
		}
	}
	return "", keyring.ErrNotFound
}

func Delete(key string) error {
	filePresent, fileErr := deleteFileFallback(key)
	keyErr := keyring.Delete(service, key)
	if keyErr != nil && !errors.Is(keyErr, keyring.ErrNotFound) && !filePresent {
		if fileErr != nil {
			return errors.Join(keyErr, fileErr)
		}
		return keyErr
	}
	return fileErr
}
