package nimbo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	updBase     = "https://github.com/Sebas1705/Nimbo/releases/download"
	updMSIBytes = "pretend this is an installer"
)

func updSha(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// updReleasesJSON builds a GitHub answer with the given versions. All of them are in the same shape the real API has,
// plus an asset from outside the repository that must be dropped.
func updReleasesJSON(versions ...string) string {
	parts := []string{}
	for _, v := range versions {
		parts = append(parts, fmt.Sprintf(`{"tag_name":"v%[1]s","draft":false,"prerelease":true,"body":"notes %[1]s","published_at":"2026-11-01T00:00:00Z","assets":[
            {"name":"Nimbo-%[1]s-windows-x64.msi","size":%[2]d,"browser_download_url":"%[3]s/v%[1]s/Nimbo-%[1]s-windows-x64.msi"},
            {"name":"nimbo-%[1]s.jar","size":10,"browser_download_url":"%[3]s/v%[1]s/nimbo-%[1]s.jar"},
            {"name":"SHA256SUMS","size":100,"browser_download_url":"%[3]s/v%[1]s/SHA256SUMS"},
            {"name":"evil.exe","size":1,"browser_download_url":"https://evil.example/evil.exe"}]}`, v, len(updMSIBytes), updBase))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type updFakeDownloader struct {
	releases string
	files    map[string]string
	fail     string
	asked    []string
}

func newUpdFake(releases string) *updFakeDownloader {
	return &updFakeDownloader{releases: releases, files: map[string]string{}}
}

func (d *updFakeDownloader) Text(_ context.Context, url string) (string, error) {
	d.asked = append(d.asked, url)
	if d.fail != "" {
		return "", UpdateError{d.fail}
	}
	if strings.Contains(url, "/releases?") {
		return d.releases, nil
	}
	if strings.HasSuffix(url, "/SHA256SUMS") {
		v := strings.SplitN(strings.SplitN(url, "/download/v", 2)[1], "/", 2)[0]
		if s, ok := d.files["SHA256SUMS-"+v]; ok {
			return s, nil
		}
		return fmt.Sprintf("%s  Nimbo-%s-windows-x64.msi\n", updSha(updMSIBytes), v), nil
	}
	return "", UpdateError{"unexpected " + url}
}

func (d *updFakeDownloader) File(_ context.Context, url, target string, _ int64) error {
	d.asked = append(d.asked, url)
	content, ok := d.files[url[strings.LastIndex(url, "/")+1:]]
	if !ok {
		content = updMSIBytes
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

type updFakeSettings struct {
	mode string
	last *int64
}

func (s *updFakeSettings) UpdateSettings() (string, *int64) { return s.mode, s.last }
func (s *updFakeSettings) MarkUpdateCheck(at int64)         { s.last = &at }

type updFakeLog struct {
	mu      sync.Mutex
	entries [][3]string
}

func (l *updFakeLog) Record(kind, subject, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, [3]string{kind, subject, detail})
}
func (l *updFakeLog) kinds() []string {
	out := []string{}
	for _, e := range l.entries {
		out = append(out, e[0])
	}
	return out
}

type updFakeStore struct {
	mu    sync.Mutex
	files map[string]string
}

func (s *updFakeStore) Read(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.files[name]
	return v, ok
}
func (s *updFakeStore) Write(name, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = text
	return nil
}

type updLaunches struct{ calls []string }

func (l *updLaunches) Launch(msi string, _ int64, _ string) error {
	l.calls = append(l.calls, msi)
	return nil
}

type updRig struct {
	u        *Updater
	dl       *updFakeDownloader
	settings *updFakeSettings
	log      *updFakeLog
	store    *updFakeStore
	dir      string
	now      *int64
	launches *updLaunches
	quits    *int
}

type updRigOpts struct {
	dist, version string
	noVersion     bool
	settings      *updFakeSettings
	dir           string
}

func newUpdRig(t *testing.T, dl *updFakeDownloader, o updRigOpts) *updRig {
	t.Helper()
	if o.dist == "" {
		o.dist = "desktop"
	}
	if o.version == "" && !o.noVersion {
		o.version = "0.2.1"
	}
	if o.settings == nil {
		o.settings = &updFakeSettings{mode: "auto"}
	}
	if o.dir == "" {
		o.dir = t.TempDir()
	}
	now := int64(1_000_000_000)
	quits := 0
	r := &updRig{dl: dl, settings: o.settings, log: &updFakeLog{}, store: &updFakeStore{files: map[string]string{}},
		dir: o.dir, now: &now, launches: &updLaunches{}, quits: &quits}
	r.u = NewUpdater(UpdateConfig{Version: o.version, Distribution: o.dist}, r.settings, r.log, r.store, dl, o.dir, UpdaterOptions{
		Installer: r.launches, Clock: func() int64 { return now }, PID: func() int64 { return 42 },
		Exe: func() string { return "C:/Nimbo/Nimbo.exe" }, Quit: func() { quits++ },
	})
	return r
}

func updInvalid(t *testing.T, err error) {
	t.Helper()
	var ip InvalidParam
	if !errors.As(err, &ip) {
		t.Fatalf("want InvalidParam, got %v", err)
	}
}

func updUpdateErr(t *testing.T, err error, contains string) {
	t.Helper()
	var ue UpdateError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, contains) {
		t.Fatalf("want UpdateError containing %q, got %v", contains, err)
	}
}

func updNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func updTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ---- SemVerTest ----------------------------------------------------------------------------------------------------

func TestSemVerParsesAndOrdersVersions(t *testing.T) {
	if v, ok := ParseSemVer("v0.2.1"); !ok || v != (SemVer{0, 2, 1, ""}) {
		t.Fatalf("v0.2.1 -> %v %v", v, ok)
	}
	if v, ok := ParseSemVer("1.0.0-beta.2"); !ok || v != (SemVer{1, 0, 0, "beta.2"}) {
		t.Fatalf("beta -> %v %v", v, ok)
	}
	for _, bad := range []string{"latest", "1.2", "", "1.0.0-", "1.0.0 extra", "123456.0.0"} {
		if _, ok := ParseSemVer(bad); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
	if _, ok := ParseSemVer("  v1.0.0\n"); !ok {
		t.Error("surrounding whitespace is trimmed")
	}
	in := []string{"0.2.1", "0.10.0", "0.3.0", "1.0.0-beta.2", "1.0.0-beta.10", "1.0.0", "1.0.0-alpha"}
	vs := []SemVer{}
	for _, s := range in {
		v, _ := ParseSemVer(s)
		vs = append(vs, v)
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) < 0 })
	got := []string{}
	for _, v := range vs {
		got = append(got, v.String())
	}
	want := []string{"0.2.1", "0.3.0", "0.10.0", "1.0.0-alpha", "1.0.0-beta.2", "1.0.0-beta.10", "1.0.0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestSemVerPreReleaseRules(t *testing.T) {
	cmp := func(a, b string) int {
		x, _ := ParseSemVer(a)
		y, _ := ParseSemVer(b)
		return x.Compare(y)
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-beta", "1.0.0-beta.1", -1}, // a shorter list is older
		{"1.0.0-beta.1", "1.0.0-beta", 1},
		{"1.0.0-1", "1.0.0-a", -1}, // numbers come before text
		{"1.0.0-a", "1.0.0-1", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-beta.2", "1.0.0-beta.2", 0},
		{"2.0.0-alpha", "1.9.9", 1},
	}
	for _, c := range cases {
		if got := cmp(c.a, c.b); got != c.want {
			t.Errorf("compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateInstallerNamesCarryTheVersion(t *testing.T) {
	if v, ok := UpdateVersionOf("Nimbo-0.3.0-windows-x64.msi"); !ok || v != (SemVer{0, 3, 0, ""}) {
		t.Fatalf("msi -> %v %v", v, ok)
	}
	if v, ok := UpdateVersionOf("nimbo-0.3.0.jar"); !ok || v != (SemVer{0, 3, 0, ""}) {
		t.Fatalf("jar -> %v %v", v, ok)
	}
	for _, bad := range []string{"evil.exe", "Nimbo-0.3.0-windows-x64.msi.exe"} {
		if _, ok := UpdateVersionOf(bad); ok {
			t.Errorf("%q is not an installer", bad)
		}
	}
}

func TestUpdateChecksumFilesAreParsed(t *testing.T) {
	h := strings.Repeat("a", 64)
	got := UpdateParseSums(h + "  x.msi\nnot a line\n" + strings.Repeat("b", 10) + "  y\n")
	if len(got) != 1 || got["x.msi"] != h {
		t.Fatalf("got %v", got)
	}
	got = UpdateParseSums(strings.ToUpper(h) + " *x.msi")
	if len(got) != 1 || got["x.msi"] != h {
		t.Fatalf("upper/star: %v", got)
	}
	if got := UpdateParseSums(h + "  x.msi\r\n"); got["x.msi"] != h {
		t.Fatalf("CRLF: %v", got)
	}
}

// ---- UpdaterTest ---------------------------------------------------------------------------------------------------

func TestUpdaterFindsTheNewestVersionAndTheInstallerForThisInstallation(t *testing.T) {
	r := newUpdRig(t, newUpdFake(updReleasesJSON("0.2.1", "0.3.0", "0.2.5")), updRigOpts{})
	s := r.u.Check(context.Background(), true)
	if s.Latest == nil || s.Latest.Version != "0.3.0" {
		t.Fatalf("latest %+v", s.Latest)
	}
	if !s.Available {
		t.Error("should be available")
	}
	if s.Asset == nil || s.Asset.Name != "Nimbo-0.3.0-windows-x64.msi" {
		t.Errorf("asset %+v", s.Asset)
	}
	if !s.CanInstall && runtime.GOOS == "windows" {
		t.Error("the desktop app on Windows can install")
	}
	for _, a := range s.Latest.Assets {
		if a.Name == "evil.exe" {
			t.Error("assets outside the repository's releases are dropped")
		}
	}
}

func TestUpdaterNothingIsOfferedWhenAlreadyCurrentOrNewer(t *testing.T) {
	ctx := context.Background()
	if newUpdRig(t, newUpdFake(updReleasesJSON("0.2.1")), updRigOpts{}).u.Check(ctx, true).Available {
		t.Error("same version")
	}
	if newUpdRig(t, newUpdFake(updReleasesJSON("0.2.1")), updRigOpts{version: "0.3.0"}).u.Check(ctx, true).Available {
		t.Error("running newer")
	}
	// A development build without a version sees every release as news.
	if !newUpdRig(t, newUpdFake(updReleasesJSON("0.2.1")), updRigOpts{noVersion: true}).u.Check(ctx, true).Available {
		t.Error("development build")
	}
}

func TestUpdaterJarInstallsPickTheJarAndDockerIsToldToPull(t *testing.T) {
	ctx := context.Background()
	jar := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{dist: "jar"}).u.Check(ctx, true)
	if jar.Asset == nil || jar.Asset.Name != "nimbo-0.3.0.jar" {
		t.Fatalf("jar asset %+v", jar.Asset)
	}
	if jar.Manual == nil || !strings.Contains(*jar.Manual, "nimbo-0.3.0.jar") {
		t.Errorf("jar manual %v", jar.Manual)
	}
	docker := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{dist: "docker"}).u.Check(ctx, true)
	if docker.Asset != nil {
		t.Error("docker has no asset")
	}
	if docker.Manual == nil || !strings.Contains(*docker.Manual, "docker compose pull") {
		t.Errorf("docker manual %v", docker.Manual)
	}
	if docker.CanInstall {
		t.Error("docker cannot install")
	}
}

func TestUpdaterManualModeNeverContactsAnyoneUnlessAsked(t *testing.T) {
	ctx := context.Background()
	dl := newUpdFake(updReleasesJSON("0.3.0"))
	r := newUpdRig(t, dl, updRigOpts{settings: &updFakeSettings{mode: "manual"}})
	if r.u.Check(ctx, false).Latest != nil {
		t.Error("manual mode must not check by itself")
	}
	if len(dl.asked) != 0 {
		t.Errorf("asked %v", dl.asked)
	}
	if r.u.Check(ctx, true).Latest == nil {
		t.Error("forced check must work")
	}
	if len(dl.asked) != 1 {
		t.Errorf("asked %v", dl.asked)
	}
}

func TestUpdaterAutomaticModeAsksAtMostOnceADay(t *testing.T) {
	ctx := context.Background()
	dl := newUpdFake(updReleasesJSON("0.3.0"))
	r := newUpdRig(t, dl, updRigOpts{})
	r.u.Check(ctx, false)
	r.u.Check(ctx, false)
	if len(dl.asked) != 1 {
		t.Fatalf("asked %d", len(dl.asked))
	}
	*r.now += UpdateDayMs + 1
	r.u.Check(ctx, false)
	if len(dl.asked) != 2 {
		t.Fatalf("asked %d", len(dl.asked))
	}
	if !strings.Contains(strings.Join(r.log.kinds(), ","), "update.check") {
		t.Error("checks are journaled")
	}
}

func TestUpdaterAFailedCheckKeepsTheReasonAndDoesNotCrash(t *testing.T) {
	ctx := context.Background()
	dl := newUpdFake("[]")
	dl.fail = "GitHub answered 403"
	s := newUpdRig(t, dl, updRigOpts{}).u.Check(ctx, true)
	if s.Error == nil || *s.Error != "GitHub answered 403" || s.Latest != nil {
		t.Fatalf("status %+v", s)
	}
	s = newUpdRig(t, newUpdFake("[]"), updRigOpts{}).u.Check(ctx, true)
	if s.Error == nil || *s.Error != "No published versions were found" {
		t.Fatalf("empty list: %v", s.Error)
	}
	s = newUpdRig(t, newUpdFake("not json"), updRigOpts{}).u.Check(ctx, true)
	if s.Error == nil || !strings.Contains(*s.Error, "Could not reach GitHub") {
		t.Fatalf("garbage: %v", s.Error)
	}
}

func TestUpdaterDraftsAndOddReleasesAreIgnored(t *testing.T) {
	// Extra: drafts, tags that are not versions and a non-list answer never win or crash.
	body := `[{"tag_name":"v9.9.9","draft":true,"assets":[]},{"tag_name":"nightly","assets":[]},{"tag_name":"v0.3.0","assets":[]},7]`
	s := newUpdRig(t, newUpdFake(body), updRigOpts{}).u.Check(context.Background(), true)
	if s.Latest == nil || s.Latest.Version != "0.3.0" {
		t.Fatalf("latest %+v", s.Latest)
	}
	s = newUpdRig(t, newUpdFake(`{"message":"rate limited"}`), updRigOpts{}).u.Check(context.Background(), true)
	if s.Error == nil || *s.Error != "No published versions were found" {
		t.Fatalf("object answer: %v", s.Error)
	}
}

func TestUpdaterDownloadVerifiesTheChecksumAndKeepsTheFile(t *testing.T) {
	ctx := context.Background()
	r := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{})
	r.u.Check(ctx, true)
	f, err := r.u.Download(ctx, "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Verified || f.SHA256 != updSha(updMSIBytes) || f.Source != "github" || f.Size != int64(len(updMSIBytes)) {
		t.Fatalf("file %+v", f)
	}
	got, _ := os.ReadFile(filepath.Join(r.dir, "updates", "Nimbo-0.3.0-windows-x64.msi"))
	if string(got) != updMSIBytes {
		t.Errorf("content %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "updates", "Nimbo-0.3.0-windows-x64.msi.part")); err == nil {
		t.Error(".part must be gone")
	}
	files := r.u.Files()
	if len(files) != 1 || files[0].Version != "0.3.0" {
		t.Fatalf("files %+v", files)
	}
}

func TestUpdaterAFileThatDoesNotMatchIsDeletedAndRefused(t *testing.T) {
	ctx := context.Background()
	dl := newUpdFake(updReleasesJSON("0.3.0"))
	dl.files["Nimbo-0.3.0-windows-x64.msi"] = "tampered"
	r := newUpdRig(t, dl, updRigOpts{})
	r.u.Check(ctx, true)
	_, err := r.u.Download(ctx, "0.3.0")
	updUpdateErr(t, err, "does not match")
	if left := updNames(t, filepath.Join(r.dir, "updates")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if len(r.u.Files()) != 0 {
		t.Error("nothing registered")
	}
}

func TestUpdaterDownloadRefusesUnknownVersionsMissingSumsAndUnlistedFiles(t *testing.T) {
	ctx := context.Background()
	dl := newUpdFake(updReleasesJSON("0.3.0"))
	r := newUpdRig(t, dl, updRigOpts{})
	_, err := r.u.Download(ctx, "0.3.0")
	updInvalid(t, err)
	r.u.Check(ctx, true)
	_, err = r.u.Download(ctx, "9.9.9")
	updInvalid(t, err)
	dl.files["SHA256SUMS-0.3.0"] = strings.Repeat("a", 64) + "  something-else.msi\n"
	_, err = r.u.Download(ctx, "0.3.0")
	updUpdateErr(t, err, "does not list")

	docker := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{dist: "docker"})
	docker.u.Check(ctx, true)
	_, err = docker.u.Download(ctx, "0.3.0")
	updInvalid(t, err)

	// Extra: a release without SHA256SUMS cannot be verified, so it is not downloaded.
	noSums := strings.Replace(updReleasesJSON("0.3.0"), `"name":"SHA256SUMS"`, `"name":"other.txt"`, 1)
	n := newUpdRig(t, newUpdFake(noSums), updRigOpts{})
	n.u.Check(ctx, true)
	_, err = n.u.Download(ctx, "0.3.0")
	updUpdateErr(t, err, "no SHA256SUMS")
}

func TestUpdaterImportChecksTheHashYouGiveOrTheSumsFileBesideIt(t *testing.T) {
	src := t.TempDir()
	installer := updTempFile(t, src, "Nimbo-0.4.0-windows-x64.msi", updMSIBytes)
	r := newUpdRig(t, newUpdFake("[]"), updRigOpts{})

	unverified, err := r.u.Import(installer, nil)
	if err != nil {
		t.Fatal(err)
	}
	if unverified.Verified || unverified.Version != "0.4.0" || unverified.Source != "file" {
		t.Fatalf("unverified %+v", unverified)
	}

	upper := strings.ToUpper(updSha(updMSIBytes))
	if f, err := r.u.Import(installer, &upper); err != nil || !f.Verified {
		t.Fatalf("given hash: %+v %v", f, err)
	}
	zeros := strings.Repeat("0", 64)
	_, err = r.u.Import(installer, &zeros)
	updUpdateErr(t, err, "does not match")
	xyz := "xyz"
	_, err = r.u.Import(installer, &xyz)
	updInvalid(t, err)

	updTempFile(t, src, "SHA256SUMS", strings.Repeat("1", 64)+"  "+filepath.Base(installer)+"\n")
	_, err = r.u.Import(installer, nil)
	updUpdateErr(t, err, "does not match")
	updTempFile(t, src, "SHA256SUMS", updSha(updMSIBytes)+"  "+filepath.Base(installer)+"\n")
	if f, err := r.u.Import(installer, nil); err != nil || !f.Verified {
		t.Fatalf("sums beside: %+v %v", f, err)
	}
	if len(r.u.Files()) != 1 {
		t.Errorf("one entry per name, got %+v", r.u.Files())
	}
}

func TestUpdaterImportRefusesOddFiles(t *testing.T) {
	src := t.TempDir()
	r := newUpdRig(t, newUpdFake("[]"), updRigOpts{})
	_, err := r.u.Import(filepath.Join(src, "missing.msi"), nil)
	updInvalid(t, err)
	odd := updTempFile(t, src, "setup.exe", "x")
	_, err = r.u.Import(odd, nil)
	updInvalid(t, err)
	if !strings.Contains(err.Error(), "does not look like a Nimbo installer") {
		t.Errorf("message %q", err)
	}
	_, err = r.u.Import(src, nil) // a folder
	updInvalid(t, err)
}

func TestUpdaterInstallNeedsConfirmationTheDesktopAppANewerVersionAndAnUntouchedFile(t *testing.T) {
	ctx := context.Background()
	r := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{})
	r.u.Check(ctx, true)
	if _, err := r.u.Download(ctx, "0.3.0"); err != nil {
		t.Fatal(err)
	}

	updInvalid(t, r.u.Install("0.3.0", false))
	if len(r.launches.calls) != 0 {
		t.Fatal("nothing launched without confirmation")
	}
	var ut UnknownTarget
	if err := r.u.Install("0.9.0", true); !errors.As(err, &ut) {
		t.Fatalf("want UnknownTarget, got %v", err)
	}

	updTempFile(t, filepath.Join(r.dir, "updates"), "Nimbo-0.3.0-windows-x64.msi", "swapped after the check")
	updUpdateErr(t, r.u.Install("0.3.0", true), "changed on disk")
	if len(r.launches.calls) != 0 {
		t.Fatal("a swapped file is never launched")
	}

	if _, err := r.u.Download(ctx, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.u.Install("0.3.0", true); err != nil {
		t.Fatal(err)
	}
	if len(r.launches.calls) != 1 || *r.quits != 1 {
		t.Fatalf("launches %v quits %d", r.launches.calls, *r.quits)
	}
	if !strings.Contains(strings.Join(r.log.kinds(), ","), "update.install") {
		t.Error("install is journaled")
	}
}

func TestUpdaterInstallIsRefusedOutsideTheDesktopAppAndForOlderVersions(t *testing.T) {
	jar := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{dist: "jar"})
	updInvalid(t, jar.u.Install("0.3.0", true))

	old := newUpdRig(t, newUpdFake("[]"), updRigOpts{version: "0.5.0"})
	installer := updTempFile(t, t.TempDir(), "Nimbo-0.3.0-windows-x64.msi", updMSIBytes)
	if _, err := old.u.Import(installer, nil); err != nil {
		t.Fatal(err)
	}
	err := old.u.Install("0.3.0", true)
	updInvalid(t, err)
	if !strings.Contains(err.Error(), "not newer") {
		t.Errorf("message %q", err)
	}
}

func TestUpdaterDefaultInstallerRefuses(t *testing.T) {
	// Extra: without an installer wired in, the updater says so instead of crashing.
	dir := t.TempDir()
	st := &updFakeStore{files: map[string]string{}}
	u := NewUpdater(UpdateConfig{Version: "0.2.1", Distribution: "desktop"}, &updFakeSettings{mode: "auto"}, &updFakeLog{}, st, newUpdFake("[]"), dir, UpdaterOptions{})
	installer := updTempFile(t, t.TempDir(), "Nimbo-0.3.0-windows-x64.msi", updMSIBytes)
	if _, err := u.Import(installer, nil); err != nil {
		t.Fatal(err)
	}
	updUpdateErr(t, u.Install("0.3.0", true), "cannot update itself")
}

func TestUpdaterCleanRemovesTheDownloadedInstallers(t *testing.T) {
	ctx := context.Background()
	r := newUpdRig(t, newUpdFake(updReleasesJSON("0.3.0")), updRigOpts{})
	r.u.Check(ctx, true)
	if _, err := r.u.Download(ctx, "0.3.0"); err != nil {
		t.Fatal(err)
	}
	n, err := r.u.Clean()
	if err != nil || n != 1 {
		t.Fatalf("clean %d %v", n, err)
	}
	if len(r.u.Files()) != 0 {
		t.Error("registry should be empty")
	}
	if left := updNames(t, filepath.Join(r.dir, "updates")); len(left) != 0 {
		t.Errorf("left: %v", left)
	}
}

func TestUpdaterFilesAreSortedNewestFirstAndSkipMissingOnes(t *testing.T) {
	// Extra: the registry order is by version, and entries whose file vanished are not listed.
	r := newUpdRig(t, newUpdFake("[]"), updRigOpts{})
	src := t.TempDir()
	for _, v := range []string{"0.3.0", "0.10.0", "0.4.0"} {
		if _, err := r.u.Import(updTempFile(t, src, "Nimbo-"+v+"-windows-x64.msi", v), nil); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(r.dir, "updates", "Nimbo-0.4.0-windows-x64.msi"))
	got := []string{}
	for _, f := range r.u.Files() {
		got = append(got, f.Version)
	}
	if strings.Join(got, ",") != "0.10.0,0.3.0" {
		t.Fatalf("files %v", got)
	}
}

func TestUpdaterADamagedRegistryIsSetAsideNotFatal(t *testing.T) {
	// Extra: like the Kotlin readOrDefault, a corrupt updates.json becomes updates.json.bad.
	r := newUpdRig(t, newUpdFake("[]"), updRigOpts{})
	r.store.files["updates.json"] = "{{ not json"
	if len(r.u.Files()) != 0 {
		t.Error("damaged registry reads as empty")
	}
	if r.store.files["updates.json.bad"] != "{{ not json" {
		t.Error("damaged copy must be kept")
	}
}

func TestUpdaterStatusJSONSendsEveryKey(t *testing.T) {
	// Extra: nullable fields are sent as null, never omitted.
	r := newUpdRig(t, newUpdFake("[]"), updRigOpts{noVersion: true})
	rec := httptest.NewRecorder()
	WriteJSON(rec, 200, r.u.Status())
	for _, key := range []string{`"current":null`, `"checkedAt":null`, `"latest":null`, `"asset":null`, `"files":[]`, `"error":null`, `"manual":null`, `"available":false`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("missing %s in %s", key, rec.Body.String())
		}
	}
}

func TestUpdaterPowershellScriptReceivesPathsAsParametersNotAsCode(t *testing.T) {
	dir := t.TempDir()
	// Only the script is written and read; launching is never exercised, so no test can install anything.
	path, err := NewWindowsInstaller(dir).WriteScript()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	script := string(b)
	for _, want := range []string{"param(", "msiexec.exe", "-LiteralPath", "'/passive'", "'/norestart'", "Wait-Process"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "Invoke-Expression") || strings.Contains(script, "iex ") {
		t.Error("the script must not evaluate strings")
	}
	if strings.Contains(script, dir) {
		t.Error("no path is pasted into the script")
	}
}

// ---- UpdateRoutesTest ----------------------------------------------------------------------------------------------

func updRouteRig(t *testing.T, dl *updFakeDownloader) (*http.ServeMux, *updRig) {
	t.Helper()
	r := newUpdRig(t, dl, updRigOpts{})
	mux := http.NewServeMux()
	NewUpdateRoutes(r.u).Register(mux)
	return mux, r
}

func updCall(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestUpdateRoutesTheWholeFlowWorksOverHTTPAndBadRequestsGetClearAnswers(t *testing.T) {
	mux, _ := updRouteRig(t, newUpdFake(updReleasesJSON("0.3.0")))

	if b := updCall(mux, "GET", "/api/updates", "").Body.String(); !strings.Contains(b, `"available":false`) {
		t.Fatal(b)
	}
	if b := updCall(mux, "POST", "/api/updates/check?force=true", "").Body.String(); !strings.Contains(b, `"available":true`) {
		t.Fatal(b)
	}
	ok := updCall(mux, "POST", "/api/updates/download", `{"version":"0.3.0"}`)
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), `"verified":true`) {
		t.Fatalf("download %d %s", ok.Code, ok.Body)
	}
	if c := updCall(mux, "POST", "/api/updates/install", `{"version":"0.3.0"}`).Code; c != 400 {
		t.Fatalf("no confirm: %d", c)
	}
	go1 := updCall(mux, "POST", "/api/updates/install", `{"version":"0.3.0","confirm":true}`)
	if go1.Code != 202 || !strings.Contains(go1.Body.String(), "Installing 0.3.0") {
		t.Fatalf("install %d %s", go1.Code, go1.Body)
	}
	if c := updCall(mux, "POST", "/api/updates/import", `{"path":"Z:/nope.msi"}`).Code; c != 400 {
		t.Fatalf("import missing: %d", c)
	}
	if c := updCall(mux, "DELETE", "/api/updates/files", "").Code; c != 200 {
		t.Fatalf("delete: %d", c)
	}
	// Extra: bodies that are missing a field or not JSON are the caller's mistake.
	for _, body := range []string{``, `{}`, `not json`} {
		if c := updCall(mux, "POST", "/api/updates/download", body).Code; c != 400 {
			t.Errorf("body %q -> %d", body, c)
		}
	}
}

func TestUpdateRoutesATamperedDownloadIsAGatewayErrorNotACrash(t *testing.T) {
	dl := newUpdFake(updReleasesJSON("0.3.0"))
	dl.files["Nimbo-0.3.0-windows-x64.msi"] = "tampered"
	mux, _ := updRouteRig(t, dl)
	updCall(mux, "POST", "/api/updates/check?force=true", "")
	r := updCall(mux, "POST", "/api/updates/download", `{"version":"0.3.0"}`)
	if r.Code != http.StatusBadGateway || !strings.Contains(r.Body.String(), "does not match") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestUpdateRoutesAnUpdateErrorFromImportIs422(t *testing.T) {
	// Extra: the Kotlin route answers 422 for a refused import.
	mux, _ := updRouteRig(t, newUpdFake("[]"))
	src := t.TempDir()
	installer := updTempFile(t, src, "Nimbo-0.4.0-windows-x64.msi", updMSIBytes)
	body := fmt.Sprintf(`{"path":%q,"sha256":%q}`, filepath.ToSlash(installer), strings.Repeat("0", 64))
	r := updCall(mux, "POST", "/api/updates/import", body)
	if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "does not match") {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
}

func TestUpdateRoutesAreAbsentWhenNoUpdaterIsConfigured(t *testing.T) {
	mux := http.NewServeMux()
	if c := updCall(mux, "GET", "/api/updates", "").Code; c != 404 {
		t.Fatalf("got %d", c)
	}
}
