package nimbo

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed resources/image-packs.json
var imagePacksJSON []byte

type PackDef struct {
	ID     string   `json:"id"`
	Images []string `json:"images"`
}

type ImageState struct {
	Provider string  `json:"provider"`
	Image    string  `json:"image"`
	Present  bool    `json:"present"`
	Pack     *string `json:"pack"`
}

// PackFile is an image pack on disk. Verified is null when no SHA256SUMS sits beside the file, true when it matched
// and false when it did not.
type PackFile struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	Pack     *string `json:"pack"`
	Verified *bool   `json:"verified"`
}

type ImagesStatus struct {
	Docker  bool         `json:"docker"`
	Images  []ImageState `json:"images"`
	Packs   []PackDef    `json:"packs"`
	Files   []PackFile   `json:"files"`
	Folders []string     `json:"folders"`
}

type LoadImagesDto struct {
	Path string `json:"path"`
}

type PullImageDto struct {
	Provider string `json:"provider"`
}

type ImagesLoadedDto struct {
	Loaded []string `json:"loaded"`
}

// ImageProviderRef is a provider and the container image it runs from: all the image service needs of the catalog.
// The integrator builds the list from the real catalog.
type ImageProviderRef struct{ ID, Image string }

// DefaultImagePacks reads the packs embedded in the binary (resources/image-packs.json).
func DefaultImagePacks() []PackDef {
	var file struct {
		Packs []PackDef `json:"packs"`
	}
	if err := json.Unmarshal(imagePacksJSON, &file); err != nil {
		panic("image-packs.json is not valid: " + err.Error())
	}
	return file.Packs
}

// CheckImagePacks reports every provider image that is not in exactly one pack and every pack image no provider
// uses. The integrator calls it from a test with the real catalog so the packs cannot drift from it.
func CheckImagePacks(packs []PackDef, providerImages []string) []string {
	var problems []string
	known := map[string]bool{}
	for _, image := range providerImages {
		known[image] = true
		n := 0
		for _, p := range packs {
			for _, i := range p.Images {
				if i == image {
					n++
					break
				}
			}
		}
		if n != 1 {
			problems = append(problems, fmt.Sprintf("image '%s' must be in exactly one pack (is in %d)", image, n))
		}
	}
	ids := map[string]bool{}
	for _, p := range packs {
		if ids[p.ID] {
			problems = append(problems, fmt.Sprintf("pack id '%s' is repeated", p.ID))
		}
		ids[p.ID] = true
		for _, i := range p.Images {
			if !known[i] {
				problems = append(problems, fmt.Sprintf("pack lists '%s' which no provider uses", i))
			}
		}
	}
	return problems
}

// Images is the container images the emulators run from. Online they are pulled on first start; offline they come
// from image packs (`docker save` files published with each release, or made by you) loaded with `docker load`.
type Images struct {
	exec      Exec
	folders   func() []string
	log       updateActivity
	providers []ImageProviderRef
	packs     []PackDef
	lock      sync.Mutex
}

// NewImages builds the image service. folders says where packs are looked for (see ImagesSearchFolders); a nil
// packs uses DefaultImagePacks().
func NewImages(exec Exec, folders func() []string, log updateActivity, providers []ImageProviderRef, packs []PackDef) *Images {
	if packs == nil {
		packs = DefaultImagePacks()
	}
	return &Images{exec: exec, folders: folders, log: log, providers: providers, packs: packs}
}

func (m *Images) Status(ctx context.Context) ImagesStatus {
	local := m.exec.Run(ctx, []string{"images", "--format", "{{.Repository}}:{{.Tag}}"}, 30)
	have := map[string]bool{}
	if local.OK() {
		for _, l := range strings.Split(local.Output, "\n") {
			have[strings.TrimSpace(l)] = true
		}
	}
	images := []ImageState{}
	for _, p := range m.providers {
		var pack *string
		for _, d := range m.packs {
			if imgContains(d.Images, p.Image) {
				id := d.ID
				pack = &id
				break
			}
		}
		images = append(images, ImageState{p.ID, p.Image, have[ImageNormalize(p.Image)], pack})
	}
	folders := []string{}
	for _, f := range m.folders() {
		folders = append(folders, filepath.Clean(f))
	}
	packs := m.packs
	if packs == nil {
		packs = []PackDef{}
	}
	return ImagesStatus{local.OK(), images, packs, m.Scan(), folders}
}

func imgContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Scan lists the pack files found in the usual folders. Nothing outside them is listed, but Load accepts a path
// you type.
func (m *Images) Scan() []PackFile {
	out := []PackFile{}
	seen := map[string]bool{}
	for _, raw := range m.folders() {
		dir := filepath.Clean(raw)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		var sums map[string]string
		if text, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS")); err == nil {
			sums = UpdateParseSums(string(text))
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := []string{}
		for _, e := range entries {
			if !e.IsDir() && ImageIsPack(e.Name()) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			path := filepath.Join(dir, name)
			st, err := os.Stat(path)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			var verified *bool
			if want, ok := sums[name]; ok {
				if got, err := UpdateFileSHA256(path); err == nil {
					v := strings.EqualFold(want, got)
					verified = &v
				}
			}
			out = append(out, PackFile{name, path, st.Size(), ImagePackOf(name), verified})
		}
	}
	return out
}

var imageLoaded = regexp.MustCompile(`^Loaded image(?: ID)?: (.+)$`)

// Load runs `docker load` on a pack, after checking it against a SHA256SUMS beside it when there is one.
func (m *Images) Load(ctx context.Context, path string) (ImagesLoadedDto, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	file := filepath.Clean(path)
	if st, err := os.Stat(file); err != nil || st.IsDir() || path == "" {
		return ImagesLoadedDto{}, InvalidParam{"No file at that path"}
	}
	name := filepath.Base(file)
	if !ImageIsPack(name) {
		return ImagesLoadedDto{}, InvalidParam{"An image pack is a .tar, .tar.gz or .tgz file made with docker save"}
	}
	expected, hasExpected := "", false
	if text, err := os.ReadFile(filepath.Join(filepath.Dir(file), "SHA256SUMS")); err == nil {
		expected, hasExpected = UpdateParseSums(string(text))[name]
	}
	if hasExpected {
		got, err := UpdateFileSHA256(file)
		if err != nil {
			return ImagesLoadedDto{}, err
		}
		if !strings.EqualFold(expected, got) {
			return ImagesLoadedDto{}, UpdateError{name + " does not match the checksum next to it, so it was not loaded"}
		}
	}
	r := m.exec.Run(ctx, []string{"load", "-i", file}, 1800)
	if !r.OK() {
		return ImagesLoadedDto{}, UpdateError{imageTake(imgOrBlank(r.Output, "docker load failed"), 500)}
	}
	loaded := []string{}
	for _, l := range strings.Split(r.Output, "\n") {
		if mm := imageLoaded.FindStringSubmatch(strings.TrimSpace(l)); mm != nil {
			loaded = append(loaded, mm[1])
		}
	}
	state := ", not verified"
	if hasExpected {
		state = ", verified"
	}
	m.log.Record("images.load", name, fmt.Sprintf("%d images%s", len(loaded), state))
	return ImagesLoadedDto{loaded}, nil
}

// Pull asks docker for the image of one provider.
func (m *Images) Pull(ctx context.Context, providerID string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	var def *ImageProviderRef
	for i := range m.providers {
		if m.providers[i].ID == providerID {
			def = &m.providers[i]
			break
		}
	}
	if def == nil {
		return UnknownTarget{"Unknown provider '" + providerID + "'"}
	}
	r := m.exec.Run(ctx, []string{"pull", def.Image}, 1800)
	if !r.OK() {
		return UpdateError{imageTake(imgOrBlank(r.Output, "docker pull failed"), 500)}
	}
	m.log.Record("images.pull", providerID, "")
	return nil
}

func imgOrBlank(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func imageTake(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

var imagePackName = regexp.MustCompile(`(?i)^.+\.(?:tar|tar\.gz|tgz)$`)

// ImageIsPack says whether a file name looks like a docker save file (and is a bare name, not a path).
func ImageIsPack(name string) bool {
	return imagePackName.MatchString(name) && !strings.ContainsAny(name, `/\`)
}

var imagePackFileName = regexp.MustCompile(`^Nimbo-.+-images-([a-z0-9]+)\.(?:tar|tar\.gz|tgz)$`)

// ImagePackOf maps `Nimbo-0.3.0-images-data.tar.gz` to `data`.
func ImagePackOf(name string) *string {
	if m := imagePackFileName.FindStringSubmatch(name); m != nil {
		return &m[1]
	}
	return nil
}

// ImageNormalize: `docker images` always prints a tag, so an image without one means `latest`.
func ImageNormalize(ref string) string {
	last := ref[strings.LastIndex(ref, "/")+1:]
	if strings.Contains(last, ":") {
		return ref
	}
	return ref + ":latest"
}

// ImagesSearchFolders is where packs are looked for: NIMBO_IMAGES_DIR, an `images` folder beside the app, and the
// `images` folder of the data folder. Empty appDir or dataFolder mean there is none.
func ImagesSearchFolders(env map[string]string, dataFolder, appDir string) []string {
	var out []string
	if d := env["NIMBO_IMAGES_DIR"]; strings.TrimSpace(d) != "" {
		out = append(out, d)
	}
	if appDir != "" {
		out = append(out, filepath.Join(appDir, "images"))
	}
	if dataFolder != "" {
		out = append(out, filepath.Join(dataFolder, "images"))
	}
	return out
}
