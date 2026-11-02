package nimbo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// imgCatalog is a hardcoded copy of the providers of Catalog.kt (id and image). The integrator replaces it with
// the real catalog; until then this catches a pack that drifts from the providers.
var imgCatalog = []ImageProviderRef{
	{"floci-aws", "floci/floci"},
	{"floci-azure", "floci/floci-az"},
	{"floci-gcp", "floci/floci-gcp"},
	{"floci-oci", "floci/floci-oci"},
	{"azurite", "mcr.microsoft.com/azure-storage/azurite"},
	{"minio", "cgr.dev/chainguard/minio"},
	{"redpanda", "redpandadata/redpanda"},
	{"postgres", "postgres:17-alpine"},
	{"toxiproxy", "ghcr.io/shopify/toxiproxy"},
	{"k3s", "rancher/k3s"},
	{"jaeger", "jaegertracing/all-in-one"},
	{"redis", "redis:7-alpine"},
}

type imgFakeDocker struct {
	images  string
	loadOut string
	fail    bool
	calls   [][]string
}

func newImgDocker() *imgFakeDocker { return &imgFakeDocker{loadOut: "Loaded image: redis:7-alpine"} }

func (d *imgFakeDocker) Run(_ context.Context, args []string, _ int) ExecResult {
	d.calls = append(d.calls, args)
	if d.fail {
		return ExecResult{1, "daemon not running"}
	}
	switch args[0] {
	case "images":
		return ExecResult{0, d.images}
	case "load":
		return ExecResult{0, d.loadOut}
	}
	return ExecResult{0, ""}
}

func imgService(d *imgFakeDocker, log *updFakeLog, folders ...string) *Images {
	return NewImages(d, func() []string { return folders }, log, imgCatalog, nil)
}

func imgFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	return updTempFile(t, dir, name, content)
}

func imgProvider(s ImagesStatus, id string) ImageState {
	for _, i := range s.Images {
		if i.Provider == id {
			return i
		}
	}
	return ImageState{}
}

// ---- ImagePacksTest ------------------------------------------------------------------------------------------------

func TestImagePacksEveryProviderImageIsInExactlyOnePackAndEveryPackImageIsAProviderImage(t *testing.T) {
	images := []string{}
	for _, p := range imgCatalog {
		images = append(images, p.Image)
	}
	if problems := CheckImagePacks(DefaultImagePacks(), images); len(problems) != 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
}

func TestImagePacksCheckSpotsDrift(t *testing.T) {
	// Extra: the check itself must fail when a pack drifts.
	packs := []PackDef{{"a", []string{"x", "y"}}, {"b", []string{"x", "ghost"}}, {"a", nil}}
	problems := strings.Join(CheckImagePacks(packs, []string{"x", "y", "z"}), "\n")
	for _, want := range []string{"'x' must be in exactly one pack (is in 2)", "'z' must be in exactly one pack (is in 0)", "'ghost' which no provider uses", "pack id 'a' is repeated"} {
		if !strings.Contains(problems, want) {
			t.Errorf("missing %q in\n%s", want, problems)
		}
	}
}

func TestImagePacksFileNamesMapToTheirPack(t *testing.T) {
	if p := ImagePackOf("Nimbo-0.3.0-images-data.tar.gz"); p == nil || *p != "data" {
		t.Fatalf("got %v", p)
	}
	if ImagePackOf("random.tar") != nil {
		t.Error("random.tar belongs to no pack")
	}
	for _, ok := range []string{"x.tar", "x.TAR.GZ", "x.tgz"} {
		if !ImageIsPack(ok) {
			t.Errorf("%s should be a pack", ok)
		}
	}
	for _, no := range []string{"x.zip", "x.exe", `a/b.tar`, `a\b.tar`, ".tar"} {
		if ImageIsPack(no) {
			t.Errorf("%s should not be a pack", no)
		}
	}
}

func TestImagePacksAnImageWithoutATagMeansLatest(t *testing.T) {
	for in, want := range map[string]string{
		"floci/floci":         "floci/floci:latest",
		"postgres:17-alpine":  "postgres:17-alpine",
		"localhost:5000/x":    "localhost:5000/x:latest",
		"localhost:5000/x:v1": "localhost:5000/x:v1",
	} {
		if got := ImageNormalize(in); got != want {
			t.Errorf("%s -> %s want %s", in, got, want)
		}
	}
}

// ---- ImagesServiceTest ---------------------------------------------------------------------------------------------

func TestImagesStatusSaysWhichImagesArePresent(t *testing.T) {
	s := imgService(&imgFakeDocker{images: "redis:7-alpine\nfloci/floci:latest\nother:1\r\n"}, &updFakeLog{}).Status(context.Background())
	if !s.Docker {
		t.Fatal("docker is up")
	}
	if !imgProvider(s, "redis").Present || !imgProvider(s, "floci-aws").Present || imgProvider(s, "postgres").Present {
		t.Fatalf("presence wrong: %+v", s.Images)
	}
	if p := imgProvider(s, "redis").Pack; p == nil || *p != "data" {
		t.Errorf("pack %v", p)
	}
}

func TestImagesDockerDownIsReportedNotThrown(t *testing.T) {
	s := imgService(&imgFakeDocker{fail: true}, &updFakeLog{}).Status(context.Background())
	if s.Docker {
		t.Fatal("docker is down")
	}
	for _, i := range s.Images {
		if i.Present {
			t.Errorf("%s present", i.Provider)
		}
	}
}

func TestImagesScanListsPacksWithTheirChecksumState(t *testing.T) {
	d := t.TempDir()
	good := imgFile(t, d, "Nimbo-0.3.0-images-data.tar.gz", "data pack")
	imgFile(t, d, "Nimbo-0.3.0-images-k8s.tar.gz", "k8s pack")
	imgFile(t, d, "notes.txt", "ignored")
	sum, _ := UpdateFileSHA256(good)
	imgFile(t, d, "SHA256SUMS", sum+"  Nimbo-0.3.0-images-data.tar.gz\n"+strings.Repeat("0", 64)+"  Nimbo-0.3.0-images-k8s.tar.gz\n")
	found := imgService(newImgDocker(), &updFakeLog{}, d, filepath.Join(d, "missing")).Scan()
	byName := map[string]PackFile{}
	for _, f := range found {
		byName[f.Name] = f
	}
	if len(found) != 2 {
		t.Fatalf("found %+v", found)
	}
	if v := byName["Nimbo-0.3.0-images-data.tar.gz"].Verified; v == nil || !*v {
		t.Errorf("data verified %v", v)
	}
	if v := byName["Nimbo-0.3.0-images-k8s.tar.gz"].Verified; v == nil || *v {
		t.Errorf("k8s verified %v", v)
	}
	if p := byName["Nimbo-0.3.0-images-data.tar.gz"].Pack; p == nil || *p != "data" {
		t.Errorf("pack %v", p)
	}
}

func TestImagesScanWithoutASumsFileLeavesVerificationUnknown(t *testing.T) {
	d := t.TempDir()
	imgFile(t, d, "mine.tar", "x")
	found := imgService(newImgDocker(), &updFakeLog{}, d).Scan()
	if len(found) != 1 || found[0].Verified != nil || found[0].Pack != nil {
		t.Fatalf("found %+v", found)
	}
}

func TestImagesScanSkipsRepeatedFolders(t *testing.T) {
	// Extra: the same folder listed twice (env var and data folder pointing at one place) lists its packs once.
	d := t.TempDir()
	imgFile(t, d, "a.tar", "x")
	if n := len(imgService(newImgDocker(), &updFakeLog{}, d, d, filepath.Join(d, ".")).Scan()); n != 1 {
		t.Fatalf("got %d", n)
	}
}

func TestImagesLoadRunsDockerLoadAndReportsWhatCameIn(t *testing.T) {
	d := t.TempDir()
	pack := imgFile(t, d, "Nimbo-0.3.0-images-data.tar.gz", "x")
	docker := &imgFakeDocker{loadOut: "Loaded image: redis:7-alpine\nLoaded image ID: sha256:abc\nnoise\nLoaded image: postgres:17-alpine"}
	log := &updFakeLog{}
	r, err := imgService(docker, log, d).Load(context.Background(), pack)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"redis:7-alpine", "sha256:abc", "postgres:17-alpine"}; !reflect.DeepEqual(r.Loaded, want) {
		t.Fatalf("loaded %v", r.Loaded)
	}
	if last := docker.calls[len(docker.calls)-1]; !reflect.DeepEqual(last, []string{"load", "-i", pack}) {
		t.Fatalf("call %v", last)
	}
	if len(log.entries) != 1 || log.entries[0][0] != "images.load" || !strings.Contains(log.entries[0][2], "not verified") {
		t.Fatalf("log %v", log.entries)
	}
}

func TestImagesLoadRefusesAPackWhoseChecksumDoesNotMatchBeforeDockerSeesIt(t *testing.T) {
	d := t.TempDir()
	pack := imgFile(t, d, "Nimbo-0.3.0-images-data.tar.gz", "tampered")
	imgFile(t, d, "SHA256SUMS", strings.Repeat("a", 64)+"  Nimbo-0.3.0-images-data.tar.gz\n")
	docker := newImgDocker()
	_, err := imgService(docker, &updFakeLog{}, d).Load(context.Background(), pack)
	updUpdateErr(t, err, "does not match the checksum")
	for _, c := range docker.calls {
		if c[0] == "load" {
			t.Fatal("docker load must not run")
		}
	}
}

func TestImagesLoadVerifiedPackIsJournaledAsVerified(t *testing.T) {
	// Extra: a matching SHA256SUMS beside the file shows in the activity log.
	d := t.TempDir()
	pack := imgFile(t, d, "p.tar", "ok")
	sum, _ := UpdateFileSHA256(pack)
	imgFile(t, d, "SHA256SUMS", sum+"  p.tar\n")
	log := &updFakeLog{}
	if _, err := imgService(newImgDocker(), log, d).Load(context.Background(), pack); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(log.entries[0][2], ", verified") {
		t.Fatalf("log %v", log.entries)
	}
}

func TestImagesLoadRefusesOddPathsAndReportsDockerErrors(t *testing.T) {
	d := t.TempDir()
	ctx := context.Background()
	_, err := imgService(newImgDocker(), &updFakeLog{}).Load(ctx, filepath.Join(d, "nope.tar"))
	updInvalid(t, err)
	text := imgFile(t, d, "notes.txt", "x")
	_, err = imgService(newImgDocker(), &updFakeLog{}).Load(ctx, text)
	updInvalid(t, err)
	pack := imgFile(t, d, "p.tar", "x")
	_, err = imgService(&imgFakeDocker{fail: true}, &updFakeLog{}).Load(ctx, pack)
	updUpdateErr(t, err, "daemon not running")
	_, err = imgService(newImgDocker(), &updFakeLog{}).Load(ctx, "")
	updInvalid(t, err)
}

func TestImagesPullAsksDockerForTheProviderImageOnly(t *testing.T) {
	docker := newImgDocker()
	svc := imgService(docker, &updFakeLog{})
	if err := svc.Pull(context.Background(), "redis"); err != nil {
		t.Fatal(err)
	}
	if last := docker.calls[len(docker.calls)-1]; !reflect.DeepEqual(last, []string{"pull", "redis:7-alpine"}) {
		t.Fatalf("call %v", last)
	}
	var ut UnknownTarget
	if err := svc.Pull(context.Background(), "nope"); !errors.As(err, &ut) {
		t.Fatalf("want UnknownTarget, got %v", err)
	}
	updUpdateErr(t, imgService(&imgFakeDocker{fail: true}, &updFakeLog{}).Pull(context.Background(), "redis"), "daemon not running")
}

func TestImagesSearchFoldersComeFromTheEnvironmentTheAppFolderAndTheDataFolder(t *testing.T) {
	app, data := t.TempDir(), t.TempDir()
	got := ImagesSearchFolders(map[string]string{"NIMBO_IMAGES_DIR": "X:/packs"}, data, app)
	want := []string{"X:/packs", filepath.Join(app, "images"), filepath.Join(data, "images")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if n := len(ImagesSearchFolders(map[string]string{}, t.TempDir(), "")); n != 1 {
		t.Fatalf("got %d", n)
	}
	if n := len(ImagesSearchFolders(map[string]string{"NIMBO_IMAGES_DIR": "  "}, "", "")); n != 0 {
		t.Fatalf("blank env must be ignored, got %d", n)
	}
}

// ---- ImageRoutesTest -----------------------------------------------------------------------------------------------

func TestImageRoutesListLoadAndPull(t *testing.T) {
	d := t.TempDir()
	pack := imgFile(t, d, "Nimbo-0.3.0-images-data.tar.gz", "x")
	mux := http.NewServeMux()
	NewImageRoutes(imgService(newImgDocker(), &updFakeLog{}, d)).Register(mux)

	list := updCall(mux, "GET", "/api/images", "")
	if list.Code != 200 || !strings.Contains(list.Body.String(), "Nimbo-0.3.0-images-data.tar.gz") {
		t.Fatalf("list %d %s", list.Code, list.Body)
	}
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	if r := updCall(mux, "POST", "/api/images/load", `{"path":`+quote(pack)+`}`); r.Code != 200 {
		t.Fatalf("load %d %s", r.Code, r.Body)
	}
	if r := updCall(mux, "POST", "/api/images/load", `{"path":"Z:/missing.tar"}`); r.Code != 400 {
		t.Fatalf("missing %d", r.Code)
	}
	if r := updCall(mux, "POST", "/api/images/load", `{"path":"C:\Users\x"}`); r.Code != 400 {
		t.Fatalf("a malformed body is the caller's mistake, not a server error: %d", r.Code)
	}
	if r := updCall(mux, "POST", "/api/images/pull", `{"provider":"redis"}`); r.Code != 204 {
		t.Fatalf("pull %d", r.Code)
	}
	if r := updCall(mux, "POST", "/api/images/pull", `{"provider":"nope"}`); r.Code != 404 {
		t.Fatalf("unknown %d", r.Code)
	}
}

func TestImageRoutesDockerFailuresAreMappedLikeKotlin(t *testing.T) {
	// Extra: a failed docker load is 422 and a failed pull is 502.
	d := t.TempDir()
	pack := imgFile(t, d, "p.tar", "x")
	mux := http.NewServeMux()
	NewImageRoutes(imgService(&imgFakeDocker{fail: true}, &updFakeLog{}, d)).Register(mux)
	b, _ := json.Marshal(map[string]string{"path": pack})
	if r := updCall(mux, "POST", "/api/images/load", string(b)); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("load %d %s", r.Code, r.Body)
	}
	if r := updCall(mux, "POST", "/api/images/pull", `{"provider":"redis"}`); r.Code != http.StatusBadGateway {
		t.Fatalf("pull %d", r.Code)
	}
}

func TestImageRoutesStatusJSONHasEveryKey(t *testing.T) {
	// Extra: the UI reads these keys; nullable ones are null, lists are never null.
	rec := httptest.NewRecorder()
	WriteJSON(rec, 200, imgService(&imgFakeDocker{fail: true}, &updFakeLog{}).Status(context.Background()))
	for _, key := range []string{`"docker":false`, `"images":[`, `"packs":[`, `"files":[]`, `"folders":[]`, `"pack":"data"`, `"present":false`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("missing %s in %s", key, rec.Body.String())
		}
	}
}

func TestImageRoutesAreAbsentWhenNotConfigured(t *testing.T) {
	if c := updCall(http.NewServeMux(), "GET", "/api/images", "").Code; c != 404 {
		t.Fatalf("got %d", c)
	}
}
