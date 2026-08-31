package authstore

import (
	"errors"
	"os"

	"github.com/zalando/go-keyring"
)

const service = "github.com.keithah.plexctl"

func Set(key, token string) error {
	if err := keyring.Set(service, key, token); err == nil {
		return nil
	} else if fileErr := setFileFallback(key, token); fileErr == nil {
		return nil
	} else {
		return errors.Join(err, fileErr)
	}
}

func Get(key string) (string, error) {
	if tok, err := keyring.Get(service, key); err == nil {
		return tok, nil
	}
	if v, ok, err := getFileFallback(key); err != nil {
		return "", err
	} else if ok {
		return v, nil
	}
	if len(key) > 8 && key[:8] == "account/" {
		acc := key[8:]
		if v := os.Getenv("PLEX_TOKEN_" + acc); v != "" {
			return v, nil
		}
		if v := os.Getenv("PLEX_TOKEN_" + stringToUpper(acc)); v != "" {
			return v, nil
		}
	}
	return keyring.Get(service, key)
}

func stringToUpper(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		b[i] = c
	}
	return string(b)
}

func Delete(key string) error {
	fileErr := deleteFileFallback(key)
	keyErr := keyring.Delete(service, key)
	if keyErr != nil && !errors.Is(keyErr, keyring.ErrNotFound) {
		if fileErr != nil {
			return errors.Join(keyErr, fileErr)
		}
		return keyErr
	}
	return fileErr
}
