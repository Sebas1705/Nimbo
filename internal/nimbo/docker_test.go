package nimbo

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type coreExecFn func(args []string) ExecResult

func (f coreExecFn) Run(_ context.Context, args []string, _ int) ExecResult { return f(args) }

// coreDocker fakes the docker CLI: it lists containers and volumes from strings and records every call.
type coreDocker struct {
	containers, volumes string
	calls               [][]string
}

func (d *coreDocker) Run(_ context.Context, args []string, _ int) ExecResult {
	d.calls = append(d.calls, args)
	switch args[0] {
	case "ps":
		return ExecResult{0, d.containers}
	case "volume":
		if args[1] == "ls" {
			return ExecResult{0, d.volumes}
		}
	}
	return ExecResult{0, ""}
}

func (d *coreDocker) ran(prefix ...string) bool {
	for _, c := range d.calls {
		if len(c) >= len(prefix) && reflect.DeepEqual(c[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

func coreHas(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

func coreIndexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

var coreArgsEnv = map[string]string{"NIMBO_MINIO_USER": "admin", "NIMBO_MINIO_PASSWORD": "pw-12345", "NIMBO_POSTGRES_PASSWORD": "pg"}

func TestRunArgsLabelledLoopbackWithSecrets(t *testing.T) {
	args := BuildRunArgs(*CatalogFind("minio"), map[string]string{"NIMBO_MINIO_USER": "u", "NIMBO_MINIO_PASSWORD": "pw-12345"}, false)
	coreTrue(t, reflect.DeepEqual([]string{"run", "-d", "--name", "nimbo-minio"}, args[:4]))
	coreTrue(t, coreHas(args, "nimbo.managed=true"))
	coreTrue(t, coreHas(args, "127.0.0.1:9000:9000"))
	coreTrue(t, coreHas(args, "MINIO_ROOT_PASSWORD=pw-12345"))
	coreTrue(t, reflect.DeepEqual([]string{"server", "/data", "--console-address", ":9001"}, args[len(args)-4:]))
}

func TestStartRefusesWhenASecretIsMissing(t *testing.T) {
	called := false
	rt := NewCliContainerRuntime(coreExecFn(func([]string) ExecResult { called = true; return ExecResult{0, ""} }))
	err := rt.Start(context.Background(), *CatalogFind("postgres"), map[string]string{}, false)
	coreTrue(t, err != nil)
	coreContains(t, err.Error(), "NIMBO_POSTGRES_PASSWORD")
	coreContains(t, err.Error(), ".env")
	coreContains(t, err.Error(), "doppler")
	coreTrue(t, !called, "docker must not be called")
}

func TestStartReusesAnExistingStoppedContainer(t *testing.T) {
	var calls [][]string
	rt := NewCliContainerRuntime(coreExecFn(func(a []string) ExecResult {
		calls = append(calls, a)
		if a[0] == "ps" {
			return ExecResult{0, "nimbo-redis|exited|Exited (0) 1 minute ago"}
		}
		return ExecResult{0, ""}
	}))
	coreTrue(t, rt.Start(context.Background(), *CatalogFind("redis"), nil, false) == nil)
	coreTrue(t, reflect.DeepEqual([]string{"start", "nimbo-redis"}, calls[len(calls)-1]))
}

func TestPsFormatHasNoQuotes(t *testing.T) {
	var calls [][]string
	rt := NewCliContainerRuntime(coreExecFn(func(a []string) ExecResult {
		calls = append(calls, a)
		return ExecResult{0, "nimbo-redis|running|Up 3 seconds"}
	}))
	coreTrue(t, rt.Containers(context.Background())["redis"].Running)
	for _, a := range calls[0] {
		coreTrue(t, !strings.Contains(a, `"`), "docker args must not contain double quotes")
	}
}

func TestStatusUnavailableWhenInfoFails(t *testing.T) {
	rt := NewCliContainerRuntime(coreExecFn(func([]string) ExecResult { return ExecResult{1, "boom"} }))
	coreTrue(t, !rt.Status(context.Background()).Available)
	ok := NewCliContainerRuntime(coreExecFn(func([]string) ExecResult { return ExecResult{0, "warn\n29.1"} }))
	st := ok.Status(context.Background())
	coreTrue(t, st.Available && *st.Version == "29.1")
}

func TestLogsClampTail(t *testing.T) {
	var got []string
	rt := NewCliContainerRuntime(coreExecFn(func(a []string) ExecResult { got = a; return ExecResult{0, "x"} }))
	_, _ = rt.Logs(context.Background(), *CatalogFind("redis"), 99999)
	coreEq(t, "2000", got[2])
	_, _ = rt.Logs(context.Background(), *CatalogFind("redis"), -4)
	coreEq(t, "1", got[2])
}

func TestPersistenceArgs(t *testing.T) {
	pg := *CatalogFind("postgres")
	off := BuildRunArgs(pg, coreArgsEnv, false)
	coreTrue(t, coreHas(off, "nimbo.persist=false") && !coreHas(off, "-v"))
	on := BuildRunArgs(pg, coreArgsEnv, true)
	vol := "nimbo-postgres-data:" + pg.Persist.MountPath
	coreTrue(t, coreHas(on, "nimbo.persist=true") && coreHas(on, vol))
	coreEq(t, "-v", on[coreIndexOf(on, vol)-1])

	redis := *CatalogFind("redis")
	ra := BuildRunArgs(redis, coreArgsEnv, true)
	coreTrue(t, reflect.DeepEqual([]string{"redis-server", "--appendonly", "yes"}, ra[len(ra)-3:]))
	rb := BuildRunArgs(redis, coreArgsEnv, false)
	coreEq(t, "redis:7-alpine", rb[len(rb)-1], "no command override without persistence")

	f := BuildRunArgs(*CatalogFind("floci-aws"), coreArgsEnv, true)
	coreTrue(t, coreHas(f, "FLOCI_STORAGE_MODE=hybrid") && coreHas(f, "FLOCI_STORAGE_PERSISTENT_PATH=/app/data"))
}

func TestUnprivilegedImagesGetAWritableVolume(t *testing.T) {
	for _, id := range []string{"minio", "jaeger"} {
		on := BuildRunArgs(*CatalogFind(id), coreArgsEnv, true)
		coreEq(t, "0:0", on[coreIndexOf(on, "--user")+1], id)
		coreTrue(t, !coreHas(BuildRunArgs(*CatalogFind(id), coreArgsEnv, false), "--user"), id)
	}
}

func TestStatelessProviderIgnoresPersist(t *testing.T) {
	for _, d := range CatalogProviders {
		if d.Persist == nil {
			args := BuildRunArgs(d, coreArgsEnv, true)
			coreTrue(t, coreHas(args, "nimbo.persist=false") && !coreHas(args, "-v"))
			return
		}
	}
	t.Fatal("no stateless provider")
}

func TestMountPathsAndVolumeNames(t *testing.T) {
	n := 0
	for _, d := range CatalogProviders {
		if d.Persist != nil {
			n++
			coreTrue(t, strings.HasPrefix(d.Persist.MountPath, "/"), d.ID)
			coreEq(t, "nimbo-"+d.ID+"-data", VolumeName(d))
		}
	}
	coreTrue(t, n >= 8)
}

func TestContainersReportHowTheyWereCreated(t *testing.T) {
	d := &coreDocker{containers: "nimbo-postgres|running|Up 1m|nimbo.managed=true,nimbo.persist=true,nimbo.provider=postgres\nnimbo-redis|exited|Exited|nimbo.managed=true,nimbo.persist=false"}
	live := NewCliContainerRuntime(d).Containers(context.Background())
	coreTrue(t, live["postgres"].Persist)
	coreTrue(t, !live["redis"].Persist)
}

func TestStoppedContainerInOtherModeIsReplaced(t *testing.T) {
	pg := *CatalogFind("postgres")
	env := map[string]string{"NIMBO_POSTGRES_PASSWORD": "pg"}
	ctx := context.Background()
	matching := &coreDocker{containers: "nimbo-postgres|exited|Exited|nimbo.managed=true,nimbo.persist=true"}
	_ = NewCliContainerRuntime(matching).Start(ctx, pg, env, true)
	coreTrue(t, matching.ran("start", "nimbo-postgres") && !matching.ran("rm"))

	other := &coreDocker{containers: "nimbo-postgres|exited|Exited|nimbo.managed=true,nimbo.persist=false"}
	_ = NewCliContainerRuntime(other).Start(ctx, pg, env, true)
	coreTrue(t, other.ran("rm", "-f", "nimbo-postgres") && other.ran("run"))
	coreTrue(t, !other.ran("volume", "rm"), "switching mode must never touch the volume")
}

func TestRunningContainerIsNeverReplaced(t *testing.T) {
	d := &coreDocker{containers: "nimbo-postgres|running|Up|nimbo.managed=true,nimbo.persist=false"}
	_ = NewCliContainerRuntime(d).Start(context.Background(), *CatalogFind("postgres"), map[string]string{"NIMBO_POSTGRES_PASSWORD": "pg"}, true)
	coreTrue(t, !d.ran("rm"))
}

func TestSavedDataFromVolumeNames(t *testing.T) {
	d := &coreDocker{volumes: "nimbo-postgres-data\nnimbo-minio-data\nsomething-else\nnimbo-weird"}
	got := NewCliContainerRuntime(d).SavedData(context.Background())
	coreTrue(t, reflect.DeepEqual(map[string]bool{"postgres": true, "minio": true}, got))
}

func TestDeleteData(t *testing.T) {
	pg, ctx := *CatalogFind("postgres"), context.Background()
	running := &coreDocker{containers: "nimbo-postgres|running|Up|nimbo.managed=true,nimbo.persist=true"}
	coreTrue(t, NewCliContainerRuntime(running).DeleteData(ctx, pg) != nil)
	coreTrue(t, !running.ran("volume", "rm"))

	stopped := &coreDocker{containers: "nimbo-postgres|exited|Exited|nimbo.managed=true,nimbo.persist=true"}
	coreTrue(t, NewCliContainerRuntime(stopped).DeleteData(ctx, pg) == nil)
	coreTrue(t, stopped.ran("rm", "-f", "nimbo-postgres") && stopped.ran("volume", "rm", "nimbo-postgres-data"))

	missing := coreExecFn(func(a []string) ExecResult {
		if a[0] == "volume" {
			return ExecResult{1, "Error: No such volume: nimbo-postgres-data"}
		}
		return ExecResult{0, ""}
	})
	coreTrue(t, NewCliContainerRuntime(missing).DeleteData(ctx, pg) == nil)
}

func TestDockerUnavailableMeansNoDataAndNoContainers(t *testing.T) {
	rt := NewCliContainerRuntime(coreExecFn(func([]string) ExecResult { return ExecResult{1, "daemon not running"} }))
	coreEq(t, 0, len(rt.SavedData(context.Background())))
	_, found := rt.Containers(context.Background())["postgres"]
	coreTrue(t, !found)
}
