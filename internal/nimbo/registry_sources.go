package nimbo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The registry keeps compose files and env files in repositories. A "source" is a folder Nimbo reads live: either a
// repo you already have on disk (never copied, never written) or a Git URL that Nimbo clones into its own data folder.

// Data files of the registry (compose profiles and their state live in registry_compose.go).
const (
	FileRegistrySources      = "registry-sources.json"
	FileRegistryCompose      = "registry-compose.json"
	FileRegistryComposeState = "registry-compose-state.json"
)

const (
	maxRegistrySources = 50
	registryGitTimeout = 120
	registryGitQuick   = 10
	registryErrTail    = 2000 // characters of git output kept in an error
)

var (
	registryIDRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	registryBranchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$`)
	registryHostRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	registryURLPath  = regexp.MustCompile(`^/[A-Za-z0-9._~/-]+$`)
	registryScpRe    = regexp.MustCompile(`^git@([A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?):([A-Za-z0-9_~/.][A-Za-z0-9._~/-]*)$`)
)

// ---- running git and docker ---------------------------------------------------------------------------------------

// RegistryCommand is one program run: the arguments are a list and never pass through a shell.
type RegistryCommand struct {
	Name           string   // "git" or "docker"
	Args           []string //
	Dir            string   // working directory, "" for the current one
	Env            []string // KEY=value pairs added to the environment of the process
	TimeoutSeconds int
}

// RegistryRunner runs git and docker for the registry. Tests fake it: no unit test starts a real program.
type RegistryRunner interface {
	Run(ctx context.Context, cmd RegistryCommand) ExecResult
}

// RegistryActivity is the activity log (the journal). *StatsService fits as it is.
type RegistryActivity interface {
	Record(kind, subject string, detail ...string)
}

// ExecRegistryRunner is the real runner. Exit 124 means it timed out and 127 that it could not be started.
type ExecRegistryRunner struct{ DockerBin string }

// capWriter keeps the first n bytes, so a chatty command cannot fill the memory.
type capWriter struct {
	buf *bytes.Buffer
	n   int
}

func (c capWriter) Write(p []byte) (int, error) {
	if room := c.n - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (r ExecRegistryRunner) Run(ctx context.Context, c RegistryCommand) ExecResult {
	bin := ""
	switch c.Name {
	case "git":
		bin = "git"
	case "docker":
		bin = orDefault(r.DockerBin, "docker")
	default:
		return ExecResult{127, "program not allowed: " + c.Name}
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, c.Args...)
	hideWindow(cmd)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	var out bytes.Buffer
	w := capWriter{&out, 4 << 20}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return ExecResult{124, fmt.Sprintf("timed out after %ds", c.TimeoutSeconds)}
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ExecResult{ee.ExitCode(), strings.TrimSpace(out.String())}
		}
		return ExecResult{127, err.Error()}
	}
	return ExecResult{0, strings.TrimSpace(out.String())}
}

// RegistryError is a failure of git or docker that the person should read; routes answer it with 422.
type RegistryError struct{ Msg string }

func (e RegistryError) Error() string { return e.Msg }

// RegistryConflict is a request that is fine in itself but not possible now (in use, missing files, running); 409.
type RegistryConflict struct{ Msg string }

func (e RegistryConflict) Error() string { return e.Msg }

// registryTail keeps the last n characters of a command's output.
func registryTail(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return "..." + string(r[len(r)-n:])
}

// registryFailed turns a failed command into a message: what was being done, and the end of what the program said.
func registryFailed(what string, res ExecResult, n int) RegistryError {
	if res.ExitCode == 127 {
		return RegistryError{what + ": the program could not be started (" + registryTail(res.Output, 300) + ")"}
	}
	out := registryTail(res.Output, n)
	if out == "" {
		out = "exit code " + strconv.Itoa(res.ExitCode)
	}
	return RegistryError{what + ": " + out}
}

// ---- git URL allow-list ---------------------------------------------------------------------------------------------

// ValidateGitURL accepts https://host/path[.git], ssh://git@host/path and git@host:path, and nothing else: no file://,
// no ext::, no credentials in the URL (Nimbo stores none; git uses the user's own setup), nothing that starts with a dash.
func ValidateGitURL(raw string) error {
	bad := InvalidParam{"The Git address must be https://host/owner/repo.git, ssh://git@host/owner/repo.git or git@host:owner/repo.git"}
	if raw == "" || len(raw) > 300 || strings.HasPrefix(raw, "-") {
		return bad
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f || r > 0x7e {
			return bad
		}
	}
	if m := registryScpRe.FindStringSubmatch(raw); m != nil {
		if registryHasDotDot(m[3]) {
			return bad
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return bad
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return InvalidParam{"Do not put a user or password in the address: git uses your own credentials"}
		}
	case "ssh":
		if u.User == nil || u.User.Username() != "git" {
			return bad
		}
		if _, hasPw := u.User.Password(); hasPw {
			return bad
		}
	default:
		return bad
	}
	if !registryHostRe.MatchString(u.Hostname()) {
		return bad
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return bad
		}
	}
	if !registryURLPath.MatchString(u.Path) || registryHasDotDot(u.Path) || u.Path != u.EscapedPath() {
		return bad
	}
	return nil
}

func registryHasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// ---- model ----------------------------------------------------------------------------------------------------------

// SourceDef is what a person saves.
type SourceDef struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"` // "local" | "git"
	Path   *string `json:"path"`
	URL    *string `json:"url"`
	Branch *string `json:"branch"`
}

// registrySourceItem is what is kept on disk: the definition and when a git source was last brought up to date.
type registrySourceItem struct {
	SourceDef
	SyncedAt *int64 `json:"syncedAt"`
}

type registrySourceFile struct {
	Items []registrySourceItem `json:"items"`
}

// SourceView is a source as the API shows it.
type SourceView struct {
	SourceDef
	Dir           string  `json:"dir"`
	Status        string  `json:"status"` // ok | missing | not-cloned | error
	Head          *string `json:"head"`
	CurrentBranch *string `json:"currentBranch"`
	Dirty         bool    `json:"dirty"`
	Behind        *int    `json:"behind"`
	SyncedAt      *int64  `json:"syncedAt"`
	Error         *string `json:"error"`
}

// RegistrySources keeps the sources and talks to git about them.
type RegistrySources struct {
	store   DataStore
	log     RegistryActivity
	dataDir string
	run     RegistryRunner
	clock   func() int64

	mu      sync.Mutex // guards the file (read, change, write) and the maps below
	lastErr map[string]string
	behind  map[string]int
	busy    map[string]bool
}

func NewRegistrySources(store DataStore, log RegistryActivity, dataDir string, run RegistryRunner) *RegistrySources {
	return &RegistrySources{store: store, log: log, dataDir: dataDir, run: run, clock: NowMillis,
		lastErr: map[string]string{}, behind: map[string]int{}, busy: map[string]bool{}}
}

func (s *RegistrySources) read() registrySourceFile {
	f := coreReadOrDefault(s.store, FileRegistrySources, func() registrySourceFile { return registrySourceFile{} })
	kept := f.Items[:0]
	for _, it := range f.Items {
		// A hand-edited or restored file may hold an entry this version cannot use: it is left out, not trusted.
		if registryIDRe.MatchString(it.ID) && (it.Kind == "local" || it.Kind == "git") {
			kept = append(kept, it)
		}
	}
	f.Items = kept
	return f
}

// snapshot is the saved sources by id, for resolving the files of several profiles in one go.
func (s *RegistrySources) snapshot() map[string]registrySourceItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]registrySourceItem{}
	for _, it := range s.read().Items {
		out[it.ID] = it
	}
	return out
}

func (s *RegistrySources) record(kind, subject string, detail ...string) {
	if s.log != nil {
		s.log.Record(kind, subject, detail...)
	}
}

func (s *RegistrySources) reposDir() string {
	if s.dataDir == "" {
		return ""
	}
	return filepath.Join(s.dataDir, "repos")
}

// cloneDir is the only place a git source may live. The id has already matched registryIDRe, so it cannot hold a
// separator or a dot.
func (s *RegistrySources) cloneDir(id string) (string, error) {
	base := s.reposDir()
	if base == "" {
		return "", InvalidParam{"Git sources need the data folder, which is not available here"}
	}
	if !registryIDRe.MatchString(id) {
		return "", InvalidParam{"Invalid source name"}
	}
	return filepath.Join(base, id), nil
}

// sourceDir is the folder the files of a source resolve against.
func (s *RegistrySources) sourceDir(it registrySourceItem) string {
	if it.Kind == "git" {
		d, err := s.cloneDir(it.ID)
		if err != nil {
			return ""
		}
		return d
	}
	if it.Path == nil {
		return ""
	}
	return filepath.Clean(*it.Path)
}

// removeClone deletes the managed clone of a source (and a half-made one). It only ever removes <data>/repos/<id> and
// <data>/repos/<id>.new; a symbolic link there is unlinked, never followed.
func (s *RegistrySources) removeClone(id string) error {
	dir, err := s.cloneDir(id)
	if err != nil {
		return nil // no data folder: there is nothing of ours to remove
	}
	base := s.reposDir()
	for _, p := range []string{dir, dir + ".new"} {
		if !registryWithin(base, p) {
			return InvalidParam{"Refusing to remove a folder outside the repos folder"}
		}
		if err := os.RemoveAll(p); err != nil {
			return RegistryError{"Could not remove the clone: " + err.Error()}
		}
	}
	return nil
}

// registryWithin says whether target is strictly inside base (lexically).
func registryWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

// ---- save, list, delete ---------------------------------------------------------------------------------------------

func (s *RegistrySources) validate(def SourceDef) (registrySourceItem, error) {
	it := registrySourceItem{SourceDef: SourceDef{ID: def.ID, Kind: def.Kind}}
	if !registryIDRe.MatchString(def.ID) {
		return it, InvalidParam{"The name must be lowercase letters, digits and hyphens, starting with a letter or digit (up to 31 characters)"}
	}
	switch def.Kind {
	case "local":
		if def.Path == nil || strings.TrimSpace(*def.Path) == "" {
			return it, InvalidParam{"Choose the folder of the repository"}
		}
		p := *def.Path
		if registryHasControl(p) || len(p) > 1000 {
			return it, InvalidParam{"The folder path has characters that are not allowed"}
		}
		if !filepath.IsAbs(p) {
			return it, InvalidParam{"The folder must be an absolute path"}
		}
		p = filepath.Clean(p)
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			return it, InvalidParam{"The folder '" + p + "' does not exist"}
		}
		if s.dataDir != "" {
			if d := filepath.Clean(s.dataDir); strings.EqualFold(p, d) || registryWithin(d, p) {
				return it, InvalidParam{"That folder is where Nimbo keeps its own data: choose your repository instead"}
			}
		}
		it.Path = &p
	case "git":
		if def.URL == nil {
			return it, InvalidParam{"Write the Git address of the repository"}
		}
		u := strings.TrimSpace(*def.URL)
		if err := ValidateGitURL(u); err != nil {
			return it, err
		}
		it.URL = &u
		if def.Branch != nil && strings.TrimSpace(*def.Branch) != "" {
			b := strings.TrimSpace(*def.Branch)
			if !registryBranchRe.MatchString(b) || strings.Contains(b, "..") || strings.HasSuffix(b, "/") ||
				strings.HasSuffix(b, ".lock") || strings.Contains(b, "//") {
				return it, InvalidParam{"The branch name is not valid"}
			}
			it.Branch = &b
		}
		if _, err := s.cloneDir(def.ID); err != nil {
			return it, err
		}
	default:
		return it, InvalidParam{"The kind must be 'local' or 'git'"}
	}
	return it, nil
}

func registryHasControl(s string) bool {
	for _, r := range s {
		if r < ' ' || r == 0x7f {
			return true
		}
	}
	return false
}

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Put creates or replaces a source. A git source is not cloned yet: that is what sync is for.
func (s *RegistrySources) Put(ctx context.Context, def SourceDef) (SourceView, error) {
	it, err := s.validate(def)
	if err != nil {
		return SourceView{}, err
	}
	s.mu.Lock()
	file := s.read()
	idx := -1
	for i := range file.Items {
		if file.Items[i].ID == it.ID {
			idx = i
		}
	}
	if idx < 0 && len(file.Items) >= maxRegistrySources {
		s.mu.Unlock()
		return SourceView{}, InvalidParam{"There are already 50 sources: delete one first"}
	}
	if idx >= 0 {
		old := file.Items[idx]
		same := old.Kind == it.Kind && strEq(old.URL, it.URL) && strEq(old.Branch, it.Branch)
		if same {
			it.SyncedAt = old.SyncedAt
		} else if old.Kind == "git" {
			// The clone belongs to another address or branch now: it goes, and the next sync makes a fresh one.
			if err := s.removeClone(old.ID); err != nil {
				s.mu.Unlock()
				return SourceView{}, err
			}
			delete(s.behind, it.ID)
			delete(s.lastErr, it.ID)
		}
		file.Items[idx] = it
	} else {
		file.Items = append(file.Items, it)
	}
	err = coreWriteJSON(s.store, FileRegistrySources, file)
	s.mu.Unlock()
	if err != nil {
		return SourceView{}, err
	}
	s.record("registry.source.save", it.ID, it.Kind)
	return s.view(ctx, it), nil
}

// List returns the sources, oldest first, with their live state.
func (s *RegistrySources) List(ctx context.Context) []SourceView {
	s.mu.Lock()
	items := s.read().Items
	s.mu.Unlock()
	out := make([]SourceView, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = s.view(ctx, items[i])
		}(i)
	}
	wg.Wait()
	return out
}

func (s *RegistrySources) find(id string) (registrySourceItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.read().Items {
		if it.ID == id {
			return it, true
		}
	}
	return registrySourceItem{}, false
}

// Get returns one source, or UnknownTarget.
func (s *RegistrySources) Get(ctx context.Context, id string) (SourceView, error) {
	it, ok := s.find(id)
	if !ok {
		return SourceView{}, UnknownTarget{"Unknown source '" + id + "'"}
	}
	return s.view(ctx, it), nil
}

// Delete removes a source. A git source also loses its managed clone; the folder of a local one is never touched.
// Whether a compose profile still uses it is checked by the route (it answers 409).
func (s *RegistrySources) Delete(id string) error {
	s.mu.Lock()
	file := s.read()
	var gone *registrySourceItem
	kept := file.Items[:0]
	for i := range file.Items {
		if file.Items[i].ID == id {
			it := file.Items[i]
			gone = &it
			continue
		}
		kept = append(kept, file.Items[i])
	}
	if gone == nil {
		s.mu.Unlock()
		return UnknownTarget{"Unknown source '" + id + "'"}
	}
	file.Items = kept
	err := coreWriteJSON(s.store, FileRegistrySources, file)
	delete(s.behind, id)
	delete(s.lastErr, id)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if gone.Kind == "git" {
		if err := s.removeClone(id); err != nil {
			return err
		}
	}
	s.record("registry.source.delete", id, gone.Kind)
	return nil
}

// ---- state ----------------------------------------------------------------------------------------------------------

func registryGit(rest ...string) []string {
	return append([]string{"-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never", "-c", "core.hooksPath="}, rest...)
}

func (s *RegistrySources) git(ctx context.Context, timeout int, rest ...string) ExecResult {
	return s.run.Run(ctx, RegistryCommand{Name: "git", Args: registryGit(rest...), Env: []string{"GIT_TERMINAL_PROMPT=0"}, TimeoutSeconds: timeout})
}

func (s *RegistrySources) view(ctx context.Context, it registrySourceItem) SourceView {
	v := SourceView{SourceDef: it.SourceDef, Dir: s.sourceDir(it), SyncedAt: it.SyncedAt, Status: "ok"}
	if it.Kind == "local" {
		v.URL, v.Branch = nil, nil
	} else {
		v.Path = nil
	}
	s.mu.Lock()
	if e, ok := s.lastErr[it.ID]; ok {
		v.Error = &e
	}
	if b, ok := s.behind[it.ID]; ok && it.Kind == "git" {
		v.Behind = &b
	}
	s.mu.Unlock()

	st, err := os.Stat(v.Dir)
	exists := v.Dir != "" && err == nil && st.IsDir()
	switch {
	case !exists && it.Kind == "git":
		v.Status = "not-cloned"
	case !exists:
		v.Status = "missing"
	case v.Error != nil:
		v.Status = "error"
	}
	if !exists {
		return v
	}
	// Not a repository is fine for a local source: it just has no head.
	head := s.git(ctx, registryGitQuick, "-C", v.Dir, "rev-parse", "--short", "HEAD")
	if !head.OK() || head.Output == "" {
		return v
	}
	h := firstLine(head.Output)
	v.Head = &h
	if br := s.git(ctx, registryGitQuick, "-C", v.Dir, "rev-parse", "--abbrev-ref", "HEAD"); br.OK() {
		if b := firstLine(br.Output); b != "" && b != "HEAD" {
			v.CurrentBranch = &b
		}
	}
	if stt := s.git(ctx, registryGitQuick, "--no-optional-locks", "-C", v.Dir, "status", "--porcelain", "--untracked-files=no"); stt.OK() {
		v.Dirty = strings.TrimSpace(stt.Output) != ""
	}
	return v
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// ---- sync -----------------------------------------------------------------------------------------------------------

func (s *RegistrySources) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[id] {
		return false
	}
	s.busy[id] = true
	return true
}

func (s *RegistrySources) release(id string) {
	s.mu.Lock()
	delete(s.busy, id)
	s.mu.Unlock()
}

// Sync brings a git source up to date: a clone the first time, then a fast-forward pull. For a local source it only
// reads the state again. A failed sync leaves the working tree as it was.
func (s *RegistrySources) Sync(ctx context.Context, id string) (SourceView, error) {
	it, ok := s.find(id)
	if !ok {
		return SourceView{}, UnknownTarget{"Unknown source '" + id + "'"}
	}
	if it.Kind == "local" {
		return s.view(ctx, it), nil
	}
	if !s.claim(id) {
		return SourceView{}, RegistryConflict{"This source is already being synchronized"}
	}
	defer s.release(id)

	dest, err := s.cloneDir(id)
	if err != nil {
		return SourceView{}, err
	}
	how := "updated"
	var failure *RegistryError
	if fi, e := os.Stat(filepath.Join(dest, ".git")); e == nil && fi != nil {
		failure = s.pull(ctx, it, dest)
	} else {
		how = "cloned"
		failure = s.clone(ctx, it, dest)
	}
	if failure != nil {
		s.mu.Lock()
		s.lastErr[id] = failure.Msg
		s.mu.Unlock()
		s.record("registry.source.sync", id, "failed")
		return SourceView{}, *failure
	}
	now := s.clock()
	s.mu.Lock()
	delete(s.lastErr, id)
	s.behind[id] = 0
	file := s.read()
	for i := range file.Items {
		if file.Items[i].ID == id {
			file.Items[i].SyncedAt = &now
			it = file.Items[i]
		}
	}
	err = coreWriteJSON(s.store, FileRegistrySources, file)
	s.mu.Unlock()
	if err != nil {
		return SourceView{}, err
	}
	s.record("registry.source.sync", id, how)
	return s.view(ctx, it), nil
}

// clone makes the working tree beside the final folder and swaps it in only when it worked, so an old tree survives a
// failed attempt.
func (s *RegistrySources) clone(ctx context.Context, it registrySourceItem, dest string) *RegistryError {
	base := s.reposDir()
	tmp := dest + ".new"
	if !registryWithin(base, tmp) || !registryWithin(base, dest) {
		return &RegistryError{"Refusing to use a folder outside the repos folder"}
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return &RegistryError{"Could not create the repos folder: " + err.Error()}
	}
	if err := os.RemoveAll(tmp); err != nil {
		return &RegistryError{"Could not clear a half-made clone: " + err.Error()}
	}
	args := []string{"clone", "--depth", "50", "--no-recurse-submodules"}
	if it.Branch != nil {
		args = append(args, "--branch", *it.Branch)
	}
	args = append(args, "--", *it.URL, tmp)
	res := s.run.Run(ctx, RegistryCommand{Name: "git", Args: registryGit(args...), Dir: base,
		Env: []string{"GIT_TERMINAL_PROMPT=0"}, TimeoutSeconds: registryGitTimeout})
	if !res.OK() {
		_ = os.RemoveAll(tmp)
		e := registryFailed("Could not clone the repository", res, registryErrTail)
		return &e
	}
	if err := os.RemoveAll(dest); err != nil {
		_ = os.RemoveAll(tmp)
		return &RegistryError{"Could not replace the old copy: " + err.Error()}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return &RegistryError{"Could not put the clone in place: " + err.Error()}
	}
	return nil
}

func (s *RegistrySources) pull(ctx context.Context, it registrySourceItem, dest string) *RegistryError {
	res := s.git(ctx, registryGitTimeout, "-C", dest, "fetch", "--no-recurse-submodules")
	if !res.OK() {
		e := registryFailed("Could not fetch from the remote", res, registryErrTail)
		return &e
	}
	// How far behind it was before the pull: if the pull cannot fast-forward, that is what the person sees.
	if r := s.git(ctx, registryGitQuick, "-C", dest, "rev-list", "--count", "HEAD..@{u}"); r.OK() {
		if n, err := strconv.Atoi(firstLine(r.Output)); err == nil {
			s.mu.Lock()
			s.behind[it.ID] = n
			s.mu.Unlock()
		}
	}
	res = s.git(ctx, registryGitTimeout, "-C", dest, "pull", "--ff-only", "--no-recurse-submodules")
	if !res.OK() {
		e := registryFailed("Could not update the repository", res, registryErrTail)
		return &e
	}
	return nil
}

// ---- resolving files inside a source --------------------------------------------------------------------------------

// registryRel checks the relative path of a file in a source (before touching the disk) and returns it cleaned.
func registryRel(rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if strings.TrimSpace(rel) == "" || len(rel) > 400 || registryHasControl(rel) {
		return "", InvalidParam{"Write the path of the file inside the source"}
	}
	if strings.HasPrefix(rel, "/") || filepath.VolumeName(rel) != "" || (len(rel) > 1 && rel[1] == ':') {
		return "", InvalidParam{"'" + rel + "' is an absolute path: inside a source, use a path relative to its folder"}
	}
	clean := path.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return "", InvalidParam{"'" + rel + "' goes outside the source folder"}
	}
	return clean, nil
}

// registryExtOK applies the extension rules: .yml/.yaml for compose files; .env, .env.*, or *.env for env files.
func registryExtOK(name string, env bool) bool {
	b := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if env {
		return b == ".env" || strings.HasPrefix(b, ".env.") || strings.HasSuffix(b, ".env")
	}
	return strings.HasSuffix(b, ".yml") || strings.HasSuffix(b, ".yaml")
}

// registryEvalExisting resolves symbolic links in the part of p that exists and keeps the rest as written.
func registryEvalExisting(p string) (string, error) {
	rest := ""
	cur := p
	for {
		real, err := registryReal(cur)
		if err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// registryInsideRoot refuses a target that, once symbolic links are followed, is not inside root.
func registryInsideRoot(root, target string) error {
	realRoot, err := registryReal(root)
	if err != nil {
		return err
	}
	realT, err := registryEvalExisting(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realRoot, realT)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("outside the source folder")
	}
	return nil
}

// resolvedFile is where a file reference points now and whether it can be used.
type resolvedFile struct {
	Path      string // absolute; "" when it could not be worked out
	Status    string // ok | missing | invalid
	Message   string
	NotCloned bool
}

// registryResolve works out the absolute path of a (source, relative path) reference and checks it. Nothing is cached.
func registryResolve(snap map[string]registrySourceItem, srcDir func(registrySourceItem) string, source, rel string, env bool) resolvedFile {
	if source == "" {
		p := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(rel, "\\", "/")))
		if !filepath.IsAbs(p) || registryHasControl(rel) {
			return resolvedFile{Path: rel, Status: "invalid", Message: "A file without a source needs an absolute path"}
		}
		if !registryExtOK(p, env) {
			return resolvedFile{Path: p, Status: "invalid", Message: registryExtMessage(env)}
		}
		return registryCheckFile(p)
	}
	it, ok := snap[source]
	if !ok {
		return resolvedFile{Path: rel, Status: "invalid", Message: "The source '" + source + "' does not exist"}
	}
	clean, err := registryRel(rel)
	if err != nil {
		return resolvedFile{Path: rel, Status: "invalid", Message: err.Error()}
	}
	if !registryExtOK(clean, env) {
		return resolvedFile{Path: clean, Status: "invalid", Message: registryExtMessage(env)}
	}
	dir := srcDir(it)
	if dir == "" {
		return resolvedFile{Path: clean, Status: "invalid", Message: "The source '" + source + "' has no folder"}
	}
	full := filepath.Join(dir, filepath.FromSlash(clean))
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if it.Kind == "git" {
			return resolvedFile{Path: full, Status: "missing", Message: "The source '" + source + "' has not been cloned yet: synchronize it first", NotCloned: true}
		}
		return resolvedFile{Path: full, Status: "missing", Message: "The folder of the source '" + source + "' does not exist"}
	}
	if err := registryInsideRoot(dir, full); err != nil {
		return resolvedFile{Path: full, Status: "invalid", Message: "'" + clean + "' leads outside the source folder"}
	}
	return registryCheckFile(full)
}

func registryExtMessage(env bool) string {
	if env {
		return "An env file must be named .env, .env.something or something.env"
	}
	return "A compose file must end in .yml or .yaml"
}

// registryMaxFile is the biggest file Nimbo will read to fingerprint or show.
const registryMaxFile = 2 << 20

func registryCheckFile(p string) resolvedFile {
	st, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolvedFile{Path: p, Status: "missing", Message: "The file does not exist"}
		}
		return resolvedFile{Path: p, Status: "invalid", Message: "The file cannot be read"}
	}
	if !st.Mode().IsRegular() {
		return resolvedFile{Path: p, Status: "invalid", Message: "That is not a regular file"}
	}
	if st.Size() > registryMaxFile {
		return resolvedFile{Path: p, Status: "invalid", Message: "The file is bigger than 2 MB"}
	}
	return resolvedFile{Path: p, Status: "ok"}
}

// registryReadCapped reads at most max bytes of a file and says whether there was more.
func registryReadCapped(p string, max int) (string, bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return "", false, err
	}
	if len(b) > max {
		b = b[:max]
		// Do not cut a character in half.
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
		return string(b), true, nil
	}
	return string(b), false, nil
}
