package nimbo

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NowMillis is the clock the services use: milliseconds since the epoch, like System.currentTimeMillis.
func NowMillis() int64 { return time.Now().UnixMilli() }

// DataDir is where Nimbo keeps what it learns about you: ~/.nimbo, or NIMBO_DATA_DIR (a volume in Docker).
func DataDir(env map[string]string, home string) string {
	if d := env["NIMBO_DATA_DIR"]; strings.TrimSpace(d) != "" {
		return d
	}
	return filepath.Join(home, ".nimbo")
}

// DataStore is a tiny named-file store. Names are plain file names; nothing here can reach outside its folder.
type DataStore interface {
	// Location is the folder on disk, or "" for the in-memory store used by tests.
	Location() string
	// Read returns the text of a file, and false when it does not exist.
	Read(name string) (string, bool, error)
	Write(name, text string) error
	// Append adds one line; when the file grows past maxBytes only its newest half is kept.
	Append(name, line string, maxBytes int64) error
	Delete(name string) error
	Sizes() map[string]int64
}

// DefaultJournalBytes is how big the activity journal may grow before its oldest half is dropped.
const DefaultJournalBytes int64 = 512 * 1024

var coreDataNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ErrInvalidDataName is returned for a file name that is not a plain, lowercase data file name.
var ErrInvalidDataName = errors.New("invalid data file name")

func coreCheckedName(name string) error {
	if !coreDataNameRe.MatchString(name) || strings.Contains(name, "..") {
		return ErrInvalidDataName
	}
	return nil
}

// FileDataStore keeps the files in a folder, creating it on the first write.
type FileDataStore struct {
	root string
	mu   sync.Mutex
}

func NewFileDataStore(root string) *FileDataStore { return &FileDataStore{root: root} }

func (s *FileDataStore) Location() string { return s.root }

func (s *FileDataStore) Read(name string) (string, bool, error) {
	if err := coreCheckedName(name); err != nil {
		return "", false, err
	}
	b, err := os.ReadFile(filepath.Join(s.root, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(b), true, nil
}

func (s *FileDataStore) Write(name, text string) error {
	if err := coreCheckedName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(name, text)
}

func (s *FileDataStore) writeLocked(name, text string) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	target := filepath.Join(s.root, name)
	tmp := filepath.Join(s.root, name+".tmp")
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	// Write beside and swap, so a crash leaves either the old file or the new one, never half of it.
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *FileDataStore) Append(name, line string, maxBytes int64) error {
	if err := coreCheckedName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.root, name)
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() && st.Size() > maxBytes {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := coreSplitLines(string(b))
		kept := lines[len(lines)/2:]
		if err := s.writeLocked(name, strings.Join(kept, "\n")+"\n"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strings.ReplaceAll(line, "\n", " ") + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func (s *FileDataStore) Delete(name string) error {
	if err := coreCheckedName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.root, name))
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *FileDataStore) Sizes() map[string]int64 {
	out := map[string]int64{}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !coreDataNameRe.MatchString(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil {
			out[e.Name()] = info.Size()
		}
	}
	return out
}

// coreSplitLines is Kotlin's readLines: lines without their terminators and without a trailing empty one.
func coreSplitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// MemoryDataStore is the store tests use; nothing touches the disk.
type MemoryDataStore struct {
	mu    sync.Mutex
	files map[string]string
}

func NewMemoryDataStore() *MemoryDataStore { return &MemoryDataStore{files: map[string]string{}} }

func (s *MemoryDataStore) Location() string { return "" }

func (s *MemoryDataStore) Read(name string) (string, bool, error) {
	if err := coreCheckedName(name); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.files[name]
	return t, ok, nil
}

func (s *MemoryDataStore) Write(name, text string) error {
	if err := coreCheckedName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = text
	return nil
}

func (s *MemoryDataStore) Append(name, line string, maxBytes int64) error {
	if err := coreCheckedName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []string
	for _, l := range coreSplitLines(s.files[name]) {
		if l != "" {
			lines = append(lines, l)
		}
	}
	lines = append(lines, strings.ReplaceAll(line, "\n", " "))
	var size int64
	for _, l := range lines {
		size += int64(len(l)) + 1
	}
	if size > maxBytes {
		lines = lines[len(lines)/2:]
	}
	s.files[name] = strings.Join(lines, "\n") + "\n"
	return nil
}

func (s *MemoryDataStore) Delete(name string) error {
	if err := coreCheckedName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, name)
	return nil
}

func (s *MemoryDataStore) Sizes() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	for k, v := range s.files {
		out[k] = int64(len(v))
	}
	return out
}

// ---- JSON helpers -------------------------------------------------------------------------------------------------

// corePrettyJSON is how documents are written: indented with four spaces, like the Kotlin version, so files written by
// either version read in the other.
func corePrettyJSON(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// coreLineJSON is one journal entry per line, so no pretty printing.
func coreLineJSON(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func coreWriteJSON(s DataStore, name string, v any) error {
	text, err := corePrettyJSON(v)
	if err != nil {
		return err
	}
	return s.Write(name, text)
}

// coreReadOrDefault reads a JSON document; a damaged one is set aside as `<name>.bad` and replaced by the default
// instead of failing. def builds the default, which also supplies the values for keys the file leaves out.
func coreReadOrDefault[T any](s DataStore, name string, def func() T) T {
	text, ok, _ := s.Read(name)
	if !ok {
		return def()
	}
	v := def()
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		_ = s.Write(name+".bad", text)
		return def()
	}
	return v
}

// DataFiles are the names of the files in the data folder.
const (
	FileSettings = "settings.json"
	FileProgress = "progress.json"
	FileStats    = "stats.json"
	FileJournal  = "journal.jsonl"
	FileFidelity = "fidelity-history.json"
	FileSecrets  = "secrets.json"
)

// BackupFiles are the files a backup carries. Secrets are deliberately not among them.
// The registry (own images, repositories, compose profiles) is part of a backup; the record of the last "up" is not.
var BackupFiles = []string{FileSettings, FileProgress, FileStats, FileFidelity, FileCustomStacks, FileRegistryImages, FileRegistrySources, FileRegistryCompose}

// ---- Settings -----------------------------------------------------------------------------------------------------

type Settings struct {
	Lang  *string `json:"lang"`
	Theme string  `json:"theme"`
	// UpdateMode "auto" checks GitHub for a newer version once a day; "manual" never contacts anyone by itself.
	UpdateMode string `json:"updateMode"`
	// Persist lists the providers whose data is kept in a Docker volume between runs.
	Persist         []string `json:"persist"`
	LastUpdateCheck *int64   `json:"lastUpdateCheck"`
}

func defaultSettings() Settings {
	return Settings{Theme: "auto", UpdateMode: "auto", Persist: []string{}}
}

type SettingsPatch struct {
	Lang       *string `json:"lang"`
	Theme      *string `json:"theme"`
	UpdateMode *string `json:"updateMode"`
}

type SettingsService struct {
	store DataStore
	mu    sync.Mutex
}

func NewSettingsService(store DataStore) *SettingsService { return &SettingsService{store: store} }

func (s *SettingsService) get() Settings {
	v := coreReadOrDefault(s.store, FileSettings, defaultSettings)
	if v.Persist == nil {
		v.Persist = []string{}
	}
	return v
}

func (s *SettingsService) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get()
}

func coreOneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func (s *SettingsService) Patch(p SettingsPatch) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Lang != nil && !coreOneOf(*p.Lang, "es", "en") {
		return Settings{}, InvalidParam{"lang must be es or en"}
	}
	if p.Theme != nil && !coreOneOf(*p.Theme, "auto", "light", "dark") {
		return Settings{}, InvalidParam{"theme must be auto, light or dark"}
	}
	if p.UpdateMode != nil && !coreOneOf(*p.UpdateMode, "auto", "manual") {
		return Settings{}, InvalidParam{"updateMode must be auto or manual"}
	}
	next := s.get()
	if p.Lang != nil {
		next.Lang = p.Lang
	}
	if p.Theme != nil {
		next.Theme = *p.Theme
	}
	if p.UpdateMode != nil {
		next.UpdateMode = *p.UpdateMode
	}
	return next, coreWriteJSON(s.store, FileSettings, next)
}

func (s *SettingsService) SetPersist(providerID string, enabled bool) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	def := FindProvider(providerID)
	if def == nil {
		return Settings{}, UnknownTarget{"Unknown provider '" + providerID + "'"}
	}
	if def.Persist == nil {
		return Settings{}, InvalidParam{def.Name + " keeps no data that could be preserved"}
	}
	cur := s.get()
	var next []string
	for _, id := range cur.Persist {
		if id != providerID {
			next = append(next, id)
		}
	}
	if enabled {
		next = append(next, providerID)
	}
	if next == nil {
		next = []string{}
	}
	cur.Persist = next
	return cur, coreWriteJSON(s.store, FileSettings, cur)
}

func (s *SettingsService) MarkUpdateCheck(at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.get()
	cur.LastUpdateCheck = &at
	return coreWriteJSON(s.store, FileSettings, cur)
}

func (s *SettingsService) Persists(providerID string) bool {
	for _, id := range s.Get().Persist {
		if id == providerID {
			return true
		}
	}
	return false
}

// ---- Lab progress -------------------------------------------------------------------------------------------------

type LabRecord struct {
	CompletedAt    *int64 `json:"completedAt"`
	Attempts       int    `json:"attempts"`
	FirstAttemptAt *int64 `json:"firstAttemptAt"`
	LastAttemptAt  *int64 `json:"lastAttemptAt"`
}

type Progress struct {
	Labs map[string]LabRecord `json:"labs"`
}

type ProgressService struct {
	store DataStore
	clock func() int64
	mu    sync.Mutex
}

func NewProgressService(store DataStore, clock func() int64) *ProgressService {
	return &ProgressService{store: store, clock: clock}
}

func (p *ProgressService) get() Progress {
	v := coreReadOrDefault(p.store, FileProgress, func() Progress { return Progress{} })
	if v.Labs == nil {
		v.Labs = map[string]LabRecord{}
	}
	return v
}

func (p *ProgressService) Get() Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.get()
}

// Attempt records a verification. Completion is sticky: failing later does not take the badge away.
func (p *ProgressService) Attempt(labID string, passed bool) (LabRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	cur := p.get()
	next := cur.Labs[labID]
	next.Attempts++
	if next.FirstAttemptAt == nil {
		next.FirstAttemptAt = &now
	}
	next.LastAttemptAt = &now
	if next.CompletedAt == nil && passed {
		next.CompletedAt = &now
	}
	cur.Labs[labID] = next
	return next, coreWriteJSON(p.store, FileProgress, cur)
}

var coreLabIDRe = regexp.MustCompile(`^[a-z0-9-]{1,41}$`)

// ImportDone brings over what an earlier version kept in the browser. Existing records win.
func (p *ProgressService) ImportDone(ids []string) (Progress, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	cur := p.get()
	added := false
	for _, id := range ids {
		if _, exists := cur.Labs[id]; exists || !coreLabIDRe.MatchString(id) {
			continue
		}
		at := now
		cur.Labs[id] = LabRecord{CompletedAt: &at}
		added = true
	}
	if added {
		return cur, coreWriteJSON(p.store, FileProgress, cur)
	}
	return cur, nil
}

// Completed is the set of lab ids that have a completion date.
func (p *ProgressService) Completed() map[string]bool {
	out := map[string]bool{}
	for id, r := range p.Get().Labs {
		if r.CompletedAt != nil {
			out[id] = true
		}
	}
	return out
}

// ---- Usage statistics and the activity journal --------------------------------------------------------------------

type ProviderUsage struct {
	Starts        int    `json:"starts"`
	Stops         int    `json:"stops"`
	UpSeconds     int64  `json:"upSeconds"`
	LastStartedAt *int64 `json:"lastStartedAt"`
}

type StatsData struct {
	Since     int64                    `json:"since"`
	Providers map[string]ProviderUsage `json:"providers"`
	Counters  map[string]int           `json:"counters"`
}

type JournalEntry struct {
	At      int64   `json:"at"`
	Kind    string  `json:"kind"`
	Subject string  `json:"subject"`
	Detail  *string `json:"detail"`
}

// StatsService: everything stays on this machine. Nothing here is ever sent anywhere.
type StatsService struct {
	store DataStore
	clock func() int64
	mu    sync.Mutex
}

func NewStatsService(store DataStore, clock func() int64) *StatsService {
	return &StatsService{store: store, clock: clock}
}

func (s *StatsService) stats() StatsData {
	v := coreReadOrDefault(s.store, FileStats, func() StatsData { return StatsData{Since: s.clock()} })
	if v.Since == 0 {
		v.Since = s.clock()
	}
	if v.Providers == nil {
		v.Providers = map[string]ProviderUsage{}
	}
	if v.Counters == nil {
		v.Counters = map[string]int{}
	}
	return v
}

func (s *StatsService) Stats() StatsData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats()
}

// Record adds an entry to the journal and bumps the counters. It is best effort: a full disk must not break the
// action that was being recorded. The optional detail is shortened to 200 characters.
func (s *StatsService) Record(kind, subject string, detail ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	entry := JournalEntry{At: now, Kind: kind, Subject: subject}
	if len(detail) > 0 {
		d := detail[0]
		if r := []rune(d); len(r) > 200 {
			d = string(r[:200])
		}
		entry.Detail = &d
	}
	if line, err := coreLineJSON(entry); err == nil {
		_ = s.store.Append(FileJournal, line, DefaultJournalBytes)
	}
	st := s.stats()
	switch kind {
	case "provider.start":
		u := st.Providers[subject]
		u.Starts++
		u.LastStartedAt = &now
		st.Providers[subject] = u
	case "provider.stop":
		u := st.Providers[subject]
		u.Stops++
		st.Providers[subject] = u
	}
	st.Counters[kind]++
	_ = coreWriteJSON(s.store, FileStats, st)
}

// Sample is called on a timer with the providers that are up: time is measured while Nimbo is open, not guessed.
func (s *StatsService) Sample(running []string, seconds int64) {
	if len(running) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats()
	for _, id := range running {
		u := st.Providers[id]
		u.UpSeconds += seconds
		st.Providers[id] = u
	}
	_ = coreWriteJSON(s.store, FileStats, st)
}

// Journal is the newest entries first; limit is kept between 1 and 500. A damaged line is skipped.
func (s *StatsService) Journal(limit int) []JournalEntry {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	text, _, _ := s.store.Read(FileJournal)
	var all []JournalEntry
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e JournalEntry
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			all = append(all, e)
		}
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	out := make([]JournalEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i])
	}
	return out
}

func (s *StatsService) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.store.Delete(FileStats)
	_ = s.store.Delete(FileJournal)
}

// ---- History of the fidelity matrix -------------------------------------------------------------------------------

type FidelityRunSummary struct {
	At         int64             `json:"at"`
	DurationMs int64             `json:"durationMs"`
	Suites     map[string]string `json:"suites"`
	Steps      int               `json:"steps"`
	Passed     int               `json:"passed"`
}

type FidelityChange struct {
	Suite      string `json:"suite"`
	From       string `json:"from"`
	To         string `json:"to"`
	Regression bool   `json:"regression"`
}

// FidelitySuiteSummary and FidelityReportSummary are the little the history needs from a fidelity report, so this
// file does not depend on the area that produces it. The integrator converts a report into one.
type FidelitySuiteSummary struct {
	ID     string
	Status string
	// Steps is how many steps the suite ran; Passed how many of them passed.
	Steps  int
	Passed int
}

type FidelityReportSummary struct {
	DurationMs int64
	Suites     []FidelitySuiteSummary
}

type FidelityHistory struct {
	store DataStore
	clock func() int64
	mu    sync.Mutex
}

func NewFidelityHistory(store DataStore, clock func() int64) *FidelityHistory {
	return &FidelityHistory{store: store, clock: clock}
}

func (h *FidelityHistory) list() []FidelityRunSummary {
	v := coreReadOrDefault(h.store, FileFidelity, func() []FidelityRunSummary { return []FidelityRunSummary{} })
	if v == nil {
		v = []FidelityRunSummary{}
	}
	return v
}

func (h *FidelityHistory) List() []FidelityRunSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.list()
}

func coreFidelityRank(status string) int {
	switch status {
	case "full":
		return 3
	case "partial":
		return 2
	case "missing":
		return 1
	}
	return 0
}

// ChangesFor is what changed against the last time each suite ran. Offline suites say nothing about quality, so they
// never count as regressions.
func (h *FidelityHistory) ChangesFor(report FidelityReportSummary) []FidelityChange {
	history := h.List()
	changes := []FidelityChange{}
	for _, s := range report.Suites {
		before, found := "", false
		for i := len(history) - 1; i >= 0; i-- {
			if st, ok := history[i].Suites[s.ID]; ok {
				before, found = st, true
				break
			}
		}
		if !found || before == s.Status {
			continue
		}
		changes = append(changes, FidelityChange{
			Suite: s.ID, From: before, To: s.Status,
			Regression: before != "offline" && s.Status != "offline" && coreFidelityRank(s.Status) < coreFidelityRank(before),
		})
	}
	return changes
}

func (h *FidelityHistory) Record(report FidelityReportSummary) (FidelityRunSummary, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := FidelityRunSummary{At: h.clock(), DurationMs: report.DurationMs, Suites: map[string]string{}}
	for _, s := range report.Suites {
		entry.Suites[s.ID] = s.Status
		entry.Steps += s.Steps
		entry.Passed += s.Passed
	}
	all := append(h.list(), entry)
	if len(all) > 100 {
		all = all[len(all)-100:]
	}
	return entry, coreWriteJSON(h.store, FileFidelity, all)
}

func (h *FidelityHistory) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.store.Delete(FileFidelity)
}

// ---- Everything the app remembers ---------------------------------------------------------------------------------

// AppData is everything the app remembers, behind one object so tests can swap the store.
type AppData struct {
	Store    DataStore
	Clock    func() int64
	Settings *SettingsService
	Progress *ProgressService
	Stats    *StatsService
	Fidelity *FidelityHistory
}

// NewAppData builds the services over a store. A nil store is an in-memory one; a nil clock is the real time.
func NewAppData(store DataStore, clock func() int64) *AppData {
	if store == nil {
		store = NewMemoryDataStore()
	}
	if clock == nil {
		clock = NowMillis
	}
	return &AppData{
		Store: store, Clock: clock,
		Settings: NewSettingsService(store),
		Progress: NewProgressService(store, clock),
		Stats:    NewStatsService(store, clock),
		Fidelity: NewFidelityHistory(store, clock),
	}
}

// AppDataOnDisk keeps everything in a folder.
func AppDataOnDisk(root string) *AppData { return NewAppData(NewFileDataStore(root), nil) }

// DataBundle is a backup of what is worth keeping. Secrets are never part of it.
type DataBundle struct {
	Format     int               `json:"format"`
	ExportedAt int64             `json:"exportedAt"`
	Files      map[string]string `json:"files"`
}

func (a *AppData) Export() DataBundle {
	files := map[string]string{}
	for _, n := range BackupFiles {
		if t, ok, _ := a.Store.Read(n); ok {
			files[n] = t
		}
	}
	return DataBundle{Format: 1, ExportedAt: a.Clock(), Files: files}
}

// validateBackupFile checks that a file parses as the kind of document it claims to be.
func validateBackupFile(name, text string) error {
	trimmed := strings.TrimSpace(text)
	var target any
	opener := byte('{')
	switch name {
	case FileSettings:
		target = &Settings{}
	case FileProgress:
		target = &Progress{}
	case FileStats:
		target = &StatsData{}
	case FileFidelity:
		target = &[]FidelityRunSummary{}
		opener = '['
	case FileCustomStacks:
		target = &customStackFile{}
	case FileRegistryImages:
		target = &imageFile{}
	case FileRegistrySources:
		target = &registrySourceFile{}
	case FileRegistryCompose:
		target = &registryComposeFile{}
	default:
		return nil
	}
	if trimmed == "" || trimmed[0] != opener {
		return errors.New("not the expected document")
	}
	return json.Unmarshal([]byte(trimmed), target)
}

// Restore restores a backup. Each file must parse as what it claims to be before anything is written.
func (a *AppData) Restore(bundle DataBundle) ([]string, error) {
	if bundle.Format != 1 {
		return nil, InvalidParam{"unsupported backup format " + strconv.Itoa(bundle.Format)}
	}
	allowed := map[string]bool{}
	for _, n := range BackupFiles {
		allowed[n] = true
	}
	var unknown []string
	for n := range bundle.Files {
		if !allowed[n] {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, InvalidParam{"unexpected files in the backup: " + strings.Join(unknown, ", ")}
	}
	for name, text := range bundle.Files {
		if err := validateBackupFile(name, text); err != nil {
			return nil, InvalidParam{name + " in the backup is damaged"}
		}
	}
	names := make([]string, 0, len(bundle.Files))
	for name, text := range bundle.Files {
		if err := a.Store.Write(name, text); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
