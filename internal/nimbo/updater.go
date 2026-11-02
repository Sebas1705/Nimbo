package nimbo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- DTOs (same JSON as the Kotlin ones) ---------------------------------------------------------------------------

type UpdateAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

type UpdateRelease struct {
	Tag         string        `json:"tag"`
	Version     string        `json:"version"`
	Notes       string        `json:"notes"`
	PublishedAt *string       `json:"publishedAt"`
	Assets      []UpdateAsset `json:"assets"`
}

// UpdateFile is a file on disk that Nimbo could install. Verified is true only when its SHA-256 matched a published
// checksum.
type UpdateFile struct {
	Version  string `json:"version"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	Verified bool   `json:"verified"`
	Source   string `json:"source"`
	Size     int64  `json:"size"`
	At       int64  `json:"at"`
}

type UpdateStatus struct {
	Current      *string        `json:"current"`
	Distribution string         `json:"distribution"`
	Mode         string         `json:"mode"`
	CheckedAt    *int64         `json:"checkedAt"`
	Latest       *UpdateRelease `json:"latest"`
	Available    bool           `json:"available"`
	// Asset is the file of the latest release that fits how Nimbo was installed; null when there is none (Docker
	// updates by pulling).
	Asset      *UpdateAsset `json:"asset"`
	Files      []UpdateFile `json:"files"`
	CanInstall bool         `json:"canInstall"`
	Error      *string      `json:"error"`
	// Manual is what to do by hand when Nimbo cannot update itself.
	Manual *string `json:"manual"`
}

type DownloadRequestDto struct {
	Version string `json:"version"`
}

type ImportUpdateDto struct {
	Path   string  `json:"path"`
	SHA256 *string `json:"sha256"`
}

type InstallRequestDto struct {
	Version string `json:"version"`
	Confirm bool   `json:"confirm"`
}

// ---- What the updater needs from the rest of the app (the integrator wires these) ----------------------------------

// UpdateConfig is the part of the app configuration the updater reads. An empty Version means a development build.
type UpdateConfig struct {
	Version      string
	Distribution string // "desktop", "binary" or "docker"
}

// updateSettings is the settings view: the update mode ("auto" or "manual"), when GitHub was last asked (nil when
// never) and a way to remember that it was asked now.
type updateSettings interface {
	UpdateSettings() (mode string, lastCheck *int64)
	MarkUpdateCheck(at int64)
}

// updateActivity is the activity log. An empty detail means none.
type updateActivity interface {
	Record(kind, subject, detail string)
}

// updateStore keeps small text documents by name in the data folder (the registry of accepted installers is
// `updates.json`). Read says whether the document exists.
type updateStore interface {
	Read(name string) (string, bool)
	Write(name, text string) error
}

// ---- Network and installer seams -----------------------------------------------------------------------------------

// Downloader reads from the network. Faked in tests.
type Downloader interface {
	Text(ctx context.Context, url string) (string, error)
	File(ctx context.Context, url, target string, maxBytes int64) error
}

// Installer hands an installer to Windows and leaves. Faked in tests: nothing in the test suite ever runs an
// installer. exe is empty when unknown.
type Installer interface {
	Launch(msi string, nimboPid int64, exe string) error
}

// HTTPDownloader is the real Downloader: HTTP/1.1, redirects followed, a User-Agent GitHub accepts.
type HTTPDownloader struct{ client *http.Client }

func NewHTTPDownloader() *HTTPDownloader {
	tr := &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		// A non-nil empty map turns HTTP/2 off, like the Kotlin client pinned to HTTP/1.1.
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &HTTPDownloader{client: &http.Client{Transport: tr, Timeout: 15 * time.Minute}}
}

func (d *HTTPDownloader) get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Nimbo-updater")
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream, */*")
	return d.client.Do(req)
}

func (d *HTTPDownloader) Text(ctx context.Context, rawURL string) (string, error) {
	resp, err := d.get(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		path := rawURL
		if u, perr := url.Parse(rawURL); perr == nil {
			path = u.Path
		}
		return "", UpdateError{fmt.Sprintf("GitHub answered %d for %s", resp.StatusCode, path)}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

// File streams the body to target and stops as soon as it grows past maxBytes.
func (d *HTTPDownloader) File(ctx context.Context, rawURL, target string, maxBytes int64) error {
	resp, err := d.get(ctx, rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return UpdateError{fmt.Sprintf("The download answered %d", resp.StatusCode)}
	}
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, maxBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxBytes {
		return UpdateError{"The download is bigger than expected"}
	}
	return nil
}

// WindowsInstaller runs the PowerShell helper that waits for Nimbo to exit, runs the MSI and relaunches the exe.
type WindowsInstaller struct{ ScriptDir string }

func NewWindowsInstaller(scriptDir string) *WindowsInstaller {
	return &WindowsInstaller{ScriptDir: scriptDir}
}

func (w *WindowsInstaller) Launch(msi string, nimboPid int64, exe string) error {
	script, err := w.WriteScript()
	if err != nil {
		return err
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass",
		"-File", script, "-Msi", msi, "-NimboPid", strconv.FormatInt(nimboPid, 10), "-Exe", exe)
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// The helper outlives Nimbo, so nothing waits for it.
	_ = cmd.Process.Release()
	return nil
}

// installScript receives its paths as parameters, never pasted into the script, so a strange file name cannot
// become a command.
const installScript = `param([string]$Msi, [int]$NimboPid, [string]$Exe)
$ErrorActionPreference = 'Continue'
try { Wait-Process -Id $NimboPid -Timeout 60 -ErrorAction Stop } catch {}
Start-Process -FilePath msiexec.exe -ArgumentList @('/i', $Msi, '/passive', '/norestart') -Wait
if ($Exe -and (Test-Path -LiteralPath $Exe)) { Start-Process -FilePath $Exe }`

// WriteScript writes the helper and returns its path. It never runs it.
func (w *WindowsInstaller) WriteScript() (string, error) {
	if err := os.MkdirAll(w.ScriptDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(w.ScriptDir, "install-update.ps1")
	return path, os.WriteFile(path, []byte(installScript), 0o644)
}

// ---- The updater ---------------------------------------------------------------------------------------------------

// UpdateDayMs is the shortest time between two automatic checks.
const UpdateDayMs = int64(24 * 60 * 60 * 1000)

const updateRegistry = "updates.json"

// UpdaterOptions are the parts tests replace. Every zero value picks the real thing.
type UpdaterOptions struct {
	// Installer defaults to one that refuses: this installation cannot update itself.
	Installer Installer
	Clock     func() int64  // milliseconds since the epoch
	PID       func() int64  // Nimbo's own process id
	Exe       func() string // path of the running executable, "" when unknown
	Quit      func()        // called after the installer has been launched so Nimbo can quit and let Windows replace it
	Repo      string        // defaults to Sebas1705/Nimbo
}

type updRefusingInstaller struct{}

func (updRefusingInstaller) Launch(string, int64, string) error {
	return UpdateError{"This installation cannot update itself"}
}

// Updater looks for new versions, downloads them checking their SHA-256 against the release's SHA256SUMS, and, only
// when you confirm, hands the installer over. It never contacts anyone in manual mode, and only ever talks to this
// repository.
type Updater struct {
	cfg        UpdateConfig
	settings   updateSettings
	log        updateActivity
	store      updateStore
	downloader Downloader
	folder     string
	installer  Installer
	clock      func() int64
	pid        func() int64
	exe        func() string
	quit       func()
	repo       string

	lock    sync.Mutex // one check/download/import/install at a time
	regLock sync.Mutex // the registry file

	stateLock sync.RWMutex
	latest    *UpdateRelease
	lastError *string
}

// NewUpdater builds the updater. folder is the data folder; downloads go to <folder>/updates.
func NewUpdater(cfg UpdateConfig, settings updateSettings, log updateActivity, store updateStore, dl Downloader, folder string, opts UpdaterOptions) *Updater {
	u := &Updater{cfg: cfg, settings: settings, log: log, store: store, downloader: dl, folder: folder,
		installer: opts.Installer, clock: opts.Clock, pid: opts.PID, exe: opts.Exe, quit: opts.Quit, repo: opts.Repo}
	if u.installer == nil {
		u.installer = updRefusingInstaller{}
	}
	if u.clock == nil {
		u.clock = func() int64 { return time.Now().UnixMilli() }
	}
	if u.pid == nil {
		u.pid = func() int64 { return int64(os.Getpid()) }
	}
	if u.exe == nil {
		u.exe = func() string { p, _ := os.Executable(); return p }
	}
	if u.quit == nil {
		u.quit = func() {}
	}
	if u.repo == "" {
		u.repo = "Sebas1705/Nimbo"
	}
	return u
}

func (u *Updater) releasesURL() string {
	return "https://api.github.com/repos/" + u.repo + "/releases?per_page=10"
}
func (u *Updater) downloadPrefix() string {
	return "https://github.com/" + u.repo + "/releases/download/"
}
func (u *Updater) updatesDir() string { return filepath.Join(u.folder, "updates") }

func (u *Updater) current() (SemVer, bool) { return ParseSemVer(u.cfg.Version) }

func updStr(s string) *string { return &s }

// ---- status --------------------------------------------------------------------------------------------------------

func (u *Updater) Status() UpdateStatus {
	u.stateLock.RLock()
	release, lastErr := u.latest, u.lastError
	u.stateLock.RUnlock()

	newer := false
	if release != nil {
		if v, ok := ParseSemVer(release.Version); ok {
			cur, hasCur := u.current()
			newer = !hasCur || v.Compare(cur) > 0
		}
	}
	var asset *UpdateAsset
	if release != nil && newer {
		asset = u.pickAsset(release)
	}
	mode, last := u.settings.UpdateSettings()
	var current *string
	if u.cfg.Version != "" {
		current = updStr(u.cfg.Version)
	}
	var version string
	if release != nil {
		version = release.Version
	}
	return UpdateStatus{
		Current: current, Distribution: u.cfg.Distribution, Mode: mode, CheckedAt: last,
		Latest: release, Available: newer, Asset: asset, Files: u.Files(),
		CanInstall: u.cfg.Distribution == "desktop" && runtime.GOOS == "windows",
		Error:      lastErr, Manual: u.manualSteps(version, newer),
	}
}

func (u *Updater) manualSteps(version string, newer bool) *string {
	switch {
	case !newer:
		return nil
	case u.cfg.Distribution == "docker":
		return updStr("docker compose pull && docker compose up -d")
	case u.cfg.Distribution == "binary":
		return updStr("Replace your Nimbo with " + binaryAssetName(version) + " from the downloaded file and start it again.")
	}
	return nil
}

// ---- checking ------------------------------------------------------------------------------------------------------

// Check asks GitHub for the newest release. With force false it respects the update mode and asks at most once a day.
func (u *Updater) Check(ctx context.Context, force bool) UpdateStatus {
	u.lock.Lock()
	defer u.lock.Unlock()
	mode, last := u.settings.UpdateSettings()
	due := last == nil || u.clock()-*last >= UpdateDayMs
	if !force && (mode == "manual" || !due) {
		return u.Status()
	}
	var failure *string
	text, err := u.downloader.Text(ctx, u.releasesURL())
	if err == nil {
		var release *UpdateRelease
		release, err = u.newest(text)
		if err == nil {
			u.stateLock.Lock()
			u.latest = release
			u.stateLock.Unlock()
			if release == nil {
				failure = updStr("No published versions were found")
			}
		}
	}
	if err != nil {
		var ue UpdateError
		if errors.As(err, &ue) {
			failure = updStr(ue.Msg)
		} else {
			failure = updStr("Could not reach GitHub: " + err.Error())
		}
	}
	u.stateLock.Lock()
	u.lastError = failure
	release := u.latest
	u.stateLock.Unlock()

	u.settings.MarkUpdateCheck(u.clock())
	detail := ""
	if failure != nil {
		detail = *failure
	} else if release != nil {
		detail = release.Version
	}
	u.log.Record("update.check", "github", detail)
	return u.Status()
}

// updJSONString reads a JSON string field; ok is false for anything else.
func updJSONString(o map[string]any, key string) (string, bool) {
	s, ok := o[key].(string)
	return s, ok
}

// newest picks the highest version among the published (non-draft) releases; nil when there is none or the answer
// is not a list.
func (u *Updater) newest(text string) (*UpdateRelease, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	array, ok := root.([]any)
	if !ok {
		return nil, nil
	}
	var best *UpdateRelease
	var bestV SemVer
	for _, el := range array {
		o, ok := el.(map[string]any)
		if !ok {
			continue
		}
		if draft, _ := o["draft"].(bool); draft {
			continue
		}
		tag, ok := updJSONString(o, "tag_name")
		if !ok {
			continue
		}
		v, ok := ParseSemVer(tag)
		if !ok {
			continue
		}
		assets := []UpdateAsset{}
		rawAssets, _ := o["assets"].([]any)
		for _, a := range rawAssets {
			ao, ok := a.(map[string]any)
			if !ok {
				continue
			}
			assetURL, ok1 := updJSONString(ao, "browser_download_url")
			name, ok2 := updJSONString(ao, "name")
			if !ok1 || !ok2 || !strings.HasPrefix(assetURL, u.downloadPrefix()) {
				continue
			}
			var size int64
			if n, ok := ao["size"].(json.Number); ok {
				size, _ = n.Int64()
			}
			assets = append(assets, UpdateAsset{name, size, assetURL})
		}
		body, _ := updJSONString(o, "body")
		if r := []rune(body); len(r) > 4000 {
			body = string(r[:4000])
		}
		var published *string
		if p, ok := updJSONString(o, "published_at"); ok {
			published = &p
		}
		if best == nil || v.Compare(bestV) > 0 {
			best, bestV = &UpdateRelease{tag, v.String(), body, published, assets}, v
		}
	}
	return best, nil
}

func (u *Updater) pickAsset(r *UpdateRelease) *UpdateAsset {
	var wanted string
	switch u.cfg.Distribution {
	case "desktop":
		wanted = "Nimbo-" + r.Version + "-windows-x64.msi"
	case "binary":
		wanted = binaryAssetName(r.Version)
	default:
		return nil
	}
	for i := range r.Assets {
		if r.Assets[i].Name == wanted {
			a := r.Assets[i]
			return &a
		}
	}
	return nil
}

// ---- downloading ---------------------------------------------------------------------------------------------------

// Download fetches the asset for this installation and keeps it only if its SHA-256 matches the release's SHA256SUMS.
func (u *Updater) Download(ctx context.Context, version string) (UpdateFile, error) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.stateLock.RLock()
	release := u.latest
	u.stateLock.RUnlock()
	if release == nil || release.Version != version {
		return UpdateFile{}, InvalidParam{"Check for updates first: " + version + " is not the latest known version"}
	}
	asset := u.pickAsset(release)
	if asset == nil {
		return UpdateFile{}, InvalidParam{"There is nothing to download for this installation (" + u.cfg.Distribution + ")"}
	}
	if !strings.HasPrefix(asset.URL, u.downloadPrefix()) {
		return UpdateFile{}, UpdateError{"Refusing to download from outside " + u.repo}
	}
	var sums *UpdateAsset
	for i := range release.Assets {
		if release.Assets[i].Name == "SHA256SUMS" {
			sums = &release.Assets[i]
			break
		}
	}
	if sums == nil {
		return UpdateFile{}, UpdateError{"This release publishes no SHA256SUMS, so it cannot be verified"}
	}
	sumsText, err := u.downloader.Text(ctx, sums.URL)
	if err != nil {
		return UpdateFile{}, err
	}
	expected, ok := UpdateParseSums(sumsText)[asset.Name]
	if !ok {
		return UpdateFile{}, UpdateError{"SHA256SUMS does not list " + asset.Name}
	}

	if err := os.MkdirAll(u.updatesDir(), 0o755); err != nil {
		return UpdateFile{}, err
	}
	part := filepath.Join(u.updatesDir(), asset.Name+".part")
	defer os.Remove(part)
	maxBytes := asset.Size
	if maxBytes < 1 {
		maxBytes = 1
	}
	if err := u.downloader.File(ctx, asset.URL, part, maxBytes+1024*1024); err != nil {
		return UpdateFile{}, err
	}
	actual, err := UpdateFileSHA256(part)
	if err != nil {
		return UpdateFile{}, err
	}
	if !strings.EqualFold(actual, expected) {
		return UpdateFile{}, UpdateError{"The downloaded file does not match its published checksum, so it was deleted"}
	}
	target := filepath.Join(u.updatesDir(), asset.Name)
	if err := os.Rename(part, target); err != nil {
		return UpdateFile{}, err
	}
	st, err := os.Stat(target)
	if err != nil {
		return UpdateFile{}, err
	}
	file, err := u.register(UpdateFile{release.Version, asset.Name, actual, true, "github", st.Size(), u.clock()})
	if err != nil {
		return UpdateFile{}, err
	}
	u.log.Record("update.download", release.Version, "")
	return file, nil
}

// ---- importing a file (offline) ------------------------------------------------------------------------------------

var updSHA256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Import takes an installer you brought yourself (from the offline kit, a USB stick). When a SHA256SUMS sits next to
// it, or you give the expected hash, the file is checked; otherwise it is accepted but marked as not verified.
func (u *Updater) Import(path string, expected *string) (UpdateFile, error) {
	u.lock.Lock()
	defer u.lock.Unlock()
	src, err := os.Stat(path)
	if err != nil || src.IsDir() || path == "" {
		return UpdateFile{}, InvalidParam{"No file at that path"}
	}
	name := filepath.Base(path)
	version, ok := UpdateVersionOf(name)
	if !ok {
		return UpdateFile{}, InvalidParam{"That does not look like a Nimbo installer (Nimbo-<version>-windows-x64.msi or .zip, nimbo-<version>-linux-amd64)"}
	}
	actual, err := UpdateFileSHA256(path)
	if err != nil {
		return UpdateFile{}, err
	}
	given := ""
	if expected != nil {
		given = strings.ToLower(strings.TrimSpace(*expected))
	}
	if given != "" && !updSHA256Hex.MatchString(given) {
		return UpdateFile{}, InvalidParam{"The checksum must be 64 hexadecimal characters"}
	}
	beside := ""
	if text, err := os.ReadFile(filepath.Join(filepath.Dir(path), "SHA256SUMS")); err == nil {
		beside = strings.ToLower(UpdateParseSums(string(text))[name])
	}
	reference := given
	if reference == "" {
		reference = beside
	}
	if reference != "" && reference != actual {
		return UpdateFile{}, UpdateError{"The file does not match the checksum, so it was not imported"}
	}
	if err := os.MkdirAll(u.updatesDir(), 0o755); err != nil {
		return UpdateFile{}, err
	}
	target := filepath.Join(u.updatesDir(), name)
	if ts, err := os.Stat(target); err != nil || !os.SameFile(src, ts) {
		if err := copyUpdateFile(path, target); err != nil {
			return UpdateFile{}, err
		}
	}
	ts, err := os.Stat(target)
	if err != nil {
		return UpdateFile{}, err
	}
	file, err := u.register(UpdateFile{version.String(), name, actual, reference != "", "file", ts.Size(), u.clock()})
	if err != nil {
		return UpdateFile{}, err
	}
	state := "not verified"
	if reference != "" {
		state = "verified"
	}
	u.log.Record("update.import", version.String(), state)
	return file, nil
}

func copyUpdateFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---- installing ----------------------------------------------------------------------------------------------------

// Install re-checks the file against the hash recorded when it was accepted, then hands it to Windows. Only for the
// desktop app.
func (u *Updater) Install(version string, confirm bool) error {
	u.lock.Lock()
	defer u.lock.Unlock()
	if !confirm {
		return InvalidParam{"Installing replaces the running app: ask again with confirm=true"}
	}
	if u.cfg.Distribution != "desktop" {
		return InvalidParam{"Only the Windows app installs updates by itself"}
	}
	var entry *UpdateFile
	for _, f := range u.Files() {
		if f.Version == version && strings.HasSuffix(f.Name, ".msi") {
			f := f
			entry = &f
			break
		}
	}
	if entry == nil {
		return UnknownTarget{"No downloaded installer for " + version}
	}
	file := filepath.Join(u.updatesDir(), entry.Name)
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		return UpdateError{"The installer changed on disk since it was accepted; download it again"}
	}
	if sum, err := UpdateFileSHA256(file); err != nil || !strings.EqualFold(sum, entry.SHA256) {
		return UpdateError{"The installer changed on disk since it was accepted; download it again"}
	}
	target, ok := ParseSemVer(version)
	if !ok {
		return InvalidParam{"Bad version"}
	}
	if cur, ok := u.current(); ok && target.Compare(cur) <= 0 {
		return InvalidParam{version + " is not newer than the installed " + cur.String()}
	}
	if err := u.installer.Launch(file, u.pid(), u.exe()); err != nil {
		return err
	}
	state := "not verified"
	if entry.Verified {
		state = "verified"
	}
	u.log.Record("update.install", version, state)
	u.quit()
	return nil
}

// ---- bookkeeping ---------------------------------------------------------------------------------------------------

// Files lists the accepted installers that are still on disk, newest version first.
func (u *Updater) Files() []UpdateFile {
	u.regLock.Lock()
	all := u.readRegistry()
	u.regLock.Unlock()
	out := []UpdateFile{}
	for _, f := range all {
		if st, err := os.Stat(filepath.Join(u.updatesDir(), f.Name)); err == nil && !st.IsDir() {
			out = append(out, f)
		}
	}
	rank := func(f UpdateFile) SemVer { v, _ := ParseSemVer(f.Version); return v }
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]).Compare(rank(out[j])) > 0 })
	return out
}

// readRegistry reads the registry; a damaged one is set aside as `updates.json.bad` and replaced by an empty list
// instead of failing. The caller holds regLock.
func (u *Updater) readRegistry() []UpdateFile {
	text, ok := u.store.Read(updateRegistry)
	if !ok {
		return []UpdateFile{}
	}
	var list []UpdateFile
	if err := json.Unmarshal([]byte(text), &list); err != nil || list == nil {
		_ = u.store.Write(updateRegistry+".bad", text)
		return []UpdateFile{}
	}
	return list
}

func (u *Updater) writeRegistry(list []UpdateFile) error {
	if list == nil {
		list = []UpdateFile{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "    ")
	if err := enc.Encode(list); err != nil {
		return err
	}
	return u.store.Write(updateRegistry, buf.String())
}

// Clean deletes the downloaded installers and forgets them; it returns how many there were.
func (u *Updater) Clean() (int, error) {
	all := u.Files()
	u.regLock.Lock()
	defer u.regLock.Unlock()
	for _, f := range all {
		_ = os.Remove(filepath.Join(u.updatesDir(), f.Name))
	}
	return len(all), u.writeRegistry(nil)
}

func (u *Updater) register(file UpdateFile) (UpdateFile, error) {
	u.regLock.Lock()
	defer u.regLock.Unlock()
	rest := []UpdateFile{}
	for _, f := range u.readRegistry() {
		if f.Name != file.Name {
			rest = append(rest, f)
		}
	}
	return file, u.writeRegistry(append(rest, file))
}

// ---- helpers shared with the image service -------------------------------------------------------------------------

var updateNamePattern = regexp.MustCompile(`^(?:Nimbo-(.+)-windows-x64.(?:msi|zip)|nimbo-(.+)-linux-amd64)$`)

// UpdateVersionOf reads the version out of an installer's file name.
func UpdateVersionOf(fileName string) (SemVer, bool) {
	m := updateNamePattern.FindStringSubmatch(fileName)
	if m == nil {
		return SemVer{}, false
	}
	text := m[1]
	if text == "" {
		text = m[2]
	}
	return ParseSemVer(text)
}

var updSumsLine = regexp.MustCompile(`^([0-9a-fA-F]{64})\s+\*?(\S+)$`)

// UpdateParseSums reads `<hash>  <name>` per line, as written by sha256sum. Hashes come back lower-case.
func UpdateParseSums(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if m := updSumsLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			out[m[2]] = strings.ToLower(m[1])
		}
	}
	return out
}

// UpdateFileSHA256 is the hex SHA-256 of a file, read in chunks.
func UpdateFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// binaryAssetName is the file of a release that fits a Nimbo started with `nimbo serve` outside Docker: the portable
// Windows zip on Windows and the Linux binary elsewhere.
func binaryAssetName(version string) string {
	if runtime.GOOS == "windows" {
		return "Nimbo-" + version + "-windows-x64.zip"
	}
	return "nimbo-" + version + "-linux-amd64"
}
