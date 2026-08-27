package authstore

import "github.com/zalando/go-keyring"

const service = "github.com.keithah.plexctl"

func Set(server, token string) error    { return keyring.Set(service, server, token) }
func Get(server string) (string, error) { return keyring.Get(service, server) }
func Delete(server string) error        { return keyring.Delete(service, server) }
