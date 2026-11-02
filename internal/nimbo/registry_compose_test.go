package nimbo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// composeEnvSetup makes a local source with a compose file and an env file, and a profile that uses both.
func composeEnvSetup(t *testing.T) (*regEnv, string) {
	t.Helper()
	e := newRegEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "deploy", "compose.yml"), "services:\n  api:\n    image: x\n")
	writeFile(t, filepath.Join(dir, "deploy", "compose.extra.yaml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, ".env.test"), "SECRET_TOKEN=do-not-leak\n")
	e.local(t, "repo", dir)
	_, err := e.compose.Put(context.Background(), ComposeDef{
		ID: "api-tests", Name: "API tests", Purpose: "integration tests",
		Files:    []ComposeFileRef{{"repo", "deploy/compose.yml"}, {"repo", "deploy/compose.extra.yaml"}},
		Profiles: []string{"tests", "debug"},
		EnvFile:  &ComposeFileRef{"repo", ".env.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, dir
}

func dockerCalls(f *regFake) []RegistryCommand {
	var out []RegistryCommand
	for _, c := range f.all() {
		if c.Name == "docker" {
			out = append(out, c)
		}
	}
	return out
}

func baseArgs(dir string) []string {
	return []string{"compose", "--project-name", "nimbo-reg-api-tests", "--project-directory", filepath.Join(dir, "deploy"),
		"-f", filepath.Join(dir, "deploy", "compose.yml"), "-f", filepath.Join(dir, "deploy", "compose.extra.yaml"),
		"--env-file", filepath.Join(dir, ".env.test"), "--profile", "tests", "--profile", "debug"}
}

func with(base []string, rest ...string) []string {
	return append(append([]string{}, base...), rest...)
}

func TestComposeCommandLines(t *testing.T) {
	e, dir := composeEnvSetup(t)
	ctx := context.Background()
	base := baseArgs(dir)
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if c.Args[len(c.Args)-1] == "json" {
			return ExecResult{0, `{"Service":"api","State":"running","Health":"healthy"}`}
		}
		return ExecResult{0, "some output"}
	})

	// validate
	e.fake.reset()
	chk, err := e.compose.Validate(ctx, "api-tests")
	if err != nil || !chk.OK {
		t.Fatalf("validate: %+v %v", chk, err)
	}
	got := dockerCalls(e.fake)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Args, with(base, "config", "-q")) || got[0].TimeoutSeconds != 60 {
		t.Fatalf("validate command: %+v", got)
	}

	// up (then the view asks for ps)
	e.fake.reset()
	if _, err := e.compose.Up(ctx, "api-tests"); err != nil {
		t.Fatal(err)
	}
	got = dockerCalls(e.fake)
	if len(got) < 2 {
		t.Fatalf("up commands: %+v", got)
	}
	if !reflect.DeepEqual(got[0].Args, with(base, "up", "-d", "--remove-orphans")) || got[0].TimeoutSeconds != 300 || got[0].Name != "docker" {
		t.Fatalf("up command: %+v", got[0])
	}
	if !reflect.DeepEqual(got[1].Args, with(base, "ps", "-a", "--format", "json")) || got[1].TimeoutSeconds != 30 {
		t.Fatalf("ps command: %+v", got[1])
	}

	// down, without and with volumes
	e.fake.reset()
	if _, err := e.compose.Down(ctx, "api-tests", false); err != nil {
		t.Fatal(err)
	}
	if c := dockerCalls(e.fake)[0]; !reflect.DeepEqual(c.Args, with(base, "down", "--remove-orphans")) || c.TimeoutSeconds != 120 {
		t.Fatalf("down command: %+v", c)
	}
	e.fake.reset()
	_, _ = e.compose.Down(ctx, "api-tests", true)
	if c := dockerCalls(e.fake)[0]; !reflect.DeepEqual(c.Args, with(base, "down", "--remove-orphans", "--volumes")) {
		t.Fatalf("down -v command: %+v", c)
	}

	// logs
	e.fake.reset()
	out, err := e.compose.Logs(ctx, "api-tests", 50, "api")
	if err != nil || out != "some output" {
		t.Fatalf("logs: %q %v", out, err)
	}
	if c := dockerCalls(e.fake)[0]; !reflect.DeepEqual(c.Args, with(base, "logs", "--no-color", "--tail", "50", "api")) {
		t.Fatalf("logs command: %+v", c)
	}
	e.fake.reset()
	_, _ = e.compose.Logs(ctx, "api-tests", 0, "")
	if c := dockerCalls(e.fake)[0]; !reflect.DeepEqual(c.Args, with(base, "logs", "--no-color", "--tail", "200")) {
		t.Fatalf("default logs: %+v", c)
	}
	e.fake.reset()
	_, _ = e.compose.Logs(ctx, "api-tests", 999999, "")
	if c := dockerCalls(e.fake)[0]; c.Args[len(c.Args)-1] != "2000" {
		t.Fatalf("tail is capped: %+v", c)
	}
	for _, bad := range []string{"--follow", "a b", "-x", "../x", "a;b"} {
		if _, err := e.compose.Logs(ctx, "api-tests", 10, bad); err == nil {
			t.Errorf("service %q should be refused", bad)
		}
	}
}

func TestComposeWithoutProfilesOrEnvFile(t *testing.T) {
	e := newRegEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "c.yml"), "services: {}")
	abs := filepath.Join(dir, "c.yml")
	if _, err := e.compose.Put(context.Background(), ComposeDef{ID: "plain", Files: []ComposeFileRef{{"", abs}}}); err != nil {
		t.Fatal(err)
	}
	e.fake.reset()
	_, _ = e.compose.Validate(context.Background(), "plain")
	want := []string{"compose", "--project-name", "nimbo-reg-plain", "--project-directory", dir, "-f", abs, "config", "-q"}
	if c := dockerCalls(e.fake)[0]; !reflect.DeepEqual(c.Args, want) {
		t.Fatalf("%v", c.Args)
	}
}

func TestComposeOnlyTouchesItsOwnProjects(t *testing.T) {
	e, dir := composeEnvSetup(t)
	ctx := context.Background()
	// Every docker command carries a project name that starts with the prefix.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{} })
	_, _ = e.compose.Up(ctx, "api-tests")
	_, _ = e.compose.Down(ctx, "api-tests", true)
	_, _ = e.compose.Validate(ctx, "api-tests")
	_, _ = e.compose.Logs(ctx, "api-tests", 5, "")
	_, _ = e.compose.List(ctx), 0
	for _, c := range dockerCalls(e.fake) {
		if c.Args[1] != "--project-name" || !strings.HasPrefix(c.Args[2], "nimbo-reg-") {
			t.Fatalf("a command without our project name: %v", c.Args)
		}
	}
	// A name that is not ours is refused before the runner is reached.
	e.fake.reset()
	if res := e.compose.compose(ctx, []string{"compose", "--project-name", "someone-else", "down", "--volumes"}, 5); res.OK() {
		t.Fatal("must refuse a foreign project")
	}
	if len(e.fake.all()) != 0 {
		t.Fatal("the runner must not be reached")
	}
	// Ids that are not plain names never become a project name.
	for _, id := range []string{"", "../x", "A", "a b", "a;rm", strings.Repeat("a", 40), "-x"} {
		if _, err := composeProject(id); err == nil {
			t.Errorf("%q must not make a project name", id)
		}
		if _, err := e.compose.Put(ctx, ComposeDef{ID: id, Files: []ComposeFileRef{{"repo", "deploy/compose.yml"}}}); err == nil {
			t.Errorf("%q must not be saved", id)
		}
	}
	_ = dir
	if _, err := e.compose.Up(ctx, "unknown"); err == nil {
		t.Fatal("unknown profile")
	} else if _, ok := err.(UnknownTarget); !ok {
		t.Fatalf("%T", err)
	}
}

func TestComposePutValidation(t *testing.T) {
	e, dir := composeEnvSetup(t)
	ctx := context.Background()
	abs := filepath.Join(dir, "deploy", "compose.yml")
	ok := ComposeFileRef{"repo", "deploy/compose.yml"}
	cases := map[string]ComposeDef{
		"no files":           {ID: "x"},
		"unknown source":     {ID: "x", Files: []ComposeFileRef{{"ghost", "a.yml"}}},
		"traversal":          {ID: "x", Files: []ComposeFileRef{{"repo", "../a.yml"}}},
		"traversal deep":     {ID: "x", Files: []ComposeFileRef{{"repo", "a/../../a.yml"}}},
		"absolute in source": {ID: "x", Files: []ComposeFileRef{{"repo", abs}}},
		"relative no source": {ID: "x", Files: []ComposeFileRef{{"", "deploy/compose.yml"}}},
		"wrong extension":    {ID: "x", Files: []ComposeFileRef{{"repo", "deploy/run.sh"}}},
		"duplicate":          {ID: "x", Files: []ComposeFileRef{ok, ok}},
		"too many": {ID: "x", Files: func() []ComposeFileRef {
			var f []ComposeFileRef
			for i := 0; i < 11; i++ {
				f = append(f, ComposeFileRef{"repo", "f" + string(rune('a'+i)) + ".yml"})
			}
			return f
		}()},
		"env wrong name":     {ID: "x", Files: []ComposeFileRef{ok}, EnvFile: &ComposeFileRef{"repo", "deploy/compose.yml"}},
		"env traversal":      {ID: "x", Files: []ComposeFileRef{ok}, EnvFile: &ComposeFileRef{"repo", "../.env"}},
		"profile flag":       {ID: "x", Files: []ComposeFileRef{ok}, Profiles: []string{"--x"}},
		"profile twice":      {ID: "x", Files: []ComposeFileRef{ok}, Profiles: []string{"a", "a"}},
		"profile with space": {ID: "x", Files: []ComposeFileRef{ok}, Profiles: []string{"a b"}},
		"long name":          {ID: "x", Name: strings.Repeat("n", 61), Files: []ComposeFileRef{ok}},
	}
	for name, def := range cases {
		if _, err := e.compose.Put(ctx, def); err == nil {
			t.Errorf("%s: should be refused", name)
		} else if _, isInvalid := err.(InvalidParam); !isInvalid {
			t.Errorf("%s: wanted InvalidParam, got %T", name, err)
		}
	}
	// Valid forms: absolute file with no source, name defaults to the id, lists are never nil.
	v, err := e.compose.Put(ctx, ComposeDef{ID: "abs", Files: []ComposeFileRef{{"", abs}}})
	if err != nil || v.Name != "abs" || v.Profiles == nil || v.EnvFile != nil || v.EnvFileStatus != nil {
		t.Fatalf("absolute file: %+v %v", v, err)
	}
	if len(e.compose.List(ctx)) != 2 {
		t.Fatal("two profiles expected")
	}
}

func TestComposeUpConflictsWhenFilesAreMissing(t *testing.T) {
	e, dir := composeEnvSetup(t)
	ctx := context.Background()
	e.fake.reset()
	if err := os.Remove(filepath.Join(dir, "deploy", "compose.extra.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".env.test")); err != nil {
		t.Fatal(err)
	}
	_, err := e.compose.Up(ctx, "api-tests")
	rc, ok := err.(RegistryConflict)
	if !ok {
		t.Fatalf("wanted a conflict, got %T %v", err, err)
	}
	if !strings.Contains(rc.Msg, "compose.extra.yaml") {
		t.Fatalf("the message should list the missing files: %s", rc.Msg)
	}
	for _, c := range dockerCalls(e.fake) {
		for _, a := range c.Args {
			if a == "up" {
				t.Fatal("docker up must not run with missing files")
			}
		}
	}
	// Only the env file missing.
	writeFile(t, filepath.Join(dir, "deploy", "compose.extra.yaml"), "services: {}")
	if _, err := e.compose.Up(ctx, "api-tests"); err == nil || !strings.Contains(err.Error(), ".env.test") {
		t.Fatalf("env file missing: %v", err)
	}
	// validate reports instead of failing.
	chk, err := e.compose.Validate(ctx, "api-tests")
	if err != nil || chk.OK || !strings.Contains(chk.Message, ".env.test") {
		t.Fatalf("validate with a missing file: %+v %v", chk, err)
	}
	// The view says which file is missing.
	v, _ := e.compose.Get(ctx, "api-tests")
	if v.EnvFileStatus == nil || v.EnvFileStatus.Status != "missing" || v.EnvFileStatus.Sha256 != nil {
		t.Fatalf("env status: %+v", v.EnvFileStatus)
	}
	// down still works by project name alone when a file went away.
	e.fake.reset()
	if _, err := e.compose.Down(ctx, "api-tests", false); err != nil {
		t.Fatal(err)
	}
	if c := dockerCalls(e.fake)[0]; !reflect.DeepEqual(c.Args, []string{"compose", "--project-name", "nimbo-reg-api-tests", "down", "--remove-orphans"}) {
		t.Fatalf("down by name: %v", c.Args)
	}
}

func TestComposeUpWithAGitSourceNeverSynced(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	url := "https://h.example/o/r.git"
	_, _ = e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url})
	if _, err := e.compose.Put(ctx, ComposeDef{ID: "p", Files: []ComposeFileRef{{"remote", "compose.yml"}}}); err != nil {
		t.Fatal(err)
	}
	e.fake.reset()
	_, err := e.compose.Up(ctx, "p")
	rc, ok := err.(RegistryConflict)
	if !ok || !strings.Contains(rc.Msg, "has not been cloned") {
		t.Fatalf("%T %v", err, err)
	}
	if len(dockerCalls(e.fake)) != 0 {
		t.Fatal("docker must not be called")
	}
}

func TestComposeUpRefusesAFileThatEscapesTheSource(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, "sub", "ok.yml"), "services: {}")
	writeFile(t, filepath.Join(root, "outside", "ok.yml"), "services: {}")
	e.local(t, "repo", repo)
	if _, err := e.compose.Put(ctx, ComposeDef{ID: "p", Files: []ComposeFileRef{{"repo", "sub/ok.yml"}}}); err != nil {
		t.Fatal(err)
	}
	// The folder becomes a link to somewhere else after the profile was saved.
	if err := os.RemoveAll(filepath.Join(repo, "sub")); err != nil {
		t.Fatal(err)
	}
	linkDir(t, filepath.Join(repo, "sub"), filepath.Join(root, "outside"))
	e.fake.reset()
	if _, err := e.compose.Up(ctx, "p"); err == nil {
		t.Fatal("must refuse")
	} else if _, ok := err.(InvalidParam); !ok {
		t.Fatalf("wanted InvalidParam, got %T", err)
	}
	if _, err := e.compose.File("p", 0); err == nil {
		t.Fatal("the viewer must refuse too")
	}
	if len(dockerCalls(e.fake)) != 0 {
		t.Fatal("docker must not be called")
	}
}

func TestComposeChangedDetectionFollowsUp(t *testing.T) {
	e, dir := composeEnvSetup(t)
	ctx := context.Background()
	e.compose.clock = func() int64 { return 4242 }
	status := func() ComposeView {
		v, err := e.compose.Get(ctx, "api-tests")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := status()
	if v.LastUpSha != nil || v.LastUpAt != nil || v.FileStatus[0].Status != "ok" || v.FileStatus[0].Sha256 == nil {
		t.Fatalf("before any up: %+v", v)
	}
	first := *v.FileStatus[0].Sha256
	if _, err := e.compose.Up(ctx, "api-tests"); err != nil {
		t.Fatal(err)
	}
	v = status()
	compose := filepath.Join(dir, "deploy", "compose.yml")
	if v.LastUpAt == nil || *v.LastUpAt != 4242 || v.LastUpSha[compose] != first || len(v.LastUpSha) != 3 {
		t.Fatalf("after up: %+v", v)
	}
	if v.FileStatus[0].Status != "ok" || v.FileStatus[1].Status != "ok" || v.EnvFileStatus.Status != "ok" {
		t.Fatalf("nothing changed yet: %+v", v)
	}
	// The file changes on disk: it is read again, nothing is cached.
	writeFile(t, compose, "services:\n  api:\n    image: y\n")
	v = status()
	if v.FileStatus[0].Status != "changed" || *v.FileStatus[0].Sha256 == first || v.FileStatus[1].Status != "ok" {
		t.Fatalf("after editing: %+v", v.FileStatus)
	}
	// So does the env file.
	writeFile(t, filepath.Join(dir, ".env.test"), "SECRET_TOKEN=other\n")
	if v = status(); v.EnvFileStatus.Status != "changed" {
		t.Fatalf("env: %+v", v.EnvFileStatus)
	}
	// A new up records what it was given.
	if _, err := e.compose.Up(ctx, "api-tests"); err != nil {
		t.Fatal(err)
	}
	if v = status(); v.FileStatus[0].Status != "ok" || v.EnvFileStatus.Status != "ok" {
		t.Fatalf("after the second up: %+v", v)
	}
	// A failed up does not move the record.
	writeFile(t, compose, "services: {}\n")
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if hasArg(c.Args, "up") {
			return ExecResult{1, "boom"}
		}
		return ExecResult{}
	})
	if _, err := e.compose.Up(ctx, "api-tests"); err == nil {
		t.Fatal("up should fail")
	}
	if v = status(); v.FileStatus[0].Status != "changed" {
		t.Fatalf("a failed up must not count: %+v", v.FileStatus[0])
	}
	// The state file survives a new service on the same store, and goes with the profile.
	again := NewRegistryCompose(e.store, e.log, e.sources, e.fake)
	if got, _ := again.Get(ctx, "api-tests"); got.LastUpAt == nil {
		t.Fatal("the record must be persisted")
	}
	e.fake.setHandler(nil)
	if err := e.compose.Delete(ctx, "api-tests"); err != nil {
		t.Fatal(err)
	}
	if text, _, _ := e.store.Read(FileRegistryComposeState); strings.Contains(text, "api-tests") {
		t.Fatalf("state should go with the profile: %s", text)
	}
}

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

func TestComposeNeverReadsValuesOutOfTheEnvFile(t *testing.T) {
	e, dir := composeEnvSetup(t)
	ctx := context.Background()
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{} })
	_, _ = e.compose.Up(ctx, "api-tests")
	v, _ := e.compose.Get(ctx, "api-tests")
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "do-not-leak") || strings.Contains(string(b), "SECRET_TOKEN") {
		t.Fatalf("env content leaked into the view: %s", b)
	}
	for _, l := range e.log.all() {
		if strings.Contains(l, "do-not-leak") {
			t.Fatalf("journal: %s", l)
		}
	}
	// The viewer reads compose files only: the env file has no index.
	for i := 0; i < 2; i++ {
		f, err := e.compose.File("api-tests", i)
		if err != nil || strings.Contains(f.Content, "do-not-leak") {
			t.Fatalf("file %d: %v", i, err)
		}
	}
	if _, err := e.compose.File("api-tests", 2); err == nil {
		t.Fatal("index out of range")
	}
	if _, err := e.compose.File("api-tests", -1); err == nil {
		t.Fatal("negative index")
	}
	_ = dir
}

func TestComposeFileViewer(t *testing.T) {
	e, dir := composeEnvSetup(t)
	f, err := e.compose.File("api-tests", 0)
	if err != nil || f.Truncated || f.Path != filepath.Join(dir, "deploy", "compose.yml") || !strings.Contains(f.Content, "image: x") {
		t.Fatalf("%+v %v", f, err)
	}
	big := strings.Repeat("# comment line\n", 30000) // about 450 KB
	writeFile(t, filepath.Join(dir, "deploy", "compose.yml"), big)
	f, err = e.compose.File("api-tests", 0)
	if err != nil || !f.Truncated || len(f.Content) != composeViewMax {
		t.Fatalf("truncated: %v %d %v", f.Truncated, len(f.Content), err)
	}
	// Past the 2 MB read limit the file is not usable at all.
	writeFile(t, filepath.Join(dir, "deploy", "compose.yml"), strings.Repeat("x", registryMaxFile+1))
	if _, err := e.compose.File("api-tests", 0); err == nil {
		t.Fatal("a huge file should be refused")
	}
	_ = os.Remove(filepath.Join(dir, "deploy", "compose.yml"))
	if _, err := e.compose.File("api-tests", 0); err == nil {
		t.Fatal("missing file")
	} else if _, ok := err.(RegistryConflict); !ok {
		t.Fatalf("%T", err)
	}
}

func TestComposeDeleteAndSourceInUse(t *testing.T) {
	e, _ := composeEnvSetup(t)
	ctx := context.Background()
	if used := e.compose.UsingSource("repo"); !reflect.DeepEqual(used, []string{"api-tests"}) {
		t.Fatalf("using: %v", used)
	}
	if used := e.compose.UsingSource("other"); used == nil || len(used) != 0 {
		t.Fatalf("not used must be an empty list: %#v", used)
	}
	// Running: 409 and nothing removed.
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if hasArg(c.Args, "ps") {
			return ExecResult{0, `{"Service":"api","State":"running"}`}
		}
		return ExecResult{}
	})
	err := e.compose.Delete(ctx, "api-tests")
	if rc, ok := err.(RegistryConflict); !ok || !strings.Contains(rc.Msg, "running") {
		t.Fatalf("running profile: %T %v", err, err)
	}
	if len(e.compose.List(ctx)) != 1 {
		t.Fatal("the profile must stay")
	}
	// Stopped: fine. Docker that cannot answer counts as not running.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{1, "Cannot connect to the Docker daemon"} })
	if err := e.compose.Delete(ctx, "api-tests"); err != nil {
		t.Fatal(err)
	}
	if err := e.compose.Delete(ctx, "api-tests"); err == nil {
		t.Fatal("unknown now")
	}
	for _, c := range dockerCalls(e.fake) {
		if hasArg(c.Args, "down") {
			t.Fatal("deleting a definition never removes containers")
		}
	}
}

func TestComposeBusyProfileRefusesASecondOperation(t *testing.T) {
	e, _ := composeEnvSetup(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if hasArg(c.Args, "up") {
			close(started)
			<-release
		}
		return ExecResult{}
	})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = e.compose.Up(ctx, "api-tests")
	}()
	<-started
	if _, err := e.compose.Down(ctx, "api-tests", false); err == nil {
		t.Error("a second operation on the same profile must be refused")
	} else if _, ok := err.(RegistryConflict); !ok {
		t.Errorf("%T", err)
	}
	close(release)
	wg.Wait()
}

func TestComposeErrorsAreTrimmedToTheEnd(t *testing.T) {
	e, _ := composeEnvSetup(t)
	ctx := context.Background()
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if hasArg(c.Args, "up") {
			return ExecResult{1, strings.Repeat("noise ", 1000) + "\nError response from daemon: port is already allocated"}
		}
		return ExecResult{}
	})
	_, err := e.compose.Up(ctx, "api-tests")
	re, ok := err.(RegistryError)
	if !ok || !strings.HasSuffix(re.Msg, "port is already allocated") || len(re.Msg) > composeErrTail+80 {
		t.Fatalf("%T %d", err, len(re.Msg))
	}
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{15, ""} })
	_, err = e.compose.Up(ctx, "api-tests")
	if err == nil || !strings.Contains(err.Error(), "exit code 15") {
		t.Fatalf("%v", err)
	}
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		return ExecResult{1, strings.Repeat("y", 3000) + "services.api.image must be a string"}
	})
	chk, _ := e.compose.Validate(ctx, "api-tests")
	if chk.OK || !strings.HasSuffix(chk.Message, "must be a string") || len(chk.Message) > composeErrTail+10 {
		t.Fatalf("validate message: %d", len(chk.Message))
	}
}

func TestComposeJournal(t *testing.T) {
	e, _ := composeEnvSetup(t)
	ctx := context.Background()
	_, _ = e.compose.Up(ctx, "api-tests")
	_, _ = e.compose.Down(ctx, "api-tests", true)
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if hasArg(c.Args, "up") {
			return ExecResult{1, "password=hunter2"}
		}
		return ExecResult{}
	})
	_, _ = e.compose.Up(ctx, "api-tests")
	e.fake.setHandler(nil)
	_ = e.compose.Delete(ctx, "api-tests")
	got := e.log.all()
	want := []string{
		"registry.source.save|repo|local",
		"registry.compose.save|api-tests|",
		"registry.compose.up|api-tests|tests,debug",
		"registry.compose.down|api-tests|containers and volumes",
		"registry.compose.up|api-tests|failed",
		"registry.compose.delete|api-tests|",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("journal: %v", got)
	}
}

// ---- ps parsing -----------------------------------------------------------------------------------------------------

func TestParseComposePs(t *testing.T) {
	lines := `{"Name":"nimbo-reg-x-api-1","Service":"api","State":"running","Health":"healthy"}
{"Name":"nimbo-reg-x-db-1","Service":"db","State":"exited","Health":""}
not json at all
{"Name":"nimbo-reg-x-cache-1"}
`
	arr := `[{"Service":"api","State":"Running","Health":"starting"},{"Name":"loner","State":"restarting"}]`
	st := ParseComposePs(lines)
	if st.Running != 1 || st.Total != 3 || len(st.Services) != 3 {
		t.Fatalf("lines: %+v", st)
	}
	if st.Services[0].Name != "api" || st.Services[0].Health == nil || *st.Services[0].Health != "healthy" {
		t.Fatalf("api: %+v", st.Services[0])
	}
	if st.Services[1].Health != nil || st.Services[1].State != "exited" {
		t.Fatalf("db: %+v", st.Services[1])
	}
	if st.Services[2].Name != "nimbo-reg-x-cache-1" || st.Services[2].State != "unknown" {
		t.Fatalf("cache has only a name: %+v", st.Services[2])
	}
	st = ParseComposePs(arr)
	if st.Running != 1 || st.Total != 2 || st.Services[0].State != "running" || *st.Services[0].Health != "starting" || st.Services[1].Name != "loner" {
		t.Fatalf("array: %+v", st)
	}
	for _, empty := range []string{"", "  \n ", "[]", "null", "[", "garbage"} {
		st = ParseComposePs(empty)
		if st.Total != 0 || st.Services == nil {
			t.Errorf("%q: %+v", empty, st)
		}
	}
	// A CRLF stream (Windows) parses the same.
	if st = ParseComposePs("{\"Service\":\"a\",\"State\":\"running\"}\r\n{\"Service\":\"b\",\"State\":\"running\"}\r\n"); st.Running != 2 {
		t.Fatalf("crlf: %+v", st)
	}
}

func TestComposeViewStateAndDockerDown(t *testing.T) {
	e, _ := composeEnvSetup(t)
	ctx := context.Background()
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		return ExecResult{0, `[{"Service":"api","State":"running","Health":"healthy"},{"Service":"db","State":"exited"}]`}
	})
	v, _ := e.compose.Get(ctx, "api-tests")
	if v.State.Running != 1 || v.State.Total != 2 || v.StateError != nil {
		t.Fatalf("%+v", v.State)
	}
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{1, "Cannot connect to the Docker daemon"} })
	v, _ = e.compose.Get(ctx, "api-tests")
	if v.State.Total != 0 || v.State.Services == nil || v.StateError == nil || !strings.Contains(*v.StateError, "Docker") {
		t.Fatalf("docker down: %+v %v", v.State, v.StateError)
	}
}

// ---- HTTP -----------------------------------------------------------------------------------------------------------

func regServer(e *regEnv) *http.ServeMux {
	mux := http.NewServeMux()
	NewRegistryRoutes(e.sources, e.compose).Register(mux)
	return mux
}

func call(mux *http.ServeMux, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegistryRoutes(t *testing.T) {
	e, dir := composeEnvSetup(t)
	mux := regServer(e)
	ctx := context.Background()

	// Lists are arrays, never null, and every field is sent.
	empty := newRegEnv(t)
	for _, p := range []string{"/api/registry/sources", "/api/registry/compose"} {
		rec := call(regServer(empty), "GET", p, "")
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	rec := call(mux, "GET", "/api/registry/sources", "")
	var srcs []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &srcs)
	for _, k := range []string{"id", "kind", "path", "url", "branch", "dir", "status", "head", "currentBranch", "dirty", "behind", "syncedAt", "error"} {
		if _, ok := srcs[0][k]; !ok {
			t.Errorf("source JSON lacks %q: %s", k, rec.Body)
		}
	}
	rec = call(mux, "GET", "/api/registry/compose", "")
	var comps []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &comps)
	for _, k := range []string{"id", "name", "purpose", "files", "profiles", "envFile", "state", "fileStatus", "envFileStatus", "lastUpSha", "lastUpAt"} {
		if _, ok := comps[0][k]; !ok {
			t.Errorf("compose JSON lacks %q: %s", k, rec.Body)
		}
	}

	// Source in use cannot be deleted (409); the folder and the profile stay.
	rec = call(mux, "DELETE", "/api/registry/sources/repo", "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "api-tests") {
		t.Fatalf("source in use: %d %s", rec.Code, rec.Body)
	}
	// Up answers 202 with the profile; missing files answer 409.
	rec = call(mux, "POST", "/api/registry/compose/api-tests/up", "")
	if rec.Code != 202 || !strings.Contains(rec.Body.String(), `"id": "api-tests"`) && !strings.Contains(rec.Body.String(), `"id":"api-tests"`) {
		t.Fatalf("up: %d %s", rec.Code, rec.Body)
	}
	_ = os.Remove(filepath.Join(dir, ".env.test"))
	if rec = call(mux, "POST", "/api/registry/compose/api-tests/up", ""); rec.Code != 409 {
		t.Fatalf("up with a missing file: %d %s", rec.Code, rec.Body)
	}
	// docker failure: 422 with the message. Unknown: 404. Bad body: 400.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{1, "daemon says no"} })
	writeFile(t, filepath.Join(dir, ".env.test"), "A=1")
	if rec = call(mux, "POST", "/api/registry/compose/api-tests/up", ""); rec.Code != 422 || !strings.Contains(rec.Body.String(), "daemon says no") {
		t.Fatalf("failing up: %d %s", rec.Code, rec.Body)
	}
	e.fake.setHandler(nil)
	if rec = call(mux, "POST", "/api/registry/compose/ghost/up", ""); rec.Code != 404 {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if rec = call(mux, "PUT", "/api/registry/compose/bad", "{nope"); rec.Code != 400 {
		t.Fatalf("bad body: %d", rec.Code)
	}
	if rec = call(mux, "PUT", "/api/registry/compose/Bad", `{"files":[]}`); rec.Code != 400 {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// Logs, file, validate, down.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{0, "l1\nl2"} })
	rec = call(mux, "GET", "/api/registry/compose/api-tests/logs?tail=2&service=api", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"lines":"l1\nl2"`) {
		t.Fatalf("logs: %d %s", rec.Code, rec.Body)
	}
	if rec = call(mux, "GET", "/api/registry/compose/api-tests/logs?tail=zero", ""); rec.Code != 400 {
		t.Fatalf("bad tail: %d", rec.Code)
	}
	if rec = call(mux, "GET", "/api/registry/compose/api-tests/file?index=0", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "image: x") {
		t.Fatalf("file: %d %s", rec.Code, rec.Body)
	}
	if rec = call(mux, "GET", "/api/registry/compose/api-tests/file?index=9", ""); rec.Code != 400 {
		t.Fatalf("file index: %d", rec.Code)
	}
	if rec = call(mux, "POST", "/api/registry/compose/api-tests/validate", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}
	e.fake.reset()
	if rec = call(mux, "POST", "/api/registry/compose/api-tests/down?volumes=true", ""); rec.Code != 200 {
		t.Fatalf("down: %d", rec.Code)
	}
	if c := dockerCalls(e.fake)[0]; !hasArg(c.Args, "--volumes") {
		t.Fatalf("volumes flag: %v", c.Args)
	}
	// Delete the profile, then the source.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{} })
	if rec = call(mux, "DELETE", "/api/registry/compose/api-tests", ""); rec.Code != 204 {
		t.Fatalf("delete profile: %d %s", rec.Code, rec.Body)
	}
	if rec = call(mux, "DELETE", "/api/registry/sources/repo", ""); rec.Code != 204 {
		t.Fatalf("delete source: %d %s", rec.Code, rec.Body)
	}
	if rec = call(mux, "DELETE", "/api/registry/sources/repo", ""); rec.Code != 404 {
		t.Fatalf("delete twice: %d", rec.Code)
	}
	// Sources over HTTP: save, sync a git one (fake), 422 on failure.
	rec = call(mux, "PUT", "/api/registry/sources/remote", `{"kind":"git","url":"https://h.example/o/r.git","branch":null}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"not-cloned"`) {
		t.Fatalf("put git: %d %s", rec.Code, rec.Body)
	}
	if rec = call(mux, "PUT", "/api/registry/sources/bad", `{"kind":"git","url":"file:///etc"}`); rec.Code != 400 {
		t.Fatalf("bad url: %d", rec.Code)
	}
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{128, "fatal: nope"} })
	if rec = call(mux, "POST", "/api/registry/sources/remote/sync", ""); rec.Code != 422 || !strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("sync failure: %d %s", rec.Code, rec.Body)
	}
	e.fake.setHandler(cloneFake)
	if rec = call(mux, "POST", "/api/registry/sources/remote/sync", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("sync: %d %s", rec.Code, rec.Body)
	}
	_ = ctx
}
