package nimbo

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func coreReport(at int64, pairs ...string) FidelityReportSummary {
	r := FidelityReportSummary{DurationMs: 50}
	for i := 0; i+1 < len(pairs); i += 2 {
		passed := 0
		if pairs[i+1] == "full" {
			passed = 1
		}
		r.Suites = append(r.Suites, FidelitySuiteSummary{ID: pairs[i], Status: pairs[i+1], Steps: 1, Passed: passed})
	}
	return r
}

func TestDataDir(t *testing.T) {
	coreEq(t, filepath.Join("C:/Users/x", ".nimbo"), DataDir(map[string]string{}, "C:/Users/x"))
	coreEq(t, "/data", DataDir(map[string]string{"NIMBO_DATA_DIR": "/data"}, "/home/x"))
	coreEq(t, filepath.Join("/home/x", ".nimbo"), DataDir(map[string]string{"NIMBO_DATA_DIR": "  "}, "/home/x"))
}

func TestFileStoreRoundTripLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	s := NewFileDataStore(dir)
	_, ok, _ := s.Read("a.json")
	coreTrue(t, !ok)
	coreTrue(t, s.Write("a.json", "one") == nil && s.Write("a.json", "two") == nil)
	v, _, _ := s.Read("a.json")
	coreEq(t, "two", v)
	entries, _ := os.ReadDir(dir)
	coreEq(t, 1, len(entries))
	coreTrue(t, s.Delete("a.json") == nil)
	_, ok, _ = s.Read("a.json")
	coreTrue(t, !ok)
	coreTrue(t, s.Delete("a.json") == nil, "deleting a missing file is fine")
}

func TestStoreNamesCannotLeaveTheFolder(t *testing.T) {
	s := NewFileDataStore(t.TempDir())
	for _, n := range []string{"../x", "a/b", "..", "A.json", "", ".hidden", `a\b`} {
		coreTrue(t, s.Write(n, "x") != nil, n)
		_, _, err := s.Read(n)
		coreTrue(t, err != nil, n)
	}
	_, _, err := NewMemoryDataStore().Read("../x")
	coreTrue(t, err != nil)
}

func TestAppendKeepsNewestHalf(t *testing.T) {
	for name, s := range map[string]DataStore{"file": NewFileDataStore(t.TempDir()), "memory": NewMemoryDataStore()} {
		for i := 0; i < 100; i++ {
			_ = s.Append("j.jsonl", fmt.Sprintf("line-%d", i), 200)
		}
		text, _, _ := s.Read("j.jsonl")
		var lines []string
		for _, l := range strings.Split(text, "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		coreTrue(t, len(lines) < 100, name+": should have been trimmed")
		coreEq(t, "line-99", lines[len(lines)-1], name)
		prev := -1
		for _, l := range lines {
			n, _ := strconv.Atoi(strings.TrimPrefix(l, "line-"))
			coreTrue(t, n > prev, name+" order")
			prev = n
		}
	}
}

func TestAppendFlattensNewlines(t *testing.T) {
	s := NewMemoryDataStore()
	_ = s.Append("j.jsonl", "a\nb", DefaultJournalBytes)
	v, _, _ := s.Read("j.jsonl")
	coreEq(t, "a b\n", v)
}

func TestSizesListOnlyDataFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewFileDataStore(dir)
	_ = s.Write("a.json", "12345")
	_ = os.WriteFile(filepath.Join(dir, "Not Valid.txt"), []byte("x"), 0o644)
	coreTrue(t, reflect.DeepEqual(map[string]int64{"a.json": 5}, s.Sizes()))
}

func TestDamagedFileIsSetAside(t *testing.T) {
	s := NewMemoryDataStore()
	_ = s.Write(FileSettings, "{ not json")
	got := NewSettingsService(s).Get()
	coreTrue(t, reflect.DeepEqual(defaultSettings(), got))
	bad, _, _ := s.Read(FileSettings + ".bad")
	coreEq(t, "{ not json", bad)
}

func TestSettingsDefaultsAndPatches(t *testing.T) {
	s := NewSettingsService(NewMemoryDataStore())
	coreTrue(t, reflect.DeepEqual(defaultSettings(), s.Get()))
	en, dark := "en", "dark"
	next, err := s.Patch(SettingsPatch{Lang: &en, Theme: &dark})
	coreTrue(t, err == nil)
	coreEq(t, "en", *next.Lang)
	coreEq(t, "dark", next.Theme)
	coreEq(t, "auto", next.UpdateMode)
	manual := "manual"
	after, _ := s.Patch(SettingsPatch{UpdateMode: &manual})
	coreEq(t, "en", *after.Lang)
}

func TestInvalidSettingsWriteNothing(t *testing.T) {
	store := NewMemoryDataStore()
	s := NewSettingsService(store)
	fr, neon, sometimes := "fr", "neon", "sometimes"
	for _, p := range []SettingsPatch{{Lang: &fr}, {Theme: &neon}, {UpdateMode: &sometimes}} {
		_, err := s.Patch(p)
		_, bad := err.(InvalidParam)
		coreTrue(t, bad)
	}
	_, ok, _ := store.Read(FileSettings)
	coreTrue(t, !ok)
}

func TestPersistIsPerProviderAndOnlyForProvidersThatKeepData(t *testing.T) {
	s := NewSettingsService(NewMemoryDataStore())
	r, _ := s.SetPersist("postgres", true)
	coreTrue(t, reflect.DeepEqual([]string{"postgres"}, r.Persist))
	coreTrue(t, s.Persists("postgres") && !s.Persists("redis"))
	r, _ = s.SetPersist("postgres", false)
	coreEq(t, 0, len(r.Persist))
	coreTrue(t, r.Persist != nil, "serialises as [] not null")
	_, err := s.SetPersist("nope", true)
	_, unknown := err.(UnknownTarget)
	coreTrue(t, unknown)
	for _, d := range CatalogProviders {
		if d.Persist == nil {
			_, err := s.SetPersist(d.ID, true)
			_, invalid := err.(InvalidParam)
			coreTrue(t, invalid)
			break
		}
	}
}

func TestSettingsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	es := "es"
	_, _ = NewSettingsService(NewFileDataStore(dir)).Patch(SettingsPatch{Lang: &es})
	coreEq(t, "es", *NewSettingsService(NewFileDataStore(dir)).Get().Lang)
}

func TestSettingsFileFromKotlinStillReads(t *testing.T) {
	s := NewMemoryDataStore()
	_ = s.Write(FileSettings, "{\n    \"lang\": \"en\",\n    \"theme\": \"auto\",\n    \"updateMode\": \"auto\",\n    \"persist\": [\"redis\"],\n    \"lastUpdateCheck\": 12\n}")
	got := NewSettingsService(s).Get()
	coreTrue(t, *got.Lang == "en" && got.Persist[0] == "redis" && *got.LastUpdateCheck == 12)
}

func TestProgressAttemptsAndStickyCompletion(t *testing.T) {
	now := int64(100)
	p := NewProgressService(NewMemoryDataStore(), func() int64 { return now })
	first, _ := p.Attempt("lab-1", false)
	coreEq(t, 1, first.Attempts)
	coreTrue(t, first.CompletedAt == nil)
	now = 200
	done, _ := p.Attempt("lab-1", true)
	coreEq(t, int64(200), *done.CompletedAt)
	coreEq(t, int64(100), *done.FirstAttemptAt)
	now = 300
	later, _ := p.Attempt("lab-1", false)
	coreEq(t, int64(200), *later.CompletedAt, "failing later must not take the badge away")
	coreEq(t, 3, later.Attempts)
	coreTrue(t, reflect.DeepEqual(map[string]bool{"lab-1": true}, p.Completed()))
}

func TestProgressImportKeepsExistingAndRejectsOddIDs(t *testing.T) {
	p := NewProgressService(NewMemoryDataStore(), func() int64 { return 5 })
	_, _ = p.Attempt("lab-1", false)
	res, _ := p.ImportDone([]string{"lab-1", "lab-2", "../etc", "UPPER", "", "lab-2"})
	coreEq(t, 2, len(res.Labs))
	coreTrue(t, res.Labs["lab-1"].CompletedAt == nil, "an existing record wins over the import")
	coreTrue(t, res.Labs["lab-2"].CompletedAt != nil)
	coreTrue(t, reflect.DeepEqual(map[string]bool{"lab-2": true}, p.Completed()))
}

func TestStatsCountStartsAndStops(t *testing.T) {
	now := int64(1000)
	s := NewStatsService(NewMemoryDataStore(), func() int64 { return now })
	s.Record("provider.start", "redis")
	s.Record("provider.start", "redis")
	s.Record("provider.stop", "redis")
	s.Record("lab.verify", "x")
	st := s.Stats()
	coreEq(t, 2, st.Providers["redis"].Starts)
	coreEq(t, 1, st.Providers["redis"].Stops)
	coreEq(t, 2, st.Counters["provider.start"])
	coreEq(t, 1, st.Counters["lab.verify"])
	coreEq(t, int64(1000), st.Since)
}

func TestStatsUptimeOnlyForRunning(t *testing.T) {
	s := NewStatsService(NewMemoryDataStore(), func() int64 { return 1 })
	s.Sample(nil, 15)
	coreEq(t, 0, len(s.Stats().Providers))
	s.Sample([]string{"redis", "minio"}, 15)
	s.Sample([]string{"redis"}, 15)
	coreEq(t, int64(30), s.Stats().Providers["redis"].UpSeconds)
	coreEq(t, int64(15), s.Stats().Providers["minio"].UpSeconds)
}

func TestJournalNewestFirstLimitedAndTolerant(t *testing.T) {
	store := NewMemoryDataStore()
	now := int64(1000)
	s := NewStatsService(store, func() int64 { return now })
	for i := 0; i < 5; i++ {
		now++
		s.Record("provider.start", fmt.Sprintf("p%d", i))
	}
	_ = store.Append(FileJournal, "{broken", DefaultJournalBytes)
	latest := s.Journal(3)
	var subjects []string
	for _, e := range latest {
		subjects = append(subjects, e.Subject)
	}
	coreEq(t, "p4,p3,p2", strings.Join(subjects, ","))
	coreEq(t, 5, len(s.Journal(100)))
}

func TestJournalDetailsAreShortenedAndNullWhenAbsent(t *testing.T) {
	store := NewMemoryDataStore()
	s := NewStatsService(store, func() int64 { return 1 })
	s.Record("x", "y", strings.Repeat("d", 1000))
	coreEq(t, 200, len(*s.Journal(50)[0].Detail))
	s.Record("x", "z")
	line, _, _ := store.Read(FileJournal)
	coreContains(t, line, `"detail":null`)
}

func TestStatsReset(t *testing.T) {
	s := NewStatsService(NewMemoryDataStore(), func() int64 { return 1 })
	s.Record("provider.start", "redis")
	s.Reset()
	coreEq(t, 0, len(s.Stats().Providers))
	coreEq(t, 0, len(s.Journal(50)))
}

func TestFidelityFirstRunHasNothingToCompare(t *testing.T) {
	h := NewFidelityHistory(NewMemoryDataStore(), func() int64 { return 5 })
	coreEq(t, 0, len(h.ChangesFor(coreReport(0, "s3", "full"))))
}

func TestFidelityRegressionRules(t *testing.T) {
	h := NewFidelityHistory(NewMemoryDataStore(), func() int64 { return 5 })
	_, _ = h.Record(coreReport(0, "s3", "full", "sqs", "partial"))
	changes := map[string]FidelityChange{}
	for _, c := range h.ChangesFor(coreReport(0, "s3", "partial", "sqs", "full")) {
		changes[c.Suite] = c
	}
	coreTrue(t, changes["s3"].Regression)
	coreTrue(t, !changes["sqs"].Regression)
	coreEq(t, 0, len(h.ChangesFor(coreReport(0, "s3", "full", "sqs", "partial"))))
}

func TestOfflineIsNeverARegression(t *testing.T) {
	h := NewFidelityHistory(NewMemoryDataStore(), func() int64 { return 5 })
	_, _ = h.Record(coreReport(0, "s3", "full"))
	c := h.ChangesFor(coreReport(0, "s3", "offline"))
	coreTrue(t, len(c) == 1 && !c[0].Regression)
}

func TestFidelityHistoryIsCapped(t *testing.T) {
	h := NewFidelityHistory(NewMemoryDataStore(), func() int64 { return 5 })
	for i := 0; i < 120; i++ {
		_, _ = h.Record(coreReport(0, "s3", "full"))
	}
	coreEq(t, 100, len(h.List()))
	last := h.List()[99]
	coreEq(t, 1, last.Steps)
	coreEq(t, 1, last.Passed)
	h.Clear()
	coreEq(t, 0, len(h.List()))
}

func TestBackupRoundTripWithoutSecrets(t *testing.T) {
	a := NewAppData(NewMemoryDataStore(), func() int64 { return 7 })
	en := "en"
	_, _ = a.Settings.Patch(SettingsPatch{Lang: &en})
	_, _ = a.Progress.Attempt("lab-1", true)
	a.Stats.Record("provider.start", "redis")
	_, _ = a.Fidelity.Record(coreReport(0, "s3", "full"))
	_ = a.Store.Write(FileSecrets, `{"values":{"NIMBO_MINIO_PASSWORD":"cipher"}}`)

	bundle := a.Export()
	_, hasSecrets := bundle.Files[FileSecrets]
	coreTrue(t, !hasSecrets, "secrets must never be exported")

	b := NewAppData(NewMemoryDataStore(), func() int64 { return 7 })
	names, err := b.Restore(bundle)
	coreTrue(t, err == nil)
	coreEq(t, 4, len(names))
	coreEq(t, "en", *b.Settings.Get().Lang)
	coreTrue(t, b.Progress.Completed()["lab-1"])
	coreEq(t, 1, b.Stats.Stats().Providers["redis"].Starts)
	coreEq(t, 1, len(b.Fidelity.List()))
	_, ok, _ := b.Store.Read(FileSecrets)
	coreTrue(t, !ok)
}

func TestBadBackupWritesNothing(t *testing.T) {
	d := NewAppData(nil, nil)
	es := "es"
	_, _ = d.Settings.Patch(SettingsPatch{Lang: &es})
	_, err := d.Restore(DataBundle{Format: 1, ExportedAt: 1, Files: map[string]string{FileSettings: `{"lang":"en"}`, FileProgress: "[[["}})
	_, invalid := err.(InvalidParam)
	coreTrue(t, invalid)
	coreEq(t, "es", *d.Settings.Get().Lang)
	for _, b := range []DataBundle{
		{Format: 2, ExportedAt: 1, Files: map[string]string{}},
		{Format: 1, ExportedAt: 1, Files: map[string]string{FileSecrets: "{}"}},
		{Format: 1, ExportedAt: 1, Files: map[string]string{"../evil.json": "{}"}},
		{Format: 1, ExportedAt: 1, Files: map[string]string{FileFidelity: "{}"}},
	} {
		_, err := d.Restore(b)
		_, invalid := err.(InvalidParam)
		coreTrue(t, invalid)
	}
}
