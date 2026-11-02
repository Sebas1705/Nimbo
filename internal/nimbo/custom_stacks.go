package nimbo

import (
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"
)

// FileCustomStacks holds the stacks you make in the app, next to the ones that come with Nimbo or your nimbo.yml.
const FileCustomStacks = "stacks-custom.json"

const maxCustomStacks = 50

type customStackJSON struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Providers   []string `json:"providers"`
}

type customStackFile struct {
	Stacks []customStackJSON `json:"stacks"`
}

// CustomStacks are the groups of emulators a person puts together in the app. They live in the data folder, so they
// survive updates and travel with a backup of it.
type CustomStacks struct {
	store DataStore
	mu    sync.Mutex
}

func NewCustomStacks(store DataStore) *CustomStacks { return &CustomStacks{store: store} }

func (c *CustomStacks) read() customStackFile {
	return coreReadOrDefault(c.store, FileCustomStacks, func() customStackFile { return customStackFile{} })
}

// List returns the saved stacks, oldest first.
func (c *CustomStacks) List() []StackDef {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []StackDef{}
	for _, s := range c.read().Stacks {
		// A restored or hand-edited file may name an emulator this version does not have: those are left out.
		var known []string
		for _, id := range s.Providers {
			if CatalogFind(id) != nil {
				known = append(known, id)
			}
		}
		if len(known) > 0 && stackIDRe.MatchString(s.ID) {
			out = append(out, StackDef{ID: s.ID, Description: s.Description, Providers: known})
		}
	}
	return out
}

// Put creates or replaces a stack. taken says whether an id belongs to a stack that is not one of yours: those cannot be
// replaced, so a built-in stack never changes under you.
func (c *CustomStacks) Put(def StackDef, taken func(id string) bool) error {
	if !stackIDRe.MatchString(def.ID) {
		return InvalidParam{"The name must be lowercase letters, digits and hyphens, starting with a letter or digit (up to 41 characters)"}
	}
	if utf8.RuneCountInString(def.Description) > 200 {
		return InvalidParam{"The description can have at most 200 characters"}
	}
	if len(def.Providers) == 0 {
		return InvalidParam{"Pick at least one emulator"}
	}
	seen := map[string]bool{}
	for _, id := range def.Providers {
		if CatalogFind(id) == nil {
			return InvalidParam{"Unknown emulator '" + id + "'"}
		}
		if seen[id] {
			return InvalidParam{"'" + id + "' is picked twice"}
		}
		seen[id] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	file := c.read()
	entry := customStackJSON{ID: def.ID, Description: strings.TrimSpace(def.Description), Providers: def.Providers}
	for i := range file.Stacks {
		if file.Stacks[i].ID == def.ID {
			file.Stacks[i] = entry
			return coreWriteJSON(c.store, FileCustomStacks, file)
		}
	}
	if taken != nil && taken(def.ID) {
		return InvalidParam{"'" + def.ID + "' is the name of a stack that comes with Nimbo: choose another"}
	}
	if len(file.Stacks) >= maxCustomStacks {
		return InvalidParam{"There are already 50 stacks of your own: delete one first"}
	}
	file.Stacks = append(file.Stacks, entry)
	return coreWriteJSON(c.store, FileCustomStacks, file)
}

// Delete removes one of your stacks. It reports false when there was none with that id.
func (c *CustomStacks) Delete(id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	file := c.read()
	kept := file.Stacks[:0]
	found := false
	for _, s := range file.Stacks {
		if s.ID == id {
			found = true
			continue
		}
		kept = append(kept, s)
	}
	if !found {
		return false, nil
	}
	file.Stacks = kept
	return true, coreWriteJSON(c.store, FileCustomStacks, file)
}

func (c *CustomStacks) has(id string) bool {
	for _, s := range c.List() {
		if s.ID == id {
			return true
		}
	}
	return false
}

// ---- routes ---------------------------------------------------------------------------------------------------------

type saveStackBody struct {
	Description string   `json:"description"`
	Providers   []string `json:"providers"`
}

func (s *StackRoutes) custom() *CustomStacks { return NewCustomStacks(s.Data.Store) }

// allStacks is what comes with Nimbo or your nimbo.yml, then the ones you made.
func (s *StackRoutes) allStacks() []StackDef {
	out := append([]StackDef{}, s.Stacks.Stacks...)
	return append(out, s.custom().List()...)
}

func (s *StackRoutes) save(w http.ResponseWriter, r *http.Request) error {
	id := Param(r, "id")
	var body saveStackBody
	if err := ReadJSON(r, &body); err != nil {
		return err
	}
	def := StackDef{ID: id, Description: body.Description, Providers: body.Providers}
	if err := s.custom().Put(def, func(id string) bool { return s.Stacks.Find(id) != nil }); err != nil {
		return err
	}
	s.Data.Stats.Record("stack.save", id)
	dto := coreStackDto(def, map[string]ContainerInfo{})
	dto.Custom = true
	WriteJSON(w, http.StatusOK, dto)
	return nil
}

func (s *StackRoutes) remove(w http.ResponseWriter, r *http.Request) error {
	id := Param(r, "id")
	if s.Stacks.Find(id) != nil && !s.custom().has(id) {
		WriteJSON(w, http.StatusConflict, ErrorDto{"'" + id + "' comes with Nimbo and cannot be deleted"})
		return nil
	}
	ok, err := s.custom().Delete(id)
	if err != nil {
		return err
	}
	if !ok {
		return UnknownTarget{"Unknown stack '" + id + "'"}
	}
	s.Data.Stats.Record("stack.delete", id)
	WriteJSON(w, http.StatusNoContent, nil)
	return nil
}
