package nimbo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// SecretStore is where the passwords of MinIO and PostgreSQL live when you do not want them in an environment
// variable or a .env file. A store that cannot protect them says so instead of keeping them in the clear.
type SecretStore interface {
	Available() bool
	// Reason is why it is not available, in a sentence a person can act on ("" when it is available).
	Reason() string
	// Names are the names that have a value. Never the values.
	Names(ctx context.Context) []string
	// Get returns the value, and false when there is none.
	Get(ctx context.Context, name string) (string, bool, error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// UnavailableSecretMessage is what Nimbo says where nothing can protect a secret.
const UnavailableSecretMessage = "Secrets can only be protected on Windows; elsewhere use environment variables or a .env file."

// SecretNames are the only secrets Nimbo knows what to do with: the ones providers read from the environment.
var SecretNames = secretNames()

func secretNames() []string {
	var out []string
	seen := map[string]bool{}
	for _, def := range CatalogProviders {
		for _, kv := range def.SecretEnv {
			if !seen[kv.Value] {
				seen[kv.Value] = true
				out = append(out, kv.Value)
			}
		}
	}
	return out
}

func isSecretName(name string) bool {
	for _, n := range SecretNames {
		if n == name {
			return true
		}
	}
	return false
}

var secretControlRe = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// ValidateSecret explains what is wrong with a name or value.
func ValidateSecret(name, value string) error {
	if !isSecretName(name) {
		return UnknownTarget{"Unknown secret '" + name + "'"}
	}
	if n := utf8.RuneCountInString(value); n == 0 || n > 128 {
		return InvalidParam{"The value must have between 1 and 128 characters"}
	}
	if secretControlRe.MatchString(value) {
		return InvalidParam{"The value cannot contain control characters"}
	}
	// MinIO refuses to start with a short root password, and it is better to hear it now than from a failed container.
	if name == "NIMBO_MINIO_PASSWORD" && utf8.RuneCountInString(value) < 8 {
		return InvalidParam{"MinIO needs a password of at least 8 characters"}
	}
	if name == "NIMBO_MINIO_USER" && utf8.RuneCountInString(value) < 3 {
		return InvalidParam{"MinIO needs a user of at least 3 characters"}
	}
	return nil
}

func IsSecretPassword(name string) bool { return strings.HasSuffix(name, "PASSWORD") }

// GenerateSecret makes 24 characters from an alphabet without look-alikes (no 0/O, 1/l/I): about 139 bits.
func GenerateSecret() (string, error) {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, 24)
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
}

// SecretUsedBy lists which providers read each secret, for the UI.
func SecretUsedBy(name string) []string {
	out := []string{}
	for _, def := range CatalogProviders {
		for _, kv := range def.SecretEnv {
			if kv.Value == name {
				out = append(out, def.ID)
				break
			}
		}
	}
	return out
}

// EnvForProvider is the environment a provider is started with: the process environment first (Doppler, .env, docker
// compose), and the secret store for whatever it leaves unset. Only the secrets that provider reads are decrypted.
func EnvForProvider(ctx context.Context, def ProviderDef, hostEnv map[string]string, store SecretStore) map[string]string {
	var missing []string
	for _, kv := range def.SecretEnv {
		if strings.TrimSpace(hostEnv[kv.Value]) == "" {
			missing = append(missing, kv.Value)
		}
	}
	if len(missing) == 0 || !store.Available() {
		return hostEnv
	}
	merged := make(map[string]string, len(hostEnv)+len(missing))
	for k, v := range hostEnv {
		merged[k] = v
	}
	for _, n := range missing {
		if v, ok, err := store.Get(ctx, n); err == nil && ok {
			merged[n] = v
		}
	}
	return merged
}

// UnavailableSecretStore is used where nothing can protect a secret: the answer to "can I store this?" is an honest no.
type UnavailableSecretStore struct{ Why string }

func (u UnavailableSecretStore) Available() bool                      { return false }
func (u UnavailableSecretStore) Reason() string                       { return u.Why }
func (u UnavailableSecretStore) Names(context.Context) []string       { return nil }
func (u UnavailableSecretStore) Delete(context.Context, string) error { return nil }
func (u UnavailableSecretStore) Get(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (u UnavailableSecretStore) Set(context.Context, string, string) error {
	return InvalidParam{u.Why}
}

// DpapiSecretStore is Windows DPAPI, bound to the current Windows user. The ciphertext is a file in the data folder.
// It only talks to PowerShell through a Shell, so it builds everywhere and its tests can fake the shell.
type DpapiSecretStore struct {
	store DataStore
	shell Shell
	mu    sync.Mutex
}

func NewDpapiSecretStore(store DataStore, shell Shell) *DpapiSecretStore {
	return &DpapiSecretStore{store: store, shell: shell}
}

const dpapiEntropy = "Nimbo.secrets.v1"

type dpapiBlobs struct {
	Values map[string]string `json:"values"`
}

func (d *DpapiSecretStore) blobs() dpapiBlobs {
	b := coreReadOrDefault(d.store, FileSecrets, func() dpapiBlobs { return dpapiBlobs{} })
	if b.Values == nil {
		b.Values = map[string]string{}
	}
	return b
}

func (d *DpapiSecretStore) Available() bool { return true }
func (d *DpapiSecretStore) Reason() string  { return "" }

func (d *DpapiSecretStore) Names(context.Context) []string {
	out := []string{}
	for n := range d.blobs().Values {
		if isSecretName(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (d *DpapiSecretStore) Get(ctx context.Context, name string) (string, bool, error) {
	blob, ok := d.blobs().Values[name]
	if !ok {
		return "", false, nil
	}
	plain, err := d.run(ctx, "[Convert]::ToBase64String([Security.Cryptography.ProtectedData]::Unprotect($b, $e, 'CurrentUser'))", blob)
	if err != nil {
		return "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(plain)
	if err != nil {
		return "", false, errors.New("Windows could not protect or read the secret")
	}
	return string(raw), true, nil
}

func (d *DpapiSecretStore) Set(ctx context.Context, name, value string) error {
	if err := ValidateSecret(name, value); err != nil {
		return err
	}
	blob, err := d.run(ctx, "[Convert]::ToBase64String([Security.Cryptography.ProtectedData]::Protect($b, $e, 'CurrentUser'))", base64.StdEncoding.EncodeToString([]byte(value)))
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	b := d.blobs()
	b.Values[name] = blob
	return coreWriteJSON(d.store, FileSecrets, b)
}

func (d *DpapiSecretStore) Delete(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	b := d.blobs()
	delete(b.Values, name)
	return coreWriteJSON(d.store, FileSecrets, b)
}

// run: the value goes in through standard input, so it never appears in a process list or a log.
func (d *DpapiSecretStore) run(ctx context.Context, operation, base64In string) (string, error) {
	script := "$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Security; " +
		"$b=[Convert]::FromBase64String([Console]::In.ReadToEnd().Trim()); " +
		"$e=[Text.Encoding]::UTF8.GetBytes('" + dpapiEntropy + "'); " + operation
	r := d.shell.Run(ctx, []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script}, &base64In, 30)
	if !r.OK() {
		// Never echo the output: it could carry the value.
		return "", errors.New("Windows could not protect or read the secret")
	}
	lines := strings.Split(strings.ReplaceAll(r.Output, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i]), nil
		}
	}
	return "", errors.New("Windows could not protect or read the secret")
}
