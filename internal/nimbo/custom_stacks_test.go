package nimbo

import (
	"strings"
	"testing"
)

func TestCustomStackLifecycleOverHTTP(t *testing.T) {
	h, rt, _, _ := varsServer(t)
	body := `{"description":"My backend","providers":["redis","floci-aws"]}`

	r := coreDo(h, "PUT", "/api/stacks/my-backend", body)
	coreEq(t, 200, r.Code, r.Body)
	coreContains(t, r.Body, `"custom":true`)

	var list StackListDto
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/stacks", "").Body, &list) == nil)
	var mine *StackDto
	builtIn := 0
	for i := range list.Stacks {
		if list.Stacks[i].Custom {
			mine = &list.Stacks[i]
		} else {
			builtIn++
		}
	}
	coreTrue(t, mine != nil && mine.ID == "my-backend" && mine.Total == 2, "your stack is listed after the built-in ones")
	coreTrue(t, builtIn >= 3)

	// It behaves like any other stack: it starts, and it exports.
	coreEq(t, 200, coreDo(h, "POST", "/api/stacks/my-backend/up", "").Code)
	coreEq(t, 2, len(rt.started))
	r = coreDo(h, "GET", "/api/stacks/my-backend/export?format=compose", "")
	coreEq(t, 200, r.Code)
	coreContains(t, r.Body, "redis")

	// Saving again with the same name replaces it.
	coreEq(t, 200, coreDo(h, "PUT", "/api/stacks/my-backend", `{"description":"Smaller","providers":["redis"]}`).Code)
	coreTrue(t, jsonUnmarshal(coreDo(h, "GET", "/api/stacks", "").Body, &list) == nil)
	count := 0
	for _, s := range list.Stacks {
		if s.ID == "my-backend" {
			count++
			coreEq(t, 1, s.Total)
		}
	}
	coreEq(t, 1, count)

	coreEq(t, 204, coreDo(h, "DELETE", "/api/stacks/my-backend", "").Code)
	coreEq(t, 404, coreDo(h, "DELETE", "/api/stacks/my-backend", "").Code)
	coreEq(t, 404, coreDo(h, "POST", "/api/stacks/my-backend/up", "").Code)
}

func TestCustomStacksAreValidated(t *testing.T) {
	h, _, _, _ := varsServer(t)
	for name, c := range map[string][2]string{
		"bad name":      {"My-Stack", `{"providers":["redis"]}`},
		"empty":         {"empty", `{"providers":[]}`},
		"unknown":       {"unknown", `{"providers":["nope"]}`},
		"twice":         {"twice", `{"providers":["redis","redis"]}`},
		"long text":     {"long", `{"description":"` + strings.Repeat("x", 201) + `","providers":["redis"]}`},
		"built in name": {"event-driven", `{"providers":["redis"]}`},
	} {
		r := coreDo(h, "PUT", "/api/stacks/"+c[0], c[1])
		coreEq(t, 400, r.Code, name+": "+r.Body)
	}
	coreEq(t, 400, coreDo(h, "PUT", "/api/stacks/ok-name", "not json").Code)
}

func TestBuiltInStacksCannotBeDeleted(t *testing.T) {
	h, _, _, _ := varsServer(t)
	r := coreDo(h, "DELETE", "/api/stacks/event-driven", "")
	coreEq(t, 409, r.Code)
	coreContains(t, r.Body, "comes with Nimbo")
}

func TestCustomStacksLimitAndPersistence(t *testing.T) {
	store := NewMemoryDataStore()
	c := NewCustomStacks(store)
	for i := 0; i < maxCustomStacks; i++ {
		coreTrue(t, c.Put(StackDef{ID: "s" + itoaForTest(i), Providers: []string{"redis"}}, nil) == nil)
	}
	_, over := c.Put(StackDef{ID: "one-more", Providers: []string{"redis"}}, nil).(InvalidParam)
	coreTrue(t, over, "the 51st is refused")
	coreTrue(t, c.Put(StackDef{ID: "s1", Description: "changed", Providers: []string{"redis"}}, nil) == nil, "replacing is always allowed")
	// A new instance over the same store sees them, as after a restart.
	coreEq(t, maxCustomStacks, len(NewCustomStacks(store).List()))
}

func TestCustomStacksTravelWithABackup(t *testing.T) {
	a := NewAppData(nil, nil)
	coreTrue(t, NewCustomStacks(a.Store).Put(StackDef{ID: "mine", Providers: []string{"redis", "minio"}}, nil) == nil)
	bundle := a.Export()
	_, has := bundle.Files[FileCustomStacks]
	coreTrue(t, has, "the backup carries your stacks")
	_, hasSecrets := bundle.Files[FileSecrets]
	coreTrue(t, !hasSecrets)

	b := NewAppData(nil, nil)
	_, err := b.Restore(bundle)
	coreTrue(t, err == nil)
	coreEq(t, 1, len(NewCustomStacks(b.Store).List()))

	bad := DataBundle{Format: 1, Files: map[string]string{FileCustomStacks: "[1,2]"}}
	_, err = b.Restore(bad)
	coreTrue(t, err != nil, "a damaged stacks file is refused before anything is written")
}

func TestCustomStacksIgnoreEmulatorsThatNoLongerExist(t *testing.T) {
	store := NewMemoryDataStore()
	_ = store.Write(FileCustomStacks, `{"stacks":[{"id":"old","description":"","providers":["redis","gone"]},{"id":"dead","providers":["gone"]},{"id":"Bad Id","providers":["redis"]}]}`)
	list := NewCustomStacks(store).List()
	coreEq(t, 1, len(list))
	coreEq(t, "redis", strings.Join(list[0].Providers, ","))
}
