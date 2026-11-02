package nimbo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

// SecretNames are the settings of the built-in emulators. AllSecretNames also has the ones of registered images.
var SecretNames = secretNames(CatalogProviders)

// AllSecretNames are the only settings Nimbo knows what to do with: the ones emulators read when they start, built-in
// first, then those of the images a person registered.
func AllSecretNames() []string { return secretNames(AllProviders()) }

func secretNames(defs []ProviderDef) []string {
	var out []string
	seen := map[string]bool{}
	for _, def := range defs {
		for _, v := range def.Vars {
			if !seen[v.Name] {
				seen[v.Name] = true
				out = append(out, v.Name)
			}
		}
	}
	return out
}

func isSecretName(name string) bool { return varSpecByName(name) != nil }

// varSpecByName finds the description of a setting by its NIMBO_* name.
func varSpecByName(name string) *VarSpec {
	all := AllProviders()
	for i := range all {
		for j := range all[i].Vars {
			if all[i].Vars[j].Name == name {
				return &all[i].Vars[j]
			}
		}
	}
	return nil
}

var (
	secretControlRe = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	sqlNameRe       = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	azuriteAccount  = regexp.MustCompile(`^[a-z0-9]{3,24}:[A-Za-z0-9+/]{16,}={0,2}(:[A-Za-z0-9+/]{16,}={0,2})?$`)
)

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
	switch name {
	case "NIMBO_MINIO_PASSWORD":
		// MinIO refuses to start with a short root password, and it is better to hear it now than from a failed container.
		if utf8.RuneCountInString(value) < 8 {
			return InvalidParam{"MinIO needs a password of at least 8 characters"}
		}
	case "NIMBO_MINIO_USER":
		if utf8.RuneCountInString(value) < 3 {
			return InvalidParam{"MinIO needs a user of at least 3 characters"}
		}
	case "NIMBO_REDIS_PASSWORD":
		if strings.ContainsAny(value, " \t") || utf8.RuneCountInString(value) < 8 {
			return InvalidParam{"Redis needs a password of at least 8 characters, without spaces"}
		}
	case "NIMBO_POSTGRES_USER", "NIMBO_POSTGRES_DB":
		if !sqlNameRe.MatchString(value) {
			return InvalidParam{"Use lowercase letters, digits and underscores, starting with a letter or underscore"}
		}
	case "NIMBO_AZURITE_ACCOUNTS":
		// account:key, optionally several separated by ";". The key is base64, as Azure storage keys are.
		for _, acct := range strings.Split(value, ";") {
			if !azuriteAccount.MatchString(acct) {
				return InvalidParam{"Use name:key (name of 3 to 24 lowercase letters and digits, key in base64), several separated by ';'"}
			}
		}
	}
	return nil
}

// IsSecretPassword says whether a setting is a secret (masked, typed into a password field).
func IsSecretPassword(name string) bool {
	if v := varSpecByName(name); v != nil {
		return v.Secret
	}
	return strings.HasSuffix(name, "PASSWORD")
}

const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateSecret makes 24 characters from an alphabet without look-alikes (no 0/O, 1/l/I): about 139 bits.
func GenerateSecret() (string, error) {
	out := make([]byte, 24)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out), nil
}

// GenerateFor makes a value for a setting the way its description says, or returns "" when it is not generated.
func GenerateFor(v VarSpec) (string, error) {
	switch v.Generate {
	case "password":
		return GenerateSecret()
	case "token":
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return hex.EncodeToString(b), nil
	case "azurite":
		// A 64-byte key in base64, like the ones Azure gives, for a single account called "nimbo".
		b := make([]byte, 64)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return "nimbo:" + base64.StdEncoding.EncodeToString(b), nil
	}
	return "", nil
}

// SecretUsedBy lists which providers read each setting, for the UI.
func SecretUsedBy(name string) []string {
	out := []string{}
	for _, def := range AllProviders() {
		for _, v := range def.Vars {
			if v.Name == name {
				out = append(out, def.ID)
				break
			}
		}
	}
	return out
}

// VarSource says where the value of a setting comes from.
type VarSource string

const (
	SourceEnvironment VarSource = "environment" // Nimbo's own environment (Doppler, .env): it wins
	SourceStored      VarSource = "stored"      // the secret store
	SourceUnset       VarSource = "unset"
)

// ResolvedVar is a setting with its current value. The value never leaves the server except through a reveal and the
// environment of the container it is for.
type ResolvedVar struct {
	Spec   VarSpec
	Value  string
	Source VarSource
}

// ResolveVars looks up every setting of a provider: the process environment first, then the secret store.
func ResolveVars(ctx context.Context, def ProviderDef, hostEnv map[string]string, store SecretStore) []ResolvedVar {
	out := make([]ResolvedVar, 0, len(def.Vars))
	for _, v := range def.Vars {
		r := ResolvedVar{Spec: v, Source: SourceUnset}
		if val := strings.TrimSpace(hostEnv[v.Name]); val != "" {
			r.Value, r.Source = hostEnv[v.Name], SourceEnvironment
		} else if store != nil && store.Available() {
			if val, ok, err := store.Get(ctx, v.Name); err == nil && ok {
				r.Value, r.Source = val, SourceStored
			}
		}
		out = append(out, r)
	}
	return out
}

// VarSources says where each setting of a provider comes from without decrypting anything: the process environment, or
// the names the secret store holds. It is cheap enough to run on every refresh of the page.
func VarSources(ctx context.Context, def ProviderDef, hostEnv map[string]string, store SecretStore) map[string]VarSource {
	stored := map[string]bool{}
	if store != nil && store.Available() {
		for _, n := range store.Names(ctx) {
			stored[n] = true
		}
	}
	out := make(map[string]VarSource, len(def.Vars))
	for _, v := range def.Vars {
		switch {
		case strings.TrimSpace(hostEnv[v.Name]) != "":
			out[v.Name] = SourceEnvironment
		case stored[v.Name]:
			out[v.Name] = SourceStored
		default:
			out[v.Name] = SourceUnset
		}
	}
	return out
}

// MissingRequired are the required settings of a provider that have no value yet.
func MissingRequired(def ProviderDef, sources map[string]VarSource) []string {
	out := []string{}
	for _, v := range def.Vars {
		if v.Required && sources[v.Name] == SourceUnset {
			out = append(out, v.Name)
		}
	}
	return out
}

// EnvForProvider is the environment a provider is started with: the process environment first (Doppler, .env, docker
// compose), and the secret store for whatever it leaves unset. Only the settings that provider reads are decrypted.
func EnvForProvider(ctx context.Context, def ProviderDef, hostEnv map[string]string, store SecretStore) map[string]string {
	missing := false
	for _, v := range def.Vars {
		if strings.TrimSpace(hostEnv[v.Name]) == "" {
			missing = true
		}
	}
	if !missing || !store.Available() {
		return hostEnv
	}
	merged := make(map[string]string, len(hostEnv)+len(def.Vars))
	for k, v := range hostEnv {
		merged[k] = v
	}
	for _, r := range ResolveVars(ctx, def, hostEnv, store) {
		if r.Source == SourceStored {
			merged[r.Spec.Name] = r.Value
		}
	}
	return merged
}

// InitializeVars gives a value to every setting of a provider that has none: generated passwords and tokens, the
// defaults of users and names. Required settings are always filled; optional ones only when asked. Nothing that is
// already set (in the environment or stored) is touched. It returns the names it created.
func InitializeVars(ctx context.Context, def ProviderDef, hostEnv map[string]string, store SecretStore, optional bool) ([]string, error) {
	if !store.Available() {
		return nil, InvalidParam{store.Reason()}
	}
	created := []string{}
	for _, r := range ResolveVars(ctx, def, hostEnv, store) {
		if r.Source != SourceUnset || (!r.Spec.Required && !optional) {
			continue
		}
		value, err := GenerateFor(r.Spec)
		if err != nil {
			return created, err
		}
		if value == "" {
			value = r.Spec.Default
		}
		if value == "" {
			continue
		}
		if err := store.Set(ctx, r.Spec.Name, value); err != nil {
			return created, err
		}
		created = append(created, r.Spec.Name)
	}
	return created, nil
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
