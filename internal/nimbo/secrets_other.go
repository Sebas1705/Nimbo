//go:build !windows

package nimbo

// DefaultSecretStore: outside Windows nothing protects a secret, so Nimbo says so instead of storing it in the clear.
func DefaultSecretStore(*AppData) SecretStore {
	return UnavailableSecretStore{Why: UnavailableSecretMessage}
}
