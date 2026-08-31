package authstore

import (
	"os"

	"github.com/zalando/go-keyring"
)

const service = "github.com.keithah.plexctl"

func Set(key, token string) error {
	if err := keyring.Set(service, key, token); err == nil {
		return nil
	}
	// Fall back to file/env store (used inside containers without a keyring daemon).
	if setFileFallback(key, token) {
		return nil
	}
	// Last resort: return the original keyring error.
	return keyring.Set(service, key, token)
}

func Get(key string) (string, error) {
	if tok, err := keyring.Get(service, key); err == nil {
		return tok, nil
	}
	if v, ok := getFileFallback(key); ok {
		return v, nil
	}
	// Allow legacy PLEX_TOKEN_<ACCOUNT> env for account keys (e.g. account/mswest1 -> PLEX_TOKEN_MSWEST1).
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
	_ = deleteFileFallback(key)
	if err := keyring.Delete(service, key); err != nil {
		// If the secret was only in the file fallback, treat as success.
		if _, ok := getFileFallback(key); !ok {
			// getFileFallback already did locking; just check existence via file
			// by trying to see if we deleted it above. If deleteFileFallback
			// succeeded and keyring says not found, don't error.
			if err == keyring.ErrNotFound {
				return nil
			}
		}
		return err
	}
	return nil
}
