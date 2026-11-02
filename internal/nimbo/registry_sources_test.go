package nimbo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// ---- fakes ----------------------------------------------------------------------------------------------------------

// regFake stands in for git and docker: it remembers every command and answers through handler.
type regFake struct {
	mu      sync.Mutex
	calls   []RegistryCommand
	handler func(c RegistryCommand) ExecResult
}

func (f *regFake) Run(_ context.Context, c RegistryCommand) ExecResult {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		return h(c)
	}
	return ExecResult{}
}

func (f *regFake) all() []RegistryCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RegistryCommand{}, f.calls...)
}

func (f *regFake) reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *regFake) setHandler(h func(c RegistryCommand) ExecResult) {
	f.mu.Lock()
	f.handler = h
	f.mu.Unlock()
}

// regLog is an activity log that keeps what it was told.
type regLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *regLog) Record(kind, subject string, detail ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, kind+"|"+subject+"|"+strings.Join(detail, ","))
}

func (l *regLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.entries...)
}

type regEnv struct {
	store   *MemoryDataStore
	data    string
	fake    *regFake
	log     *regLog
	sources *RegistrySources
	compose *RegistryCompose
}

func newRegEnv(t *testing.T) *regEnv {
	t.Helper()
	e := &regEnv{store: NewMemoryDataStore(), data: filepath.Join(t.TempDir(), "data"), fake: &regFake{}, log: &regLog{}}
	e.sources = NewRegistrySources(e.store, e.log, e.data, e.fake)
	e.compose = NewRegistryCompose(e.store, e.log, e.sources, e.fake)
	return e
}

func strp(s string) *string { return &s }

func (e *regEnv) local(t *testing.T, id, dir string) {
	t.Helper()
	if _, err := e.sources.Put(context.Background(), SourceDef{ID: id, Kind: "local", Path: &dir}); err != nil {
		t.Fatalf("saving the source: %v", err)
	}
}

func writeFile(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cloneFake answers a clone by making the folder it was asked to clone into, like git does.
func cloneFake(c RegistryCommand) ExecResult {
	for i, a := range c.Args {
		if a == "clone" {
			dest := c.Args[len(c.Args)-1]
			_ = os.MkdirAll(filepath.Join(dest, ".git"), 0o755)
			_ = os.WriteFile(filepath.Join(dest, "compose.yml"), []byte("services: {}\n"), 0o644)
			_ = i
		}
	}
	return ExecResult{}
}

// ---- URL allow-list -------------------------------------------------------------------------------------------------

func TestValidateGitURL(t *testing.T) {
	good := []string{
		"https://github.com/me/repo.git",
		"https://github.com/me/repo",
		"https://git.example.com:8443/team/sub/repo.git",
		"ssh://git@github.com/me/repo.git",
		"ssh://git@host.example:2222/me/repo.git",
		"git@github.com:me/repo.git",
		"git@gitlab.example.org:group/sub/repo",
	}
	for _, u := range good {
		if err := ValidateGitURL(u); err != nil {
			t.Errorf("%q should be accepted: %v", u, err)
		}
	}
	bad := []string{
		"", "   ", "-oProxyCommand=evil", "--upload-pack=evil", "file:///etc/passwd", "file://C:/repo", "/home/me/repo", "C:\\repo",
		"ext::sh -c touch /tmp/x", "ext::git", "http://github.com/me/repo.git", "git://github.com/me/repo.git", "ftp://h/x",
		"https://user:pass@github.com/me/repo.git", "https://user@github.com/me/repo.git", "ssh://root@github.com/me/repo.git",
		"ssh://github.com/me/repo.git", "https://github.com", "https://github.com/", "https://github.com/me/repo.git?x=1",
		"https://github.com/me/repo.git#frag", "https://github.com/me repo", "https://github.com/me/re\tpo", "https://github.com/me/\x00repo",
		"https://github.com/../etc", "https://github.com/me/../../x", "git@github.com:../x", "git@github.com:", "git@host:-x",
		"https://-evil.com/x", "https://exa mple.com/x", "https://github.com/me/%2e%2e/x", "https://github.com:99999/me/r",
		"git@host:me/repo\nfoo", "https://github.com/me/répo", "ssh://git:pw@github.com/me/repo", "other@github.com:me/repo",
		"https://github.com/" + strings.Repeat("a", 400),
	}
	for _, u := range bad {
		if err := ValidateGitURL(u); err == nil {
			t.Errorf("%q should be refused", u)
		}
	}
}

// ---- save and validate ----------------------------------------------------------------------------------------------

func TestSourcePutValidation(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	writeFile(t, file, "x")
	cases := []struct {
		name string
		def  SourceDef
	}{
		{"bad id", SourceDef{ID: "Bad_ID", Kind: "local", Path: &dir}},
		{"id with dots", SourceDef{ID: "..", Kind: "local", Path: &dir}},
		{"unknown kind", SourceDef{ID: "a", Kind: "svn", Path: &dir}},
		{"local without path", SourceDef{ID: "a", Kind: "local"}},
		{"relative path", SourceDef{ID: "a", Kind: "local", Path: strp("some/where")}},
		{"missing folder", SourceDef{ID: "a", Kind: "local", Path: strp(filepath.Join(dir, "nope"))}},
		{"a file, not a folder", SourceDef{ID: "a", Kind: "local", Path: &file}},
		{"inside the data folder", SourceDef{ID: "a", Kind: "local", Path: strp(e.data)}},
		{"git without url", SourceDef{ID: "a", Kind: "git"}},
		{"git file url", SourceDef{ID: "a", Kind: "git", URL: strp("file:///tmp/x")}},
		{"git bad branch", SourceDef{ID: "a", Kind: "git", URL: strp("https://h.example/o/r.git"), Branch: strp("--evil")}},
		{"git branch dotdot", SourceDef{ID: "a", Kind: "git", URL: strp("https://h.example/o/r.git"), Branch: strp("a..b")}},
	}
	_ = os.MkdirAll(e.data, 0o755)
	for _, c := range cases {
		if _, err := e.sources.Put(ctx, c.def); err == nil {
			t.Errorf("%s: should be refused", c.name)
		} else if _, ok := err.(InvalidParam); !ok {
			t.Errorf("%s: wanted InvalidParam, got %T", c.name, err)
		}
	}
	if got := e.sources.List(ctx); len(got) != 0 {
		t.Fatalf("nothing should be saved: %v", got)
	}
	// Without a data folder a git source cannot exist.
	bare := NewRegistrySources(NewMemoryDataStore(), nil, "", e.fake)
	if _, err := bare.Put(ctx, SourceDef{ID: "a", Kind: "git", URL: strp("https://h.example/o/r.git")}); err == nil {
		t.Fatal("a git source needs a data folder")
	}
}

func TestSourcePersistsAndRoundTrips(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	e.local(t, "mine", dir)
	if _, err := e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: strp("https://h.example/o/r.git"), Branch: strp("main")}); err != nil {
		t.Fatal(err)
	}
	again := NewRegistrySources(e.store, e.log, e.data, e.fake)
	got := again.List(ctx)
	if len(got) != 2 || got[0].ID != "mine" || got[1].ID != "remote" {
		t.Fatalf("round trip lost sources: %+v", got)
	}
	if got[0].Kind != "local" || got[0].Path == nil || got[0].URL != nil || got[0].Branch != nil {
		t.Fatalf("local source fields: %+v", got[0])
	}
	if got[1].Path != nil || *got[1].URL != "https://h.example/o/r.git" || *got[1].Branch != "main" {
		t.Fatalf("git source fields: %+v", got[1])
	}
	if got[1].Dir != filepath.Join(e.data, "repos", "remote") || got[1].Status != "not-cloned" {
		t.Fatalf("git source state: %+v", got[1])
	}
	// A damaged file does not take the screen down.
	_ = e.store.Write(FileRegistrySources, "{not json")
	if len(again.List(ctx)) != 0 {
		t.Fatal("a damaged file should read as empty")
	}
}

// ---- state ----------------------------------------------------------------------------------------------------------

func TestLocalSourceStatusFromGit(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	e.local(t, "mine", dir)
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		j := strings.Join(c.Args, " ")
		switch {
		case strings.HasSuffix(j, "rev-parse --short HEAD"):
			return ExecResult{0, "abc1234\n"}
		case strings.HasSuffix(j, "rev-parse --abbrev-ref HEAD"):
			return ExecResult{0, "feature/x"}
		case strings.HasSuffix(j, "status --porcelain --untracked-files=no"):
			return ExecResult{0, " M compose.yml"}
		}
		return ExecResult{1, "unexpected " + j}
	})
	v, err := e.sources.Get(ctx, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "ok" || v.Head == nil || *v.Head != "abc1234" || v.CurrentBranch == nil || *v.CurrentBranch != "feature/x" || !v.Dirty || v.Behind != nil {
		t.Fatalf("status: %+v", v)
	}
	if v.Dir != filepath.Clean(dir) {
		t.Fatalf("dir %q", v.Dir)
	}
	for _, c := range e.fake.all() {
		if c.Name != "git" || c.Args[len(c.Args)-1] == "" {
			t.Fatalf("odd command %+v", c)
		}
	}
	// The status call does not write into the user's repository.
	var sawNoLocks bool
	for _, c := range e.fake.all() {
		if reflect.DeepEqual(c.Args[6:8], []string{"--no-optional-locks", "-C"}) {
			sawNoLocks = true
		}
	}
	if !sawNoLocks {
		t.Fatalf("status should use --no-optional-locks: %+v", e.fake.all())
	}

	// Not a repository: fine, just no head.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{128, "fatal: not a git repository"} })
	v, _ = e.sources.Get(ctx, "mine")
	if v.Status != "ok" || v.Head != nil || v.CurrentBranch != nil || v.Dirty {
		t.Fatalf("not a repo: %+v", v)
	}

	// The folder goes away.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	v, _ = e.sources.Get(ctx, "mine")
	if v.Status != "missing" || v.Head != nil {
		t.Fatalf("missing: %+v", v)
	}
	if _, err := e.sources.Get(ctx, "nope"); err == nil {
		t.Fatal("unknown source")
	}
}

// ---- sync -----------------------------------------------------------------------------------------------------------

var safeGit = []string{"-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never", "-c", "core.hooksPath="}

func gitArgs(rest ...string) []string { return append(append([]string{}, safeGit...), rest...) }

func TestSyncClonesThenPulls(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	e.sources.clock = func() int64 { return 777 }
	url := "https://h.example/o/r.git"
	if _, err := e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url, Branch: strp("main")}); err != nil {
		t.Fatal(err)
	}
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if strings.Contains(strings.Join(c.Args, " "), " clone ") {
			return cloneFake(c)
		}
		return ExecResult{}
	})
	v, err := e.sources.Sync(ctx, "remote")
	if err != nil {
		t.Fatal(err)
	}
	repos := filepath.Join(e.data, "repos")
	dest := filepath.Join(repos, "remote")
	calls := e.fake.all()
	clone := calls[0]
	want := gitArgs("clone", "--depth", "50", "--no-recurse-submodules", "--branch", "main", "--", url, dest+".new")
	if !reflect.DeepEqual(clone.Args, want) || clone.Name != "git" || clone.Dir != repos || clone.TimeoutSeconds != 120 ||
		!reflect.DeepEqual(clone.Env, []string{"GIT_TERMINAL_PROMPT=0"}) {
		t.Fatalf("clone command: %+v", clone)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("the clone should be in place: %v", err)
	}
	if _, err := os.Stat(dest + ".new"); !os.IsNotExist(err) {
		t.Fatal("the half-made folder should be gone")
	}
	if v.Status != "ok" || v.SyncedAt == nil || *v.SyncedAt != 777 {
		t.Fatalf("after clone: %+v", v)
	}

	// Second time: fetch, count what is behind, fast-forward pull.
	e.fake.reset()
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if strings.Contains(strings.Join(c.Args, " "), "rev-list") {
			return ExecResult{0, "3"}
		}
		return ExecResult{}
	})
	if _, err := e.sources.Sync(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	var syncCalls [][]string
	for _, c := range e.fake.all() {
		if c.Name == "git" && c.TimeoutSeconds >= 10 && len(c.Args) > 7 && c.Args[6] == "-C" && c.Args[7] == dest {
			syncCalls = append(syncCalls, c.Args[8:])
		}
		if c.Env[0] != "GIT_TERMINAL_PROMPT=0" {
			t.Fatalf("prompts must be off: %+v", c)
		}
	}
	wantCalls := [][]string{
		{"fetch", "--no-recurse-submodules"},
		{"rev-list", "--count", "HEAD..@{u}"},
		{"pull", "--ff-only", "--no-recurse-submodules"},
	}
	if len(syncCalls) < 3 || !reflect.DeepEqual(syncCalls[:3], wantCalls) {
		t.Fatalf("pull sequence: %v", syncCalls)
	}
	for _, c := range e.fake.all() {
		if len(c.Args) > 8 && c.Args[8] == "clone" {
			t.Fatal("the second sync must not clone")
		}
	}
}

func TestSyncFailureKeepsTheOldTreeAndTrimsTheError(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	url := "https://h.example/o/r.git"
	_, _ = e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url})
	e.fake.setHandler(cloneFake)
	if _, err := e.sources.Sync(ctx, "remote"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(e.data, "repos", "remote")
	writeFile(t, filepath.Join(dest, "local-work.txt"), "keep me")

	noise := strings.Repeat("x", 5000)
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		if strings.Contains(strings.Join(c.Args, " "), "pull") {
			return ExecResult{128, noise + "\nfatal: Not possible to fast-forward, aborting."}
		}
		return ExecResult{}
	})
	_, err := e.sources.Sync(ctx, "remote")
	re, ok := err.(RegistryError)
	if !ok {
		t.Fatalf("wanted a RegistryError, got %T %v", err, err)
	}
	if !strings.HasSuffix(re.Msg, "Not possible to fast-forward, aborting.") || len(re.Msg) > registryErrTail+120 {
		t.Fatalf("the error should keep the end of the output only (%d chars): %.100s", len(re.Msg), re.Msg)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "local-work.txt")); err != nil || string(b) != "keep me" {
		t.Fatal("a failed sync must keep the working tree")
	}
	v, _ := e.sources.Get(ctx, "remote")
	if v.Status != "error" || v.Error == nil || !strings.Contains(*v.Error, "fast-forward") {
		t.Fatalf("status after failure: %+v", v)
	}
	if v.Behind == nil {
		t.Fatal("behind should be known after a fetch")
	}
	// A later success clears the error.
	e.fake.setHandler(nil)
	if v, err := e.sources.Sync(ctx, "remote"); err != nil || v.Status != "ok" || v.Error != nil {
		t.Fatalf("recovery: %+v %v", v, err)
	}
}

func TestFailedCloneLeavesNothingBehind(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	url := "https://h.example/o/r.git"
	_, _ = e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url})
	e.fake.setHandler(func(c RegistryCommand) ExecResult {
		_ = cloneFake(c) // git leaves a half folder behind when it fails
		return ExecResult{128, "fatal: repository not found"}
	})
	_, err := e.sources.Sync(ctx, "remote")
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("error: %v", err)
	}
	repos := filepath.Join(e.data, "repos")
	entries, _ := os.ReadDir(repos)
	if len(entries) != 0 {
		t.Fatalf("nothing should remain in the repos folder: %v", entries)
	}
	// Git itself missing is explained.
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{127, "exec: \"git\": executable file not found"} })
	if _, err := e.sources.Sync(ctx, "remote"); err == nil || !strings.Contains(err.Error(), "could not be started") {
		t.Fatalf("git missing: %v", err)
	}
}

func TestChangingTheAddressDropsTheClone(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	url := "https://h.example/o/r.git"
	_, _ = e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url})
	e.fake.setHandler(cloneFake)
	_, _ = e.sources.Sync(ctx, "remote")
	dest := filepath.Join(e.data, "repos", "remote")
	// Same definition again: the clone stays.
	_, _ = e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url})
	if _, err := os.Stat(dest); err != nil {
		t.Fatal("an unchanged source keeps its clone")
	}
	other := "https://h.example/o/other.git"
	v, err := e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &other})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) || v.Status != "not-cloned" || v.SyncedAt != nil {
		t.Fatalf("a new address needs a new clone: %+v", v)
	}
}

func TestSyncOfALocalSourceOnlyReadsState(t *testing.T) {
	e := newRegEnv(t)
	e.local(t, "mine", t.TempDir())
	e.fake.reset()
	if _, err := e.sources.Sync(context.Background(), "mine"); err != nil {
		t.Fatal(err)
	}
	for _, c := range e.fake.all() {
		for _, a := range c.Args {
			if a == "clone" || a == "pull" || a == "fetch" {
				t.Fatalf("a local source must never be changed: %v", c.Args)
			}
		}
	}
}

// ---- delete ---------------------------------------------------------------------------------------------------------

func TestDeleteGitSourceRemovesOnlyItsClone(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	url := "https://h.example/o/r.git"
	for _, id := range []string{"one", "two"} {
		_, _ = e.sources.Put(ctx, SourceDef{ID: id, Kind: "git", URL: &url})
	}
	e.fake.setHandler(cloneFake)
	_, _ = e.sources.Sync(ctx, "one")
	_, _ = e.sources.Sync(ctx, "two")
	writeFile(t, filepath.Join(e.data, "secrets.json"), "{}")
	writeFile(t, filepath.Join(e.data, "repos", "stranger", "keep.txt"), "x")
	writeFile(t, filepath.Join(e.data, "repos", "one.new", "half.txt"), "x")
	if err := e.sources.Delete("one"); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		filepath.Join(e.data, "repos", "one"):                  false,
		filepath.Join(e.data, "repos", "one.new"):              false,
		filepath.Join(e.data, "repos", "two", "compose.yml"):   true,
		filepath.Join(e.data, "repos", "stranger", "keep.txt"): true,
		filepath.Join(e.data, "secrets.json"):                  true,
	} {
		_, err := os.Stat(p)
		if (err == nil) != want {
			t.Errorf("%s: exists=%v, wanted %v", p, err == nil, want)
		}
	}
	if len(e.sources.List(ctx)) != 1 {
		t.Fatal("one source should remain")
	}
	if err := e.sources.Delete("one"); err == nil {
		t.Fatal("deleting twice is an unknown target")
	} else if _, ok := err.(UnknownTarget); !ok {
		t.Fatalf("%T", err)
	}
}

func TestDeleteLocalSourceNeverTouchesTheFolder(t *testing.T) {
	e := newRegEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yml"), "services: {}")
	e.local(t, "mine", dir)
	if err := e.sources.Delete("mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err != nil {
		t.Fatal("the user's folder must stay as it is")
	}
	for _, c := range e.fake.all() {
		for _, a := range c.Args {
			if a == "rm" || a == "clean" {
				t.Fatal("nothing should be cleaned")
			}
		}
	}
}

func TestRemoveCloneRefusesToLeaveTheReposFolder(t *testing.T) {
	e := newRegEnv(t)
	outside := filepath.Join(e.data, "outside")
	writeFile(t, filepath.Join(outside, "x.txt"), "x")
	// An id with separators can never reach the disk: cloneDir refuses it before anything is removed.
	for _, id := range []string{"..", "../outside", "a/b", "a\\b", "", "A", "a.b"} {
		if _, err := e.sources.cloneDir(id); err == nil {
			t.Errorf("%q must not name a clone", id)
		}
	}
	if registryWithin(filepath.Join(e.data, "repos"), filepath.Join(e.data, "repos")) {
		t.Fatal("the repos folder itself is not inside itself")
	}
	if registryWithin(filepath.Join(e.data, "repos"), outside) {
		t.Fatal("a sibling is not inside")
	}
	if _, err := os.Stat(filepath.Join(outside, "x.txt")); err != nil {
		t.Fatal(err)
	}
}

// ---- journal --------------------------------------------------------------------------------------------------------

func TestSourceJournalHasNoSecrets(t *testing.T) {
	e := newRegEnv(t)
	ctx := context.Background()
	url := "https://h.example/o/r.git"
	_, _ = e.sources.Put(ctx, SourceDef{ID: "remote", Kind: "git", URL: &url})
	e.fake.setHandler(cloneFake)
	_, _ = e.sources.Sync(ctx, "remote")
	e.fake.setHandler(func(c RegistryCommand) ExecResult { return ExecResult{1, "token ghp_SECRET leaked in output"} })
	_, _ = e.sources.Sync(ctx, "remote")
	_ = e.sources.Delete("remote")
	got := e.log.all()
	want := []string{
		"registry.source.save|remote|git",
		"registry.source.sync|remote|cloned",
		"registry.source.sync|remote|failed",
		"registry.source.delete|remote|git",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("journal: %v", got)
	}
	for _, l := range got {
		if strings.Contains(l, "ghp_") || strings.Contains(l, "h.example") {
			t.Fatalf("the journal must not carry output or addresses: %s", l)
		}
	}
}

// ---- file resolution ------------------------------------------------------------------------------------------------

func TestRegistryRel(t *testing.T) {
	good := map[string]string{"a.yml": "a.yml", "deploy/compose.yml": "deploy/compose.yml", "deploy\\x.yml": "deploy/x.yml", "./a/../b.yml": "b.yml"}
	for in, want := range good {
		got, err := registryRel(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "  ", "..", "../a.yml", "a/../../b.yml", "/etc/passwd", "\\windows\\x.yml", "C:\\x.yml", "C:x.yml", ".", "a\x00.yml", "a\n.yml"} {
		if _, err := registryRel(in); err == nil {
			t.Errorf("%q should be refused", in)
		}
	}
}

func TestRegistryExtensions(t *testing.T) {
	for _, n := range []string{"a.yml", "a.YAML", "dir/compose.override.yml"} {
		if !registryExtOK(n, false) {
			t.Errorf("%s is a compose file", n)
		}
	}
	for _, n := range []string{"a.json", "a", "a.yml.txt", ".env", "x.sh"} {
		if registryExtOK(n, false) {
			t.Errorf("%s is not a compose file", n)
		}
	}
	for _, n := range []string{".env", ".env.test", "dir/.env.local", "prod.env", "DIR/.ENV"} {
		if !registryExtOK(n, true) {
			t.Errorf("%s is an env file", n)
		}
	}
	for _, n := range []string{"env", "a.yml", ".environment", "a.env.bak", "id_rsa", "secrets.json"} {
		if registryExtOK(n, true) {
			t.Errorf("%s is not an env file", n)
		}
	}
}

func TestResolveRefusesTraversalAndSymlinks(t *testing.T) {
	e := newRegEnv(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside")
	writeFile(t, filepath.Join(repo, "ok.yml"), "services: {}")
	writeFile(t, filepath.Join(outside, "secret.yml"), "stolen: true")
	writeFile(t, filepath.Join(outside, "secret.env"), "TOKEN=1")
	e.local(t, "r", repo)
	snap := e.sources.snapshot()
	res := func(rel string, env bool) resolvedFile {
		return registryResolve(snap, e.sources.sourceDir, "r", rel, env)
	}
	if r := res("ok.yml", false); r.Status != "ok" || r.Path != filepath.Join(repo, "ok.yml") {
		t.Fatalf("plain file: %+v", r)
	}
	if r := res("nope.yml", false); r.Status != "missing" {
		t.Fatalf("missing file: %+v", r)
	}
	for _, rel := range []string{"../outside/secret.yml", "a/../../outside/secret.yml", "..\\outside\\secret.yml", "/abs.yml"} {
		if r := res(rel, false); r.Status != "invalid" {
			t.Errorf("%q: %+v", rel, r)
		}
	}
	if r := res("ok.txt", false); r.Status != "invalid" {
		t.Fatalf("extension: %+v", r)
	}
	if r := registryResolve(snap, e.sources.sourceDir, "ghost", "ok.yml", false); r.Status != "invalid" {
		t.Fatalf("unknown source: %+v", r)
	}

	// A folder link inside the repo that points outside it (a junction on Windows, where symbolic links need rights).
	linkDir(t, filepath.Join(repo, "linkdir"), outside)
	for _, rel := range []string{"linkdir/secret.yml", "linkdir/new.yml", "linkdir/deeper/new.yml"} {
		if r := res(rel, false); r.Status != "invalid" {
			t.Errorf("link escape %q must be refused: %+v", rel, r)
		}
	}
	if r := res("linkdir/secret.env", true); r.Status != "invalid" {
		t.Errorf("link escape for env: %+v", r)
	}
	// A file link to the outside, when links are allowed here.
	if err := os.Symlink(filepath.Join(outside, "secret.yml"), filepath.Join(repo, "link.yml")); err == nil {
		if r := res("link.yml", false); r.Status != "invalid" {
			t.Errorf("file link escape must be refused: %+v", r)
		}
		if err := os.Symlink(filepath.Join(repo, "ok.yml"), filepath.Join(repo, "inside.yml")); err == nil {
			if r := res("inside.yml", false); r.Status != "ok" {
				t.Errorf("a link inside the source is allowed: %+v", r)
			}
		}
	}
	// A folder link that stays inside is fine.
	writeFile(t, filepath.Join(repo, "real", "c.yml"), "services: {}")
	linkDir(t, filepath.Join(repo, "alias"), filepath.Join(repo, "real"))
	if r := res("alias/c.yml", false); r.Status != "ok" {
		t.Errorf("a folder link inside the source is allowed: %+v", r)
	}
}

// linkDir makes link point at target: a symbolic link, or a junction on Windows when links are not allowed.
func linkDir(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err == nil {
			return
		} else {
			t.Skipf("no way to make a folder link here: %v %s", err, out)
		}
	}
	t.Skip("no way to make a folder link here")
}

func TestResolveOfAGitSourceNeverClonedSaysSo(t *testing.T) {
	e := newRegEnv(t)
	url := "https://h.example/o/r.git"
	_, _ = e.sources.Put(context.Background(), SourceDef{ID: "remote", Kind: "git", URL: &url})
	r := registryResolve(e.sources.snapshot(), e.sources.sourceDir, "remote", "compose.yml", false)
	if r.Status != "missing" || !r.NotCloned || !strings.Contains(r.Message, "synchronize") {
		t.Fatalf("%+v", r)
	}
}

func TestRegistryTail(t *testing.T) {
	if registryTail("  short  ", 10) != "short" {
		t.Fatal("short output stays")
	}
	got := registryTail(strings.Repeat("a", 50)+"END", 10)
	if !strings.HasSuffix(got, "aaaaaaaEND") || len([]rune(got)) != 13 {
		t.Fatalf("%q", got)
	}
}
