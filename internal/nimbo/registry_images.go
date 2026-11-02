package nimbo

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// FileRegistryImages holds the images a person registered. It is a plain list of definitions: the values of their secret
// keys live in the secret store like those of any emulator, never here.
const FileRegistryImages = "registry-images.json"

const (
	maxRegistryImages     = 50
	maxImagePorts         = 10
	maxImageEnv           = 30
	maxImageSecretKeys    = 20
	maxImageCommandItems  = 20
	maxImageCommandLength = 200
	maxImageEnvValue      = 500
)

var (
	registryIDRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	registryImageRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{0,200}$`)
	registryEnvKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	registryVolumeRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{0,200}$`)
)

// ImageEnv is one fixed, non-secret variable of the container.
type ImageEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ImageDef is an image a person registered. It runs like one more emulator: no privileged mode, no host mounts, no extra
// docker flags and no network options exist in this model, by design.
type ImageDef struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Image      string     `json:"image"`
	Ports      []int      `json:"ports"`
	Env        []ImageEnv `json:"env"`
	SecretKeys []string   `json:"secretKeys"`
	Command    []string   `json:"command"`
	// Volume is the folder inside the container whose data can be kept (the "keep data" switch), or null.
	Volume *string `json:"volume"`
	Notes  string  `json:"notes"`
}

// ImageSecretDto says which secret holds one secret key of an image, and whether it has a value. Never the value.
type ImageSecretDto struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

// ImageDto is what the API answers: the definition plus the secrets it reads.
type ImageDto struct {
	ImageDef
	Secrets []ImageSecretDto `json:"secrets"`
	Custom  bool             `json:"custom"`
}

type imageBody struct {
	Name       string     `json:"name"`
	Image      string     `json:"image"`
	Ports      []int      `json:"ports"`
	Env        []ImageEnv `json:"env"`
	SecretKeys []string   `json:"secretKeys"`
	Command    []string   `json:"command"`
	Volume     *string    `json:"volume"`
	Notes      string     `json:"notes"`
}

type imageFile struct {
	Items []ImageDef `json:"items"`
}

// ImageRegistry keeps the registered images, validates them and hands them to the rest of Nimbo as providers
// (SetCustomProviders). Building one loads the saved images, so create it before anything that looks providers up.
type ImageRegistry struct {
	store   DataStore
	data    *AppData
	runtime ContainerRuntime
	secrets SecretStore
	hostEnv map[string]string
	mu      sync.Mutex
}

// NewImageRegistry loads the saved images and publishes them as providers. runtime is only asked which containers exist
// (a registered image cannot be deleted while its container does); secrets and hostEnv only tell whether a secret key has
// a value.
func NewImageRegistry(store DataStore, data *AppData, runtime ContainerRuntime, secrets SecretStore, hostEnv map[string]string) *ImageRegistry {
	r := &ImageRegistry{store: store, data: data, runtime: runtime, secrets: secrets, hostEnv: hostEnv}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publish(r.load())
	return r
}

// load reads the file. An entry that no longer passes validation (a hand-edited or restored file, a port a new
// built-in emulator now uses) is left out rather than breaking the others; the file itself is not rewritten.
func (r *ImageRegistry) load() []ImageDef {
	file := coreReadOrDefault(r.store, FileRegistryImages, func() imageFile { return imageFile{} })
	var kept []ImageDef
	for _, it := range file.Items {
		it = normalizeImage(it)
		if validateImage(it, kept) == nil && len(kept) < maxRegistryImages {
			kept = append(kept, it)
		}
	}
	return kept
}

// publish makes the list visible to the rest of Nimbo.
func (r *ImageRegistry) publish(items []ImageDef) {
	defs := make([]ProviderDef, 0, len(items))
	for _, it := range items {
		defs = append(defs, imageProvider(it))
	}
	SetCustomProviders(defs)
}

func (r *ImageRegistry) save(items []ImageDef) error {
	if items == nil {
		items = []ImageDef{}
	}
	if err := coreWriteJSON(r.store, FileRegistryImages, imageFile{Items: items}); err != nil {
		return err
	}
	r.publish(items)
	return nil
}

// CustomSecretName is the secret that holds one secret key of an image: NIMBO_C_<ID>_<KEY>, upper case, hyphens as
// underscores.
func CustomSecretName(id, key string) string {
	return "NIMBO_C_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_" + strings.ToUpper(key)
}

// imageProvider turns a definition into the provider Nimbo runs.
func imageProvider(it ImageDef) ProviderDef {
	def := ProviderDef{
		ID: it.ID, Name: it.Name, Cloud: CloudOther, Custom: true,
		Description: orDefault(it.Notes, "Your own image, "+it.Image+"."),
		Image:       it.Image,
		Ports:       append([]int{}, it.Ports...),
	}
	if len(it.Command) > 0 {
		def.Command = append([]string{}, it.Command...)
	}
	for _, e := range it.Env {
		def.ContainerEnv = append(def.ContainerEnv, KV{e.Key, e.Value})
	}
	for _, k := range it.SecretKeys {
		// Optional on purpose: an image that does not need the key still starts, and the value is only handed to it when set.
		def.Vars = append(def.Vars, VarSpec{Name: CustomSecretName(it.ID, k), Container: k, Secret: true, Generate: "password"})
	}
	if it.Volume != nil {
		def.Persist = &PersistSpec{MountPath: *it.Volume}
	}
	return def
}

// normalizeImage trims the free text and makes every list a real list.
func normalizeImage(it ImageDef) ImageDef {
	it.Name = strings.TrimSpace(it.Name)
	if it.Name == "" {
		it.Name = it.ID
	}
	it.Image = strings.TrimSpace(it.Image)
	it.Notes = strings.TrimSpace(it.Notes)
	if it.Ports == nil {
		it.Ports = []int{}
	}
	if it.Env == nil {
		it.Env = []ImageEnv{}
	}
	if it.SecretKeys == nil {
		it.SecretKeys = []string{}
	}
	if it.Command == nil {
		it.Command = []string{}
	}
	if it.Volume != nil && strings.TrimSpace(*it.Volume) == "" {
		it.Volume = nil
	}
	return it
}

// validateImage checks a definition against the rules and against everything it could collide with: built-in
// providers and the other registered images (others must not contain the definition itself).
func validateImage(it ImageDef, others []ImageDef) error {
	if !registryIDRe.MatchString(it.ID) {
		return InvalidParam{"The id must be lowercase letters, digits and hyphens, starting with a letter or digit (up to 31 characters)"}
	}
	if CatalogFind(it.ID) != nil {
		return InvalidParam{"'" + it.ID + "' is the id of an emulator that comes with Nimbo: choose another"}
	}
	for _, o := range others {
		if o.ID == it.ID {
			return InvalidParam{"There is already an image called '" + it.ID + "'"}
		}
	}
	if utf8.RuneCountInString(it.Name) > 60 {
		return InvalidParam{"The name can have at most 60 characters"}
	}
	if utf8.RuneCountInString(it.Notes) > 500 {
		return InvalidParam{"The notes can have at most 500 characters"}
	}
	if secretControlRe.MatchString(it.Name) || secretControlRe.MatchString(it.Notes) {
		return InvalidParam{"The name and the notes cannot contain control characters"}
	}
	if !registryImageRe.MatchString(it.Image) {
		return InvalidParam{"The image must look like ghcr.io/me/api:1.2: lowercase letters, digits and . _ / : @ -, starting with a letter or digit"}
	}
	if err := validateImagePorts(it, others); err != nil {
		return err
	}
	if len(it.Env) > maxImageEnv {
		return InvalidParam{fmt.Sprintf("At most %d variables are allowed", maxImageEnv)}
	}
	if len(it.SecretKeys) > maxImageSecretKeys {
		return InvalidParam{fmt.Sprintf("At most %d secret keys are allowed", maxImageSecretKeys)}
	}
	keys := map[string]bool{}
	for _, e := range it.Env {
		if !registryEnvKeyRe.MatchString(e.Key) {
			return InvalidParam{"The variable name '" + e.Key + "' must be letters, digits and underscores, not starting with a digit (up to 64 characters)"}
		}
		if keys[e.Key] {
			return InvalidParam{"'" + e.Key + "' is used twice among the variables and secret keys"}
		}
		keys[e.Key] = true
		if utf8.RuneCountInString(e.Value) > maxImageEnvValue {
			return InvalidParam{fmt.Sprintf("The value of %s can have at most %d characters", e.Key, maxImageEnvValue)}
		}
		if secretControlRe.MatchString(e.Value) {
			return InvalidParam{"The value of " + e.Key + " cannot contain control characters"}
		}
	}
	mine := map[string]bool{}
	for _, k := range it.SecretKeys {
		if !registryEnvKeyRe.MatchString(k) {
			return InvalidParam{"The secret key '" + k + "' must be letters, digits and underscores, not starting with a digit (up to 64 characters)"}
		}
		if keys[k] {
			return InvalidParam{"'" + k + "' is used twice among the variables and secret keys"}
		}
		keys[k] = true
		mine[CustomSecretName(it.ID, k)] = true
	}
	// "a-b" with key K and "a" with key B_K would both be NIMBO_C_A_B_K; two images must never share a secret.
	for _, o := range others {
		for _, k := range o.SecretKeys {
			if mine[CustomSecretName(o.ID, k)] {
				return InvalidParam{"A secret key of this image would share its secret name " + CustomSecretName(o.ID, k) + " with the image '" + o.ID + "': rename a key"}
			}
		}
	}
	if len(it.Command) > maxImageCommandItems {
		return InvalidParam{fmt.Sprintf("The command can have at most %d items", maxImageCommandItems)}
	}
	for _, c := range it.Command {
		if utf8.RuneCountInString(c) > maxImageCommandLength {
			return InvalidParam{fmt.Sprintf("Each command item can have at most %d characters", maxImageCommandLength)}
		}
		if secretControlRe.MatchString(c) {
			return InvalidParam{"The command cannot contain control characters"}
		}
	}
	if it.Volume != nil && !registryVolumeRe.MatchString(*it.Volume) {
		return InvalidParam{"The volume must be an absolute folder inside the container, like /data"}
	}
	return nil
}

func validateImagePorts(it ImageDef, others []ImageDef) error {
	if len(it.Ports) > maxImagePorts {
		return InvalidParam{fmt.Sprintf("At most %d ports are allowed", maxImagePorts)}
	}
	used := map[int]string{}
	for _, p := range CatalogProviders {
		for _, port := range p.Ports {
			used[port] = p.Name
		}
	}
	for _, o := range others {
		for _, port := range o.Ports {
			used[port] = o.Name
		}
	}
	seen := map[int]bool{}
	for _, p := range it.Ports {
		switch {
		case p < 1024 || p > 65535:
			return InvalidParam{fmt.Sprintf("Port %d is not allowed: use a port between 1024 and 65535", p)}
		case seen[p]:
			return InvalidParam{fmt.Sprintf("Port %d is listed twice", p)}
		case used[p] != "":
			return InvalidParam{fmt.Sprintf("Port %d is already used by %s", p, used[p])}
		}
		seen[p] = true
	}
	return nil
}

func without(items []ImageDef, id string) []ImageDef {
	out := make([]ImageDef, 0, len(items))
	for _, it := range items {
		if it.ID != id {
			out = append(out, it)
		}
	}
	return out
}

func (r *ImageRegistry) dto(ctx context.Context, it ImageDef) ImageDto {
	def := imageProvider(it)
	sources := VarSources(ctx, def, r.hostEnv, r.secrets)
	secrets := make([]ImageSecretDto, 0, len(it.SecretKeys))
	for _, k := range it.SecretKeys {
		name := CustomSecretName(it.ID, k)
		secrets = append(secrets, ImageSecretDto{Key: k, Name: name, Set: sources[name] != SourceUnset})
	}
	return ImageDto{ImageDef: it, Secrets: secrets, Custom: true}
}

// List returns the registered images, oldest first.
func (r *ImageRegistry) List(ctx context.Context) []ImageDto {
	r.mu.Lock()
	items := r.load()
	r.mu.Unlock()
	out := make([]ImageDto, 0, len(items))
	for _, it := range items {
		out = append(out, r.dto(ctx, it))
	}
	return out
}

// Put creates or replaces an image.
func (r *ImageRegistry) Put(ctx context.Context, id string, body imageBody) (ImageDto, error) {
	it := normalizeImage(ImageDef{
		ID: id, Name: body.Name, Image: body.Image, Ports: body.Ports, Env: body.Env, SecretKeys: body.SecretKeys,
		Command: body.Command, Volume: body.Volume, Notes: body.Notes,
	})
	r.mu.Lock()
	items := r.load()
	if err := validateImage(it, without(items, id)); err != nil {
		r.mu.Unlock()
		return ImageDto{}, err
	}
	replaced := false
	for i := range items {
		if items[i].ID == id {
			items[i], replaced = it, true
		}
	}
	if !replaced {
		if len(items) >= maxRegistryImages {
			r.mu.Unlock()
			return ImageDto{}, InvalidParam{fmt.Sprintf("There are already %d images of your own: delete one first", maxRegistryImages)}
		}
		items = append(items, it)
	}
	err := r.save(items)
	r.mu.Unlock()
	if err != nil {
		return ImageDto{}, err
	}
	r.data.Stats.Record("registry.image.save", id)
	return r.dto(ctx, it), nil
}

// ErrImageHasContainer is returned by Delete while the image's container exists.
type ErrImageHasContainer struct{ ID string }

func (e ErrImageHasContainer) Error() string {
	return "'" + e.ID + "' still has a container: remove it first (its saved data and secrets are kept)"
}

// Delete removes an image from the registry. Its container must be gone first; its data volume and its secrets are not
// touched. It reports false when there was no such image.
func (r *ImageRegistry) Delete(ctx context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.load()
	found := false
	for _, it := range items {
		found = found || it.ID == id
	}
	if !found {
		return false, nil
	}
	if _, exists := r.runtime.Containers(ctx)[id]; exists {
		return true, ErrImageHasContainer{id}
	}
	if err := r.save(without(items, id)); err != nil {
		return true, err
	}
	r.data.Stats.Record("registry.image.delete", id)
	return true, nil
}

// Register adds the routes of the image registry.
func (r *ImageRegistry) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/registry/images", Handle(func(w http.ResponseWriter, req *http.Request) error {
		WriteJSON(w, http.StatusOK, r.List(req.Context()))
		return nil
	}))
	mux.HandleFunc("PUT /api/registry/images/{id}", Handle(func(w http.ResponseWriter, req *http.Request) error {
		var body imageBody
		if err := ReadJSON(req, &body); err != nil {
			return err
		}
		dto, err := r.Put(req.Context(), Param(req, "id"), body)
		if err != nil {
			return err
		}
		WriteJSON(w, http.StatusOK, dto)
		return nil
	}))
	mux.HandleFunc("DELETE /api/registry/images/{id}", Handle(func(w http.ResponseWriter, req *http.Request) error {
		id := Param(req, "id")
		found, err := r.Delete(req.Context(), id)
		if !found {
			return UnknownTarget{"Unknown image '" + id + "'"}
		}
		if err != nil {
			if _, busy := err.(ErrImageHasContainer); busy {
				WriteJSON(w, http.StatusConflict, ErrorDto{err.Error()})
				return nil
			}
			return err
		}
		WriteJSON(w, http.StatusNoContent, nil)
		return nil
	}))
}
