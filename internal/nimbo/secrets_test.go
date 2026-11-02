package nimbo

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSecretNamesAreTheOnesProvidersRead(t *testing.T) {
	coreTrue(t, reflect.DeepEqual([]string{"NIMBO_AZURITE_ACCOUNTS", "NIMBO_MINIO_USER", "NIMBO_MINIO_PASSWORD", "NIMBO_POSTGRES_PASSWORD", "NIMBO_POSTGRES_USER", "NIMBO_POSTGRES_DB", "NIMBO_K3S_TOKEN", "NIMBO_REDIS_PASSWORD"}, SecretNames))
	coreTrue(t, reflect.DeepEqual([]string{"minio"}, SecretUsedBy("NIMBO_MINIO_USER")))
	coreTrue(t, IsSecretPassword("NIMBO_POSTGRES_PASSWORD") && !IsSecretPassword("NIMBO_MINIO_USER"))
}

func TestSecretValidationExplains(t *testing.T) {
	_, unknown := ValidateSecret("HOME", "x").(UnknownTarget)
	coreTrue(t, unknown)
	for _, c := range [][2]string{
		{"NIMBO_POSTGRES_PASSWORD", ""}, {"NIMBO_POSTGRES_PASSWORD", strings.Repeat("x", 129)},
		{"NIMBO_POSTGRES_PASSWORD", "line\nbreak"}, {"NIMBO_MINIO_PASSWORD", "short"}, {"NIMBO_MINIO_USER", "ab"},
	} {
		_, bad := ValidateSecret(c[0], c[1]).(InvalidParam)
		coreTrue(t, bad, c[0], c[1])
	}
	coreTrue(t, ValidateSecret("NIMBO_MINIO_PASSWORD", "long-enough") == nil)
	coreTrue(t, ValidateSecret("NIMBO_POSTGRES_PASSWORD", "x") == nil)
}

func TestGeneratedPasswords(t *testing.T) {
	a, _ := GenerateSecret()
	b, _ := GenerateSecret()
	coreEq(t, 24, len(a))
	coreTrue(t, a != b)
	coreTrue(t, !strings.ContainsAny(a, "0OIl1"))
	coreTrue(t, ValidateSecret("NIMBO_MINIO_PASSWORD", a) == nil)
}

func TestEnvironmentWinsOverTheStoreAndOnlyMissingIsRead(t *testing.T) {
	store := newCoreMemorySecrets()
	_ = store.Set(nil, "NIMBO_POSTGRES_PASSWORD", "from-store")
	_ = store.Set(nil, "NIMBO_MINIO_PASSWORD", "minio-from-store")
	pg := *CatalogFind("postgres")
	ctx := context.Background()

	coreEq(t, "from-env", EnvForProvider(ctx, pg, map[string]string{"NIMBO_POSTGRES_PASSWORD": "from-env"}, store)["NIMBO_POSTGRES_PASSWORD"])
	merged := EnvForProvider(ctx, pg, map[string]string{"OTHER": "1"}, store)
	coreEq(t, "from-store", merged["NIMBO_POSTGRES_PASSWORD"])
	_, leaked := merged["NIMBO_MINIO_PASSWORD"]
	coreTrue(t, !leaked, "a provider only gets the secrets it reads")
	coreEq(t, "1", merged["OTHER"])
}

func TestUnavailableStoreChangesNothing(t *testing.T) {
	env := map[string]string{"A": "b"}
	off := newCoreMemorySecrets()
	off.avail = false
	coreTrue(t, reflect.DeepEqual(env, EnvForProvider(context.Background(), *CatalogFind("postgres"), env, off)))
	none := UnavailableSecretStore{Why: "because"}
	ctx := context.Background()
	coreTrue(t, !none.Available() && len(none.Names(ctx)) == 0)
	_, ok, _ := none.Get(ctx, "NIMBO_MINIO_USER")
	coreTrue(t, !ok)
	_, bad := none.Set(ctx, "NIMBO_MINIO_USER", "abcd").(InvalidParam)
	coreTrue(t, bad)
}

// coreFakePowerShell records what it was asked and never sees clear text in arguments. Reversing the bytes is its
// own inverse, so Protect followed by Unprotect gives the value back.
type coreFakePowerShell struct {
	calls []struct {
		args  []string
		stdin *string
	}
}

func (f *coreFakePowerShell) Run(_ context.Context, args []string, stdin *string, _ int) ExecResult {
	f.calls = append(f.calls, struct {
		args  []string
		stdin *string
	}{args, stdin})
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(*stdin))
	for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
		raw[i], raw[j] = raw[j], raw[i]
	}
	return ExecResult{0, "noise\n" + base64.StdEncoding.EncodeToString(raw)}
}

func TestDpapiRoundTripEncryptedAndNeverInArguments(t *testing.T) {
	shell, data := &coreFakePowerShell{}, NewMemoryDataStore()
	store := NewDpapiSecretStore(data, shell)
	ctx := context.Background()
	coreTrue(t, store.Set(ctx, "NIMBO_POSTGRES_PASSWORD", "s3cret-value") == nil)
	v, ok, err := store.Get(ctx, "NIMBO_POSTGRES_PASSWORD")
	coreTrue(t, err == nil && ok)
	coreEq(t, "s3cret-value", v)
	coreTrue(t, reflect.DeepEqual([]string{"NIMBO_POSTGRES_PASSWORD"}, store.Names(ctx)))

	onDisk, _, _ := data.Read(FileSecrets)
	coreTrue(t, !strings.Contains(onDisk, "s3cret-value"))
	coreTrue(t, !strings.Contains(onDisk, base64.StdEncoding.EncodeToString([]byte("s3cret-value"))), "stored text must be the protected blob")
	for _, c := range shell.calls {
		for _, a := range c.args {
			coreTrue(t, !strings.Contains(a, "s3cret-value"), "secret leaked into the command line")
		}
		coreTrue(t, c.stdin != nil)
	}
	last := shell.calls[0].args[len(shell.calls[0].args)-1]
	coreContains(t, last, "CurrentUser")
}

func TestDpapiDeleteAndUnknown(t *testing.T) {
	store := NewDpapiSecretStore(NewMemoryDataStore(), &coreFakePowerShell{})
	ctx := context.Background()
	_, ok, _ := store.Get(ctx, "NIMBO_MINIO_USER")
	coreTrue(t, !ok)
	_ = store.Set(ctx, "NIMBO_MINIO_USER", "admin")
	_ = store.Delete(ctx, "NIMBO_MINIO_USER")
	coreEq(t, 0, len(store.Names(ctx)))
	_, bad := store.Set(ctx, "NIMBO_MINIO_PASSWORD", "short").(InvalidParam)
	coreTrue(t, bad)
}

type coreFailingShell struct{}

func (coreFailingShell) Run(context.Context, []string, *string, int) ExecResult {
	return ExecResult{1, "boom s3cret-value"}
}

func TestDpapiFailureDoesNotEchoTheValue(t *testing.T) {
	store := NewDpapiSecretStore(NewMemoryDataStore(), coreFailingShell{})
	err := store.Set(context.Background(), "NIMBO_POSTGRES_PASSWORD", "s3cret-value")
	coreTrue(t, err != nil && !strings.Contains(err.Error(), "s3cret-value"))
	var inv InvalidParam
	coreTrue(t, !errors.As(err, &inv))
}

// The real thing, only where it exists.
func TestRealDpapiRoundTripOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI only exists on Windows")
	}
	dir := t.TempDir()
	const value = "päss wörd 123 \"quoted\" $dollar"
	ctx := context.Background()
	store := NewDpapiSecretStore(NewFileDataStore(dir), ProcessShell{Bin: "powershell.exe"})
	coreTrue(t, store.Set(ctx, "NIMBO_POSTGRES_PASSWORD", value) == nil)
	got, ok, err := store.Get(ctx, "NIMBO_POSTGRES_PASSWORD")
	coreTrue(t, err == nil && ok)
	coreEq(t, value, got)
	raw, _ := os.ReadFile(filepath.Join(dir, FileSecrets))
	coreTrue(t, !strings.Contains(string(raw), "päss"))
	// Another instance over the same folder reads it back, as after a restart.
	again, _, _ := NewDpapiSecretStore(NewFileDataStore(dir), ProcessShell{Bin: "powershell.exe"}).Get(ctx, "NIMBO_POSTGRES_PASSWORD")
	coreEq(t, value, again)
	// DefaultSecretStore picks it on Windows when the data is in a folder.
	coreTrue(t, DefaultSecretStore(AppDataOnDisk(dir)).Available())
	coreTrue(t, !DefaultSecretStore(NewAppData(nil, nil)).Available())
}
