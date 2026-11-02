package main

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Sebas1705/Nimbo/internal/nimbo"
	"gopkg.in/yaml.v3"
)

func TestPlanLaunch(t *testing.T) {
	never := func(int) bool { t.Fatal("must not probe"); return false }
	t.Run("a running nimbo is reused instead of starting a second one", func(t *testing.T) {
		got := planLaunch(8080, func(p int) bool { return p == 8080 }, never, func() int { t.Fatal("must not pick"); return 0 })
		if got != (launchPlan{openExisting, 8080}) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a free preferred port is used", func(t *testing.T) {
		got := planLaunch(8080, func(int) bool { return false }, func(int) bool { return true }, func() int { t.Fatal("must not pick"); return 0 })
		if got != (launchPlan{startNew, 8080}) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a port held by something else makes nimbo pick another one", func(t *testing.T) {
		got := planLaunch(8080, func(int) bool { return false }, func(int) bool { return false }, func() int { return 49999 })
		if got != (launchPlan{startNew, 49999}) {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestLooksLikeNimbo(t *testing.T) {
	for body, want := range map[string]bool{
		`{"status":"ok","authRequired":false,"version":"0.2.0"}`: true,
		`{"authRequired":true,"status":"ok","version":null}`:     true,
		"{\n  \"status\": \"ok\",\n  \"authRequired\": false\n}": true,
		`{"status":"ok"}`:      false,
		`<html>Jenkins</html>`: false,
		``:                     false,
	} {
		if got := looksLikeNimbo(body); got != want {
			t.Errorf("looksLikeNimbo(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestRealProbesAgreeWithTheOS(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := held.Addr().(*net.TCPAddr).Port
	if probeIsFree(port) {
		t.Error("a bound port is not free")
	}
	// A socket that accepts and never answers is not nimbo.
	if probeIsNimbo(port) {
		t.Error("a socket that never answers is not nimbo")
	}
	held.Close()
	free := probeFreePort()
	if free < 1024 || !probeIsFree(free) {
		t.Errorf("freePort gave %d", free)
	}
}

func TestProbeRecognisesANimboHealthAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","authRequired":false,"version":null}`))
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	if !probeIsNimbo(port) {
		t.Error("a Nimbo health body was not recognised")
	}
}

func TestDataDirs(t *testing.T) {
	if got, want := appDataDir(map[string]string{"LOCALAPPDATA": `C:\Users\a\AppData\Local`}, "/home/a"), filepath.Join(`C:\Users\a\AppData\Local`, "Nimbo"); got != want {
		t.Errorf("windows: %q != %q", got, want)
	}
	for _, env := range []map[string]string{{}, {"LOCALAPPDATA": "  "}} {
		if got, want := appDataDir(env, "/home/a"), filepath.Join("/home/a", ".nimbo"); got != want {
			t.Errorf("other: %q != %q", got, want)
		}
	}
	if got := shellDataDir(map[string]string{"NIMBO_DATA_DIR": "/data", "LOCALAPPDATA": "x"}, "/h"); got != "/data" {
		t.Errorf("NIMBO_DATA_DIR must win, got %q", got)
	}
}

func TestRotateLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "nimbo.log")
	write := func(n int, c string) { _ = os.WriteFile(log, []byte(strings.Repeat(c, n)), 0o644) }
	size := func(p string) int64 {
		st, err := os.Stat(p)
		if err != nil {
			return -1
		}
		return st.Size()
	}

	write(100, "x")
	rotateLog(log, 1000)
	if size(log) != 100 || size(log+".1") != -1 {
		t.Fatal("a small log is left alone")
	}
	write(2000, "y")
	rotateLog(log, 1000)
	if size(log) != -1 || size(log+".1") != 2000 {
		t.Fatal("a big log is moved aside")
	}
	write(3000, "z")
	rotateLog(log, 1000)
	if size(log+".1") != 3000 {
		t.Fatal("only one previous generation is kept")
	}
	rotateLog(filepath.Join(dir, "missing.log"), 1) // a log that does not exist yet is fine

	f, err := openLog(filepath.Join(dir, "sub", "folder"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if size(filepath.Join(dir, "sub", "folder", "nimbo.log")) != 0 {
		t.Fatal("openLog creates the folder and the file")
	}
}

func TestHostAllowed(t *testing.T) {
	allowed := []string{"nimbo.lan"}
	for host, want := range map[string]bool{
		"localhost": true, "localhost:8080": true, "127.0.0.1": true, "127.0.0.1:18780": true, "LOCALHOST": true,
		"[::1]:8080": false, "evil.example": false, "evil.example:8080": false, "localhost.evil.example": false,
		"nimbo.lan:8080": true, "wails.localhost": false, "": false,
	} {
		if got := hostAllowed(host, allowed); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestAllowedHostsFromEnv(t *testing.T) {
	got := parseAllowedHosts(map[string]string{"NIMBO_ALLOWED_HOSTS": " Nimbo.LAN, ,box.internal "})
	if len(got) != 2 || got[0] != "nimbo.lan" || got[1] != "box.internal" {
		t.Fatalf("got %v", got)
	}
}

// guardedAPI is a stand-in for the real API: it refuses any Host but loopback, like the host guard does.
func guardedAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, nil) {
			http.Error(w, "Host not allowed", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("api:" + r.URL.Path))
	})
}

func TestDesktopAPIPresentsTheWebviewAsLoopbackAndNothingElse(t *testing.T) {
	h := desktopAPI(guardedAPI())
	for host, want := range map[string]int{
		"wails.localhost": 200, "WAILS.localhost": 200, "evil.example": 403, "wails.localhost.evil.example": 403,
	} {
		req := httptest.NewRequest("GET", "http://"+host+"/api/health", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s: %d, want %d", host, rec.Code, want)
		}
	}
	// the incoming request is not mutated
	req := httptest.NewRequest("GET", "http://wails.localhost/api/x", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if req.Host != "wails.localhost" {
		t.Errorf("the caller's request was changed: %s", req.Host)
	}
}

func TestServeRouterKeepsTheGuardOnForTheUIAndTheAPI(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<title>Nimbo</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	h := serveRouter(guardedAPI(), ui, nil)
	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://"+host+path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if r := get("127.0.0.1:8080", "/"); r.Code != 200 || !strings.Contains(r.Body.String(), "<title>Nimbo</title>") {
		t.Errorf("/: %d %q", r.Code, r.Body.String())
	}
	if r := get("localhost", "/index.html"); r.Code != 200 {
		t.Errorf("/index.html: %d", r.Code)
	}
	if r := get("localhost", "/assets/app.js"); r.Code != 200 || !strings.HasPrefix(r.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("js: %d %s", r.Code, r.Header().Get("Content-Type"))
	}
	if r := get("localhost", "/nope"); r.Code != 404 {
		t.Errorf("missing: %d", r.Code)
	}
	if r := get("localhost", "/assets/"); r.Code != 404 {
		t.Errorf("a folder is not listed: %d", r.Code)
	}
	if r := get("evil.example", "/"); r.Code != 403 {
		t.Errorf("UI with a foreign host: %d", r.Code)
	}
	if r := get("evil.example", "/api/health"); r.Code != 403 {
		t.Errorf("API with a foreign host: %d", r.Code)
	}
	if r := get("localhost", "/api/health"); r.Body.String() != "api:/api/health" {
		t.Errorf("api: %q", r.Body.String())
	}
	req := httptest.NewRequest("POST", "http://localhost/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST to the UI: %d", rec.Code)
	}
}

func TestEnvMap(t *testing.T) {
	m := envMap([]string{"NIMBO_PORT=9000", "EMPTY=", "=weird", "A=b=c"})
	if m["NIMBO_PORT"] != "9000" || m["A"] != "b=c" {
		t.Fatalf("%v", m)
	}
	if _, ok := m[""]; ok {
		t.Fatal("a nameless entry must be skipped")
	}
}

func TestFirstNonBlank(t *testing.T) {
	if firstNonBlank("", "  ", " x ", "y") != "x" || firstNonBlank() != "" {
		t.Fatal("firstNonBlank")
	}
}

// ---- The icon: the tray and the exe share packaging/nimbo.ico ----------------------------------------------------

func TestTheIconIsAValidIcoWithPngPayloads(t *testing.T) {
	b, err := os.ReadFile("packaging/nimbo.ico")
	if err != nil {
		t.Fatal(err)
	}
	if len(trayIcon) != len(b) {
		t.Fatal("the embedded tray icon is not the file")
	}
	if binary.LittleEndian.Uint16(b[0:]) != 0 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		t.Fatal("not an icon header")
	}
	count := int(binary.LittleEndian.Uint16(b[4:]))
	if count < 3 {
		t.Fatalf("the icon needs several sizes, has %d", count)
	}
	for i := 0; i < count; i++ {
		e := 6 + 16*i
		length := int(binary.LittleEndian.Uint32(b[e+8:]))
		offset := int(binary.LittleEndian.Uint32(b[e+12:]))
		if offset+length > len(b) {
			t.Fatalf("entry %d runs past the file", i)
		}
		img, err := png.Decode(bytes.NewReader(b[offset : offset+length]))
		if err != nil {
			t.Fatalf("entry %d is not a PNG: %v", i, err)
		}
		w := int(b[e])
		if w == 0 {
			w = 256
		}
		if img.Bounds().Dx() != w {
			t.Errorf("entry %d says %d px but the image is %d", i, w, img.Bounds().Dx())
		}
	}
	if st, err := os.Stat("build/windows/icon.ico"); err != nil || st.Size() != int64(len(b)) {
		t.Error("build/windows/icon.ico must be a copy of packaging/nimbo.ico")
	}
}

// ---- Release rules (port of WorkflowFilesTest and PackagingTest) ----------------------------------------------

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheDeclaredVersionIsWellFormedAndHasReleaseNotes(t *testing.T) {
	v := strings.TrimSpace(declaredVersion)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("VERSION %q must be x.y.z (an MSI version has three numbers)", v)
	}
	changelog := readRepoFile(t, "CHANGELOG.md")
	_, after, ok := strings.Cut(changelog, "## ["+v+"]")
	if !ok {
		t.Fatalf("CHANGELOG.md needs a '## [%s]' section: the release workflow publishes it as the notes", v)
	}
	section, _, _ := strings.Cut(after, "\n## [")
	if strings.TrimSpace(section) == "" {
		t.Fatalf("the '## [%s]' section is empty", v)
	}
	if !strings.Contains(readRepoFile(t, "wails.json"), `"productVersion": "`+v+`"`) {
		t.Errorf("wails.json productVersion must be %s (the exe's version resource)", v)
	}
}

func workflowJobs(t *testing.T, name string) map[string]map[string]any {
	t.Helper()
	var root struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".github/workflows/"+name)), &root); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return root.Jobs
}

func TestWorkflows(t *testing.T) {
	ci, release := workflowJobs(t, "ci.yml"), workflowJobs(t, "release.yml")
	for _, j := range []string{"web", "go", "desktop"} {
		if ci[j] == nil {
			t.Errorf("ci.yml lacks job %s", j)
		}
	}
	for _, j := range []string{"package", "desktop", "images", "release", "image"} {
		if release[j] == nil {
			t.Errorf("release.yml lacks job %s", j)
		}
	}
	if !strings.HasPrefix(ci["desktop"]["runs-on"].(string), "windows") {
		t.Error("the CI desktop job runs on Windows")
	}
	if release["desktop"]["runs-on"] != "windows-2022" {
		t.Error("the installer needs WiX 3, which windows-2022 provides")
	}
	needs, _ := release["release"]["needs"].([]any)
	var got []string
	for _, n := range needs {
		got = append(got, n.(string))
	}
	if !strings.Contains(strings.Join(got, ","), "desktop") || !strings.Contains(strings.Join(got, ","), "package") {
		t.Errorf("a release must not be published without the desktop build: %v", got)
	}
	for _, j := range []string{"release", "image"} {
		if !strings.Contains(release[j]["if"].(string), "refs/tags/v") {
			t.Errorf("%s must not run on a manual dry run", j)
		}
	}
	if _, has := release["desktop"]["if"]; has {
		t.Error("the desktop build runs on dry runs too")
	}
	if !strings.Contains(readRepoFile(t, ".github/workflows/release.yml"), "tr -d '[:space:]' < VERSION") {
		t.Error("the release checks the tag against VERSION")
	}
	for _, f := range []string{"ci.yml", "release.yml"} {
		w := readRepoFile(t, ".github/workflows/"+f)
		if !strings.Contains(w, "packaging/smoke-test.ps1") {
			t.Errorf("%s must run the smoke test", f)
		}
		if !strings.Contains(w, "-X main.version=") {
			t.Errorf("%s must inject the version with ldflags", f)
		}
	}
	if !strings.Contains(readRepoFile(t, ".github/workflows/release.yml"), "SHA256SUMS") {
		t.Error("the release publishes SHA256SUMS")
	}
}

func TestSmokeTestUsesTheHeadlessMode(t *testing.T) {
	s := readRepoFile(t, "packaging/smoke-test.ps1")
	if !strings.Contains(s, `"serve"`) || !strings.Contains(s, "NIMBO_DATA_DIR") {
		t.Error("the smoke test runs `Nimbo.exe serve` with a throwaway data folder")
	}
}

func TestTheInstallerKeepsAFixedUpgradeCodeAndInstallsPerUser(t *testing.T) {
	w := readRepoFile(t, "packaging/nimbo.wxs")
	m := regexp.MustCompile(`UpgradeCode="([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})"`).FindStringSubmatch(w)
	if m == nil {
		t.Fatal("the installer needs a fixed UpgradeCode so a new version replaces the old one")
	}
	if !strings.EqualFold(m[1], "7c3f2b9e-4d51-4a8e-9a47-2f1e6c0b5d83") {
		t.Error("keep the upgrade code of the old jpackage installer: it makes this MSI replace the Java build in place")
	}
	for _, want := range []string{`InstallScope="perUser"`, "<MajorUpgrade", "LocalAppDataFolder", "<Shortcut", `Name="Nimbo"`} {
		if !strings.Contains(w, want) {
			t.Errorf("nimbo.wxs lacks %s", want)
		}
	}
	dec := xml.NewDecoder(strings.NewReader(w))
	for {
		if _, err := dec.Token(); err != nil {
			if err != io.EOF {
				t.Errorf("nimbo.wxs is not well-formed XML: %v", err)
			}
			break
		}
	}
}

func TestDockerfileRunsTheHeadlessServer(t *testing.T) {
	d := readRepoFile(t, "Dockerfile")
	for _, want := range []string{"NIMBO_HOST=0.0.0.0", "NIMBO_EMULATOR_HOST=host.docker.internal", "NIMBO_DISTRIBUTION=docker", "NIMBO_DATA_DIR=/data", "VOLUME /data", "docker:27-cli", `ENTRYPOINT ["nimbo", "serve"]`, "CGO_ENABLED=0", "-X main.version="} {
		if !strings.Contains(d, want) {
			t.Errorf("Dockerfile lacks %s", want)
		}
	}
}

// The window's requests carry Origin http://wails.localhost on every POST/PUT/DELETE. They must pass the cross-site
// guard of the real server, and a page from anywhere else must not, through the very same wrapper.
func TestTheWindowCanChangeThingsAndAForeignPageCannot(t *testing.T) {
	api := nimbo.NewServer(nimbo.Config{HostEnv: map[string]string{}}, nimbo.Deps{Data: nimbo.NewAppData(nil, nil)})
	h := desktopAPI(api)
	do := func(method, origin string) int {
		req := httptest.NewRequest(method, "http://wails.localhost/api/settings", strings.NewReader(`{"theme":"dark"}`))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := do("PUT", "http://wails.localhost"); got != 200 {
		t.Errorf("the window itself: %d, want 200", got)
	}
	if got := do("PUT", "https://evil.example"); got != 403 {
		t.Errorf("a foreign page: %d, want 403", got)
	}
	if got := do("PUT", ""); got != 200 {
		t.Errorf("no Origin (a script): %d, want 200", got)
	}
}

// The published app is not started from the repository, so what it shows must come from what is inside the exe. This
// is the test that 0.3.0 lacked: it used the real embedded files, from a folder with no labs/ and no nimbo.yml.
func TestTheEmbeddedLabsAndStacksLoadFromAnyFolder(t *testing.T) {
	onDisk, err := os.ReadDir("labs")
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, e := range onDisk {
		if strings.HasSuffix(e.Name(), ".yml") {
			want++
		}
	}
	t.Chdir(t.TempDir())

	labs := nimbo.LoadLabs(map[string]string{}, bundledDefaults)
	if len(labs.Labs) != want || want < 8 {
		t.Errorf("labs: %d loaded, %d in the repository", len(labs.Labs), want)
	}
	if len(labs.Problems) != 0 {
		t.Errorf("lab problems: %v", labs.Problems)
	}
	stacks := nimbo.LoadStacks(map[string]string{}, bundledDefaults)
	if len(stacks.Problems) != 0 || stacks.Find("cluster") == nil || stacks.Find("storage-lab") == nil {
		t.Errorf("stacks: %+v", stacks)
	}
}

// A folder named labs or a nimbo.yml in the folder the app was started from must not change what it shows.
func TestTheLaunchFolderDoesNotDecideWhatTheAppShows(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "labs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "labs", "stray.yml"), []byte("not: a lab"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nimbo.yml"), []byte("stacks:\n  stray:\n    providers: [redis]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, l := range nimbo.LoadLabs(map[string]string{}, bundledDefaults).Labs {
		if l.Dto.ID == "stray" {
			t.Error("a lab from the launch folder was loaded")
		}
	}
	if nimbo.LoadStacks(map[string]string{}, bundledDefaults).Find("stray") != nil {
		t.Error("a stack from the launch folder was loaded")
	}
	// Explicit settings still win.
	if got := nimbo.LoadStacks(map[string]string{"NIMBO_STACKS_FILE": filepath.Join(dir, "nimbo.yml")}, bundledDefaults); got.Find("stray") == nil {
		t.Error("NIMBO_STACKS_FILE was ignored")
	}
}
