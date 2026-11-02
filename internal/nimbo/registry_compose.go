package nimbo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// A compose profile is a saved set of compose files (plus --profile names and an env file) that Nimbo brings up and
// down with `docker compose`. Files are read from their source every time: nothing is copied or cached.

const (
	maxRegistryCompose  = 50
	maxComposeFiles     = 10
	maxComposeProfiles  = 10
	composeUpTimeout    = 300
	composeDownTimeout  = 120
	composeCheckTimeout = 60
	composeQuickTimeout = 30
	composeErrTail      = 1500
	composeViewMax      = 256 * 1024
	registryProjectName = "nimbo-reg-"
)

var composeProfileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

// ComposeFileRef points at a file: inside a source (Source is its id) or, when Source is "", an absolute path.
type ComposeFileRef struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

// ComposeDef is what a person saves.
type ComposeDef struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Purpose  string           `json:"purpose"`
	Files    []ComposeFileRef `json:"files"`
	Profiles []string         `json:"profiles"`
	EnvFile  *ComposeFileRef  `json:"envFile"`
}

type registryComposeFile struct {
	Items []ComposeDef `json:"items"`
}

type composeRun struct {
	LastUpSha map[string]string `json:"lastUpSha"`
	LastUpAt  *int64            `json:"lastUpAt"`
}

type registryComposeStateFile struct {
	Items map[string]composeRun `json:"items"`
}

// ComposeService is one container of a profile.
type ComposeService struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Health *string `json:"health"`
}

type ComposeState struct {
	Running  int              `json:"running"`
	Total    int              `json:"total"`
	Services []ComposeService `json:"services"`
}

// ComposeFileStatus is the live state of one file. Status: ok | missing | changed | invalid. Message explains
// missing and invalid; it is null for the others.
type ComposeFileStatus struct {
	Path    string  `json:"path"`
	Status  string  `json:"status"`
	Sha256  *string `json:"sha256"`
	Message *string `json:"message"`
}

type ComposeView struct {
	ComposeDef
	State         ComposeState        `json:"state"`
	StateError    *string             `json:"stateError"`
	FileStatus    []ComposeFileStatus `json:"fileStatus"`
	EnvFileStatus *ComposeFileStatus  `json:"envFileStatus"`
	LastUpSha     map[string]string   `json:"lastUpSha"`
	LastUpAt      *int64              `json:"lastUpAt"`
}

// ComposeCheck is the answer of validate.
type ComposeCheck struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ComposeFileContent is a live read of one compose file.
type ComposeFileContent struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// RegistryCompose keeps the profiles and runs docker compose for them.
type RegistryCompose struct {
	store   DataStore
	log     RegistryActivity
	sources *RegistrySources
	run     RegistryRunner
	clock   func() int64

	mu   sync.Mutex // guards the two files and busy
	busy map[string]bool
}

func NewRegistryCompose(store DataStore, log RegistryActivity, sources *RegistrySources, run RegistryRunner) *RegistryCompose {
	return &RegistryCompose{store: store, log: log, sources: sources, run: run, clock: NowMillis, busy: map[string]bool{}}
}

func (c *RegistryCompose) record(kind, subject string, detail ...string) {
	if c.log != nil {
		c.log.Record(kind, subject, detail...)
	}
}

func (c *RegistryCompose) read() registryComposeFile {
	f := coreReadOrDefault(c.store, FileRegistryCompose, func() registryComposeFile { return registryComposeFile{} })
	kept := f.Items[:0]
	for _, it := range f.Items {
		if registryIDRe.MatchString(it.ID) {
			if it.Files == nil {
				it.Files = []ComposeFileRef{}
			}
			if it.Profiles == nil {
				it.Profiles = []string{}
			}
			kept = append(kept, it)
		}
	}
	f.Items = kept
	return f
}

func (c *RegistryCompose) readState() registryComposeStateFile {
	f := coreReadOrDefault(c.store, FileRegistryComposeState, func() registryComposeStateFile {
		return registryComposeStateFile{Items: map[string]composeRun{}}
	})
	if f.Items == nil {
		f.Items = map[string]composeRun{}
	}
	return f
}

// projectName is the compose project of a profile. Nimbo only ever runs compose for names built here.
func composeProject(id string) (string, error) {
	if !registryIDRe.MatchString(id) {
		return "", InvalidParam{"Invalid profile name"}
	}
	return registryProjectName + id, nil
}

// ---- save, list, delete ---------------------------------------------------------------------------------------------

func (c *RegistryCompose) validate(def ComposeDef) (ComposeDef, error) {
	if !registryIDRe.MatchString(def.ID) {
		return def, InvalidParam{"The name must be lowercase letters, digits and hyphens, starting with a letter or digit (up to 31 characters)"}
	}
	def.Name = strings.TrimSpace(def.Name)
	if def.Name == "" {
		def.Name = def.ID
	}
	if utf8.RuneCountInString(def.Name) > 60 || registryHasControl(def.Name) {
		return def, InvalidParam{"The display name can have at most 60 characters"}
	}
	def.Purpose = strings.TrimSpace(def.Purpose)
	if utf8.RuneCountInString(def.Purpose) > 200 || registryHasControl(def.Purpose) {
		return def, InvalidParam{"The purpose can have at most 200 characters"}
	}
	if len(def.Files) == 0 {
		return def, InvalidParam{"Add at least one compose file"}
	}
	if len(def.Files) > maxComposeFiles {
		return def, InvalidParam{"A profile can have at most 10 compose files"}
	}
	snap := c.sources.snapshot()
	seen := map[string]bool{}
	check := func(ref ComposeFileRef, env bool) error {
		what := "compose file"
		if env {
			what = "env file"
		}
		if ref.Source == "" {
			p := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(ref.Path, "\\", "/")))
			if registryHasControl(ref.Path) || !filepath.IsAbs(p) {
				return InvalidParam{"The " + what + " '" + ref.Path + "' needs an absolute path, or pick a source"}
			}
		} else {
			if _, ok := snap[ref.Source]; !ok {
				return InvalidParam{"The source '" + ref.Source + "' does not exist"}
			}
			if _, err := registryRel(ref.Path); err != nil {
				return err
			}
		}
		if !registryExtOK(ref.Path, env) {
			return InvalidParam{registryExtMessage(env)}
		}
		return nil
	}
	for i := range def.Files {
		if err := check(def.Files[i], false); err != nil {
			return def, err
		}
		key := def.Files[i].Source + "\x00" + strings.ToLower(strings.ReplaceAll(def.Files[i].Path, "\\", "/"))
		if seen[key] {
			return def, InvalidParam{"The file '" + def.Files[i].Path + "' is listed twice"}
		}
		seen[key] = true
	}
	if def.EnvFile != nil {
		if err := check(*def.EnvFile, true); err != nil {
			return def, err
		}
	}
	if def.Profiles == nil {
		def.Profiles = []string{}
	}
	if len(def.Profiles) > maxComposeProfiles {
		return def, InvalidParam{"A profile can enable at most 10 compose profiles"}
	}
	seenP := map[string]bool{}
	for _, p := range def.Profiles {
		if !composeProfileRe.MatchString(p) {
			return def, InvalidParam{"'" + p + "' is not a valid compose profile name"}
		}
		if seenP[p] {
			return def, InvalidParam{"The compose profile '" + p + "' is listed twice"}
		}
		seenP[p] = true
	}
	return def, nil
}

// Put creates or replaces a profile.
func (c *RegistryCompose) Put(ctx context.Context, def ComposeDef) (ComposeView, error) {
	def, err := c.validate(def)
	if err != nil {
		return ComposeView{}, err
	}
	c.mu.Lock()
	file := c.read()
	replaced := false
	for i := range file.Items {
		if file.Items[i].ID == def.ID {
			file.Items[i] = def
			replaced = true
		}
	}
	if !replaced {
		if len(file.Items) >= maxRegistryCompose {
			c.mu.Unlock()
			return ComposeView{}, InvalidParam{"There are already 50 compose profiles: delete one first"}
		}
		file.Items = append(file.Items, def)
	}
	err = coreWriteJSON(c.store, FileRegistryCompose, file)
	c.mu.Unlock()
	if err != nil {
		return ComposeView{}, err
	}
	c.record("registry.compose.save", def.ID)
	return c.View(ctx, def), nil
}

func (c *RegistryCompose) find(id string) (ComposeDef, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, d := range c.read().Items {
		if d.ID == id {
			return d, true
		}
	}
	return ComposeDef{}, false
}

// List returns the profiles, oldest first, each with its live state and the state of its files.
func (c *RegistryCompose) List(ctx context.Context) []ComposeView {
	c.mu.Lock()
	items := c.read().Items
	c.mu.Unlock()
	out := make([]ComposeView, len(items))
	snap := c.sources.snapshot()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = c.viewWith(ctx, items[i], snap)
		}(i)
	}
	wg.Wait()
	return out
}

func (c *RegistryCompose) Get(ctx context.Context, id string) (ComposeView, error) {
	d, ok := c.find(id)
	if !ok {
		return ComposeView{}, UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	return c.View(ctx, d), nil
}

// UsingSource lists the profiles that use a source: it cannot be deleted while there are any.
func (c *RegistryCompose) UsingSource(id string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []string{}
	for _, d := range c.read().Items {
		used := d.EnvFile != nil && d.EnvFile.Source == id
		for _, f := range d.Files {
			if f.Source == id {
				used = true
			}
		}
		if used {
			out = append(out, d.ID)
		}
	}
	return out
}

// Delete removes a profile. It refuses (RegistryConflict) while any of its containers is running; its containers
// are never removed by deleting the definition.
func (c *RegistryCompose) Delete(ctx context.Context, id string) error {
	d, ok := c.find(id)
	if !ok {
		return UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	snap := c.sources.snapshot()
	if st, _ := c.liveState(ctx, d, snap); st.Running > 0 {
		return RegistryConflict{"'" + id + "' is running: bring it down first"}
	}
	c.mu.Lock()
	file := c.read()
	kept := file.Items[:0]
	for _, it := range file.Items {
		if it.ID != id {
			kept = append(kept, it)
		}
	}
	file.Items = kept
	err := coreWriteJSON(c.store, FileRegistryCompose, file)
	if err == nil {
		st := c.readState()
		if _, had := st.Items[id]; had {
			delete(st.Items, id)
			err = coreWriteJSON(c.store, FileRegistryComposeState, st)
		}
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	c.record("registry.compose.delete", id)
	return nil
}

// ---- files ----------------------------------------------------------------------------------------------------------

func (c *RegistryCompose) srcDir(it registrySourceItem) string { return c.sources.sourceDir(it) }

func (c *RegistryCompose) resolve(snap map[string]registrySourceItem, ref ComposeFileRef, env bool) resolvedFile {
	return registryResolve(snap, c.srcDir, ref.Source, ref.Path, env)
}

func registrySha(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// fileStatus resolves one file and compares its fingerprint with the one recorded at the last successful up.
func (c *RegistryCompose) fileStatus(rf resolvedFile, last map[string]string) ComposeFileStatus {
	st := ComposeFileStatus{Path: rf.Path, Status: rf.Status}
	if rf.Message != "" {
		m := rf.Message
		st.Message = &m
	}
	if rf.Status != "ok" {
		return st
	}
	sum, err := registrySha(rf.Path)
	if err != nil {
		m := "The file cannot be read"
		st.Status, st.Message = "missing", &m
		return st
	}
	st.Sha256 = &sum
	if old, ok := last[rf.Path]; ok && old != sum {
		st.Status = "changed"
	}
	return st
}

// View builds the live picture of a profile: docker's state and the files read again from their sources.
func (c *RegistryCompose) View(ctx context.Context, d ComposeDef) ComposeView {
	return c.viewWith(ctx, d, c.sources.snapshot())
}

func (c *RegistryCompose) viewWith(ctx context.Context, d ComposeDef, snap map[string]registrySourceItem) ComposeView {
	c.mu.Lock()
	run := c.readState().Items[d.ID]
	c.mu.Unlock()
	v := ComposeView{ComposeDef: d, FileStatus: []ComposeFileStatus{}, LastUpSha: run.LastUpSha, LastUpAt: run.LastUpAt}
	if v.Files == nil {
		v.Files = []ComposeFileRef{}
	}
	if v.Profiles == nil {
		v.Profiles = []string{}
	}
	for _, f := range d.Files {
		v.FileStatus = append(v.FileStatus, c.fileStatus(c.resolve(snap, f, false), run.LastUpSha))
	}
	if d.EnvFile != nil {
		s := c.fileStatus(c.resolve(snap, *d.EnvFile, true), run.LastUpSha)
		v.EnvFileStatus = &s
	}
	st, err := c.liveState(ctx, d, snap)
	v.State = st
	if err != nil {
		m := err.Error()
		v.StateError = &m
	}
	return v
}

// ---- docker compose command lines -----------------------------------------------------------------------------------

// composeBase is the start of every command line: the project, its folder, the files, the env file and the profiles.
// It reports false when a file cannot be used, so the caller can fall back to the project name alone.
func (c *RegistryCompose) composeBase(d ComposeDef, snap map[string]registrySourceItem) ([]string, []resolvedFile, bool) {
	proj, err := composeProject(d.ID)
	if err != nil {
		return nil, nil, false
	}
	var files []resolvedFile
	ok := true
	for _, f := range d.Files {
		rf := c.resolve(snap, f, false)
		files = append(files, rf)
		if rf.Status != "ok" {
			ok = false
		}
	}
	args := []string{"compose", "--project-name", proj}
	if !ok || len(files) == 0 {
		return args, files, false
	}
	args = append(args, "--project-directory", filepath.Dir(files[0].Path))
	for _, rf := range files {
		args = append(args, "-f", rf.Path)
	}
	if d.EnvFile != nil {
		ev := c.resolve(snap, *d.EnvFile, true)
		files = append(files, ev)
		if ev.Status != "ok" {
			return []string{"compose", "--project-name", proj}, files, false
		}
		args = append(args, "--env-file", ev.Path)
	}
	for _, p := range d.Profiles {
		args = append(args, "--profile", p)
	}
	return args, files, true
}

// compose runs docker compose for a profile. The project name is checked once more here, as the last line of defence
// against ever touching a project that is not ours.
func (c *RegistryCompose) compose(ctx context.Context, args []string, timeout int) ExecResult {
	for i, a := range args {
		if a == "--project-name" && i+1 < len(args) && !strings.HasPrefix(args[i+1], registryProjectName) {
			return ExecResult{1, "refusing to touch a project that Nimbo did not create"}
		}
	}
	return c.run.Run(ctx, RegistryCommand{Name: "docker", Args: args, TimeoutSeconds: timeout})
}

// liveState asks docker which containers the profile has. When its files are not usable it asks by project name alone,
// so a profile whose file went away can still be seen and brought down.
func (c *RegistryCompose) liveState(ctx context.Context, d ComposeDef, snap map[string]registrySourceItem) (ComposeState, error) {
	empty := ComposeState{Services: []ComposeService{}}
	args, _, _ := c.composeBase(d, snap)
	if args == nil {
		return empty, InvalidParam{"Invalid profile name"}
	}
	res := c.compose(ctx, append(args, "ps", "-a", "--format", "json"), composeQuickTimeout)
	if !res.OK() {
		return empty, RegistryError{"Could not ask Docker: " + registryTail(res.Output, 300)}
	}
	return ParseComposePs(res.Output), nil
}

// ParseComposePs reads `docker compose ps --format json`: one object per line (older versions) or one array (newer).
// Missing fields are tolerated and lines that are not JSON are skipped.
func ParseComposePs(out string) ComposeState {
	st := ComposeState{Services: []ComposeService{}}
	out = strings.TrimSpace(out)
	if out == "" {
		return st
	}
	var rows []map[string]any
	if strings.HasPrefix(out, "[") {
		_ = json.Unmarshal([]byte(out), &rows)
	} else {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) == nil {
				rows = append(rows, row)
			}
		}
	}
	str := func(row map[string]any, key string) string {
		s, _ := row[key].(string)
		return strings.TrimSpace(s)
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		name := str(row, "Service")
		if name == "" {
			name = str(row, "Name")
		}
		state := strings.ToLower(str(row, "State"))
		if state == "" {
			state = "unknown"
		}
		svc := ComposeService{Name: name, State: state}
		if h := strings.ToLower(str(row, "Health")); h != "" {
			svc.Health = &h
		}
		st.Services = append(st.Services, svc)
		st.Total++
		if state == "running" {
			st.Running++
		}
	}
	return st
}

// ---- up, down, validate, logs, file ---------------------------------------------------------------------------------

func (c *RegistryCompose) claim(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.busy[id] {
		return false
	}
	c.busy[id] = true
	return true
}

func (c *RegistryCompose) release(id string) {
	c.mu.Lock()
	delete(c.busy, id)
	c.mu.Unlock()
}

// problems lists what stops a profile from starting: missing files (a conflict) or unusable ones (a bad request).
func composeProblems(files []resolvedFile) (missing []string, invalid error) {
	for _, rf := range files {
		switch rf.Status {
		case "missing":
			if rf.NotCloned {
				missing = append(missing, rf.Message)
			} else {
				missing = append(missing, rf.Path)
			}
		case "invalid":
			if invalid == nil {
				invalid = InvalidParam{rf.Message + " (" + rf.Path + ")"}
			}
		}
	}
	return
}

func composeMissingMessage(missing []string) string {
	return "These files are missing: " + strings.Join(missing, "; ")
}

// Up starts the profile with `docker compose up -d`. A profile with missing files answers a conflict that lists them.
func (c *RegistryCompose) Up(ctx context.Context, id string) (ComposeView, error) {
	d, ok := c.find(id)
	if !ok {
		return ComposeView{}, UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	if !c.claim(id) {
		return ComposeView{}, RegistryConflict{"Something is already running for this profile"}
	}
	defer c.release(id)
	snap := c.sources.snapshot()
	args, files, usable := c.composeBase(d, snap)
	if !usable {
		missing, invalid := composeProblems(files)
		if invalid != nil {
			return ComposeView{}, invalid
		}
		return ComposeView{}, RegistryConflict{composeMissingMessage(missing)}
	}
	// Fingerprints are taken before up, so what is recorded is what docker was given.
	sums := map[string]string{}
	for _, rf := range files {
		sum, err := registrySha(rf.Path)
		if err != nil {
			return ComposeView{}, RegistryConflict{composeMissingMessage([]string{rf.Path})}
		}
		sums[rf.Path] = sum
	}
	res := c.compose(ctx, append(args, "up", "-d", "--remove-orphans"), composeUpTimeout)
	if !res.OK() {
		c.record("registry.compose.up", id, "failed")
		return ComposeView{}, registryFailed("docker compose up failed", res, composeErrTail)
	}
	now := c.clock()
	c.mu.Lock()
	st := c.readState()
	st.Items[id] = composeRun{LastUpSha: sums, LastUpAt: &now}
	err := coreWriteJSON(c.store, FileRegistryComposeState, st)
	c.mu.Unlock()
	if err != nil {
		return ComposeView{}, err
	}
	c.record("registry.compose.up", id, strings.Join(d.Profiles, ","))
	return c.viewWith(ctx, d, snap), nil
}

// Down stops and removes the containers of the profile; volumes only when asked. If a file went away it still works,
// by project name.
func (c *RegistryCompose) Down(ctx context.Context, id string, volumes bool) (ComposeView, error) {
	d, ok := c.find(id)
	if !ok {
		return ComposeView{}, UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	if !c.claim(id) {
		return ComposeView{}, RegistryConflict{"Something is already running for this profile"}
	}
	defer c.release(id)
	snap := c.sources.snapshot()
	args, _, _ := c.composeBase(d, snap)
	args = append(args, "down", "--remove-orphans")
	if volumes {
		args = append(args, "--volumes")
	}
	res := c.compose(ctx, args, composeDownTimeout)
	if !res.OK() {
		c.record("registry.compose.down", id, "failed")
		return ComposeView{}, registryFailed("docker compose down failed", res, composeErrTail)
	}
	detail := "containers"
	if volumes {
		detail = "containers and volumes"
	}
	c.record("registry.compose.down", id, detail)
	return c.viewWith(ctx, d, snap), nil
}

// Validate runs `docker compose config -q`. A profile with missing files is reported as not valid, not as an error.
func (c *RegistryCompose) Validate(ctx context.Context, id string) (ComposeCheck, error) {
	d, ok := c.find(id)
	if !ok {
		return ComposeCheck{}, UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	snap := c.sources.snapshot()
	args, files, usable := c.composeBase(d, snap)
	if !usable {
		missing, invalid := composeProblems(files)
		if invalid != nil {
			return ComposeCheck{false, invalid.Error()}, nil
		}
		return ComposeCheck{false, composeMissingMessage(missing)}, nil
	}
	res := c.compose(ctx, append(args, "config", "-q"), composeCheckTimeout)
	if !res.OK() {
		msg := registryTail(res.Output, composeErrTail)
		if msg == "" {
			msg = "docker compose config failed"
		}
		return ComposeCheck{false, msg}, nil
	}
	return ComposeCheck{true, "The configuration is valid."}, nil
}

// Logs returns the last lines of the logs, of all services or one.
func (c *RegistryCompose) Logs(ctx context.Context, id string, tail int, service string) (string, error) {
	d, ok := c.find(id)
	if !ok {
		return "", UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	if tail <= 0 {
		tail = 200
	}
	if tail > 2000 {
		tail = 2000
	}
	if service != "" && !composeProfileRe.MatchString(service) {
		return "", InvalidParam{"Invalid service name"}
	}
	snap := c.sources.snapshot()
	args, _, _ := c.composeBase(d, snap)
	args = append(args, "logs", "--no-color", "--tail", strconv.Itoa(tail))
	if service != "" {
		args = append(args, service)
	}
	res := c.compose(ctx, args, composeQuickTimeout)
	if !res.OK() {
		return "", registryFailed("Could not read the logs", res, composeErrTail)
	}
	return res.Output, nil
}

// File reads one compose file live (never the env file: it may hold secrets).
func (c *RegistryCompose) File(id string, index int) (ComposeFileContent, error) {
	d, ok := c.find(id)
	if !ok {
		return ComposeFileContent{}, UnknownTarget{"Unknown compose profile '" + id + "'"}
	}
	if index < 0 || index >= len(d.Files) {
		return ComposeFileContent{}, InvalidParam{"There is no file with that number"}
	}
	rf := c.resolve(c.sources.snapshot(), d.Files[index], false)
	switch rf.Status {
	case "invalid":
		return ComposeFileContent{}, InvalidParam{rf.Message}
	case "missing":
		msg := rf.Message
		if msg == "" || msg == "The file does not exist" {
			msg = "The file is missing: " + rf.Path
		}
		return ComposeFileContent{}, RegistryConflict{msg}
	}
	text, truncated, err := registryReadCapped(rf.Path, composeViewMax)
	if err != nil {
		return ComposeFileContent{}, RegistryConflict{"The file cannot be read: " + rf.Path}
	}
	return ComposeFileContent{Path: rf.Path, Content: text, Truncated: truncated}, nil
}
