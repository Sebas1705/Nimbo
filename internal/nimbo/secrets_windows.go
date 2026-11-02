//go:build windows

package nimbo

// DefaultSecretStore protects passwords with Windows (DPAPI) when the data lives in a folder; an in-memory store has
// no place to keep the ciphertext.
func DefaultSecretStore(data *AppData) SecretStore {
	if data != nil && data.Store.Location() != "" {
		return NewDpapiSecretStore(data.Store, ProcessShell{Bin: "powershell.exe"})
	}
	return UnavailableSecretStore{Why: UnavailableSecretMessage}
}
