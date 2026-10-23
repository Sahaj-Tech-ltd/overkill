// Package credentials stores application credentials in the operating system's
// credential vault. It intentionally has no plaintext-file fallback.
package credentials

import "github.com/zalando/go-keyring"

const Service = "Overkill CLI credentials"

// Store is injected by acceptance tests and embedders. Accounts are opaque,
// profile-bound identifiers; callers must never use credential text as an ID.
type Store interface {
	Set(account, secret string) error
	Get(account string) (string, error)
	Delete(account string) error
}

// NativeStore uses macOS Keychain, Windows Credential Manager, or Linux
// Secret Service through go-keyring. Constructing it does not access a vault.
type NativeStore struct{}

func (NativeStore) Set(account, secret string) error   { return keyring.Set(Service, account, secret) }
func (NativeStore) Get(account string) (string, error) { return keyring.Get(Service, account) }
func (NativeStore) Delete(account string) error        { return keyring.Delete(Service, account) }
