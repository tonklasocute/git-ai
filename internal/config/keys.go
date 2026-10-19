package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name used in the OS keychain.
const KeyringService = "git-quill"

// KeySource says where an API key came from (never the key itself).
type KeySource string

const (
	KeyFromEnv     KeySource = "env"
	KeyFromKeyring KeySource = "keychain"
	KeyMissing     KeySource = ""
)

// APIKey resolves the key for a provider: env vars first, then the OS
// keychain. It returns the source for `config list` / --verbose output.
func APIKey(info ProviderInfo) (string, KeySource, string) {
	for _, env := range info.KeyEnv {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, KeyFromEnv, env
		}
	}
	if v, err := keyring.Get(KeyringService, info.Name); err == nil && v != "" {
		return v, KeyFromKeyring, "keychain"
	}
	return "", KeyMissing, ""
}

// SetKey stores a key in the OS keychain.
func SetKey(provider, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("empty key")
	}
	if err := keyring.Set(KeyringService, provider, key); err != nil {
		return fmt.Errorf("could not save to the OS keychain (%w); set %s in your environment instead", err, envFor(provider))
	}
	return nil
}

// DeleteKey removes a key from the OS keychain.
func DeleteKey(provider string) error {
	err := keyring.Delete(KeyringService, provider)
	if errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("no key stored for %s", provider)
	}
	return err
}

func envFor(provider string) string {
	if info, ok := Lookup(provider); ok && len(info.KeyEnv) > 0 {
		return info.KeyEnv[0]
	}
	return "the provider's API key variable"
}

// MissingKeyError explains how to provide a key.
func MissingKeyError(info ProviderInfo) error {
	return fmt.Errorf("no API key for %s.\n  Set %s in your environment, or store it in the OS keychain:\n    git quill config set-key %s\n  Want to stay fully local instead? Use Ollama: git quill config set provider ollama",
		info.Name, strings.Join(info.KeyEnv, " or "), info.Name)
}
