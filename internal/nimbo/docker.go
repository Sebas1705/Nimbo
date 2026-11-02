package nimbo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ContainerInfo describes a container Nimbo manages. Persist is how it was created: with its data in a volume, or without.
type ContainerInfo struct {
	ProviderID string
	Running    bool
	Status     string
	Persist    bool
}

// ContainerRuntime starts and stops the providers. Failures are returned as plain errors; the routes turn them into
// 409 answers carrying the message.
type ContainerRuntime interface {
	Status(ctx context.Context) DockerStatusDto
	Containers(ctx context.Context) map[string]ContainerInfo
	// Start starts the provider, keeping its data in a volume when persist is true and the provider can. Runtimes
	// without volumes ignore the flag.
	Start(ctx context.Context, def ProviderDef, hostEnv map[string]string, persist bool) error
	Stop(ctx context.Context, def ProviderDef) error
	Remove(ctx context.Context, def ProviderDef) error
	Logs(ctx context.Context, def ProviderDef, tail int) (string, error)
	// SavedData is the set of ids of the providers that have saved data in a volume.
	SavedData(ctx context.Context) map[string]bool
	// DeleteData deletes the saved data of a provider (and its stopped container).
	DeleteData(ctx context.Context, def ProviderDef) error
}

const (
	LabelManaged  = "nimbo.managed"
	LabelProvider = "nimbo.provider"
	LabelPersist  = "nimbo.persist"

	ContainerNamePrefix = "nimbo-"
	VolumeSuffix        = "-data"
)

func ContainerName(def ProviderDef) string { return ContainerNamePrefix + def.ID }

// VolumeName: one named volume per provider, so deleting a container never deletes what it saved.
func VolumeName(def ProviderDef) string { return ContainerNamePrefix + def.ID + VolumeSuffix }

// CliContainerRuntime drives the local `docker` CLI. It only ever touches containers labelled `nimbo.managed=true`.
type CliContainerRuntime struct{ exec Exec }

func NewCliContainerRuntime(exec Exec) *CliContainerRuntime { return &CliContainerRuntime{exec: exec} }

func (c *CliContainerRuntime) Status(ctx context.Context) DockerStatusDto {
	r := c.exec.Run(ctx, []string{"info", "--format", "{{.ServerVersion}}"}, 15)
	if r.OK() {
		lines := strings.Split(r.Output, "\n")
		return DockerStatusDto{Available: true, Version: coreStr(strings.TrimSpace(lines[len(lines)-1]))}
	}
	return DockerStatusDto{Available: false, Error: coreStr("Docker daemon not reachable")}
}

func (c *CliContainerRuntime) Containers(ctx context.Context) map[string]ContainerInfo {
	out := map[string]ContainerInfo{}
	r := c.exec.Run(ctx, []string{
		"ps", "-a", "--filter", "label=" + LabelManaged + "=true",
		// No quotes in the template: Windows does not escape them reliably for docker.exe.
		"--format", "{{.Names}}|{{.State}}|{{.Status}}|{{.Labels}}",
	}, 15)
	if !r.OK() {
		return out
	}
	for _, line := range strings.Split(r.Output, "\n") {
		parts := strings.SplitN(strings.TrimRight(line, "\r"), "|", 4)
		if len(parts) < 3 {
			continue
		}
		id := strings.TrimPrefix(parts[0], ContainerNamePrefix)
		if strings.TrimSpace(id) == "" {
			continue
		}
		persist := len(parts) > 3 && strings.Contains(parts[3], LabelPersist+"=true")
		out[id] = ContainerInfo{ProviderID: id, Running: parts[1] == "running", Status: parts[2], Persist: persist}
	}
	return out
}

func (c *CliContainerRuntime) Start(ctx context.Context, def ProviderDef, hostEnv map[string]string, persist bool) error {
	var missing []string
	for _, v := range def.Vars {
		if v.Required && strings.TrimSpace(hostEnv[v.Name]) == "" {
			missing = append(missing, v.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Missing settings: %s. Initialize them in Nimbo (Settings > Emulator variables), or set them in Nimbo's environment (a .env file with docker compose, or 'doppler run --').", strings.Join(missing, ", "))
	}
	wanted := persist && def.Persist != nil
	existing, found := c.Containers(ctx)[def.ID]
	if found && (existing.Persist == wanted || existing.Running) {
		return c.run(ctx, []string{"start", ContainerName(def)})
	}
	// A stopped container created in the other mode is replaced; the volume, if any, is left alone.
	if found {
		_ = c.run(ctx, []string{"rm", "-f", ContainerName(def)})
	}
	return c.run(ctx, BuildRunArgs(def, hostEnv, wanted))
}

func (c *CliContainerRuntime) SavedData(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	r := c.exec.Run(ctx, []string{"volume", "ls", "--filter", "name=" + ContainerNamePrefix, "--format", "{{.Name}}"}, 15)
	if !r.OK() {
		return out
	}
	for _, line := range strings.Split(r.Output, "\n") {
		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, ContainerNamePrefix) && strings.HasSuffix(name, VolumeSuffix) {
			out[strings.TrimSuffix(strings.TrimPrefix(name, ContainerNamePrefix), VolumeSuffix)] = true
		}
	}
	return out
}

func (c *CliContainerRuntime) DeleteData(ctx context.Context, def ProviderDef) error {
	existing, found := c.Containers(ctx)[def.ID]
	if found && existing.Running {
		return fmt.Errorf("Stop %s before deleting its data", def.Name)
	}
	if found {
		_ = c.run(ctx, []string{"rm", "-f", ContainerName(def)})
	}
	r := c.exec.Run(ctx, []string{"volume", "rm", VolumeName(def)}, 30)
	if r.OK() || strings.Contains(r.Output, "No such volume") {
		return nil
	}
	return errors.New(orDefault(strings.TrimSpace(r.Output), "docker failed"))
}

func (c *CliContainerRuntime) Stop(ctx context.Context, def ProviderDef) error {
	return c.run(ctx, []string{"stop", ContainerName(def)})
}

func (c *CliContainerRuntime) Remove(ctx context.Context, def ProviderDef) error {
	return c.run(ctx, []string{"rm", "-f", ContainerName(def)})
}

func (c *CliContainerRuntime) Logs(ctx context.Context, def ProviderDef, tail int) (string, error) {
	if tail < 1 {
		tail = 1
	}
	if tail > 2000 {
		tail = 2000
	}
	r := c.exec.Run(ctx, []string{"logs", "--tail", strconv.Itoa(tail), ContainerName(def)}, 15)
	if r.OK() {
		return r.Output, nil
	}
	return "", errors.New(r.Output)
}

func (c *CliContainerRuntime) run(ctx context.Context, args []string) error {
	r := c.exec.Run(ctx, args, 120)
	if r.OK() {
		return nil
	}
	return errors.New(orDefault(strings.TrimSpace(r.Output), "docker failed"))
}

// BuildRunArgs is the `docker run` command line for a provider. Secrets come from hostEnv (callers check they are
// there first); ports are only ever published on loopback.
func BuildRunArgs(def ProviderDef, hostEnv map[string]string, persist bool) []string {
	var keep *PersistSpec
	if persist {
		keep = def.Persist
	}
	args := []string{"run", "-d", "--name", ContainerName(def)}
	args = append(args, "--label", LabelManaged+"=true", "--label", LabelProvider+"="+def.ID, "--label", fmt.Sprintf("%s=%t", LabelPersist, keep != nil))
	for _, p := range def.Ports {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", p, p))
	}
	if keep != nil {
		args = append(args, "-v", VolumeName(def)+":"+keep.MountPath)
		if keep.User != "" {
			args = append(args, "--user", keep.User)
		}
	}
	for _, kv := range def.ContainerEnv {
		args = append(args, "-e", kv.Key+"="+kv.Value)
	}
	if keep != nil {
		for _, kv := range keep.Env {
			args = append(args, "-e", kv.Key+"="+kv.Value)
		}
	}
	for _, v := range def.Vars {
		// Optional settings only reach the container when they have a value.
		if val := strings.TrimSpace(hostEnv[v.Name]); v.Container != "" && val != "" {
			args = append(args, "-e", v.Container+"="+hostEnv[v.Name])
		}
	}
	args = append(args, def.RunArgs...)
	args = append(args, def.Image)
	if keep != nil && keep.Command != nil {
		args = append(args, keep.Command...)
	} else {
		args = append(args, def.Command...)
	}
	return args
}

// coreSortedKeys returns the keys of a set in order, for stable output.
func coreSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
