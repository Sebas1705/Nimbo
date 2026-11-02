package nimbo

import (
	"context"
	"sync"
	"time"
)

// CacheTTL says how long each answer of the container runtime is reused.
type CacheTTL struct {
	// Status is how long "Docker is up" is believed.
	Status time.Duration
	// StatusDown is how long "Docker is not reachable" is believed: short, so starting Docker is noticed quickly.
	StatusDown time.Duration
	// Containers is how long the list of containers is reused.
	Containers time.Duration
	// Saved is how long the list of volumes with saved data is reused.
	Saved time.Duration
}

// DefaultCacheTTL keeps the screens fresh (a change you make shows at once, because every change drops the cache; saved
// data only changes through Nimbo, so it is kept long) while
// the several requests the page makes every few seconds share one run of the docker CLI.
var DefaultCacheTTL = CacheTTL{Status: 10 * time.Second, StatusDown: 1500 * time.Millisecond, Containers: 2 * time.Second, Saved: 20 * time.Second}

// CachedRuntime answers the read-only questions (is Docker up, what runs, what is saved) from a short-lived cache and
// lets one caller at a time ask docker, so a page that polls three endpoints does not start three docker processes per
// tick. Everything that changes something goes straight through and drops the cache first and last.
type CachedRuntime struct {
	inner ContainerRuntime
	ttl   CacheTTL
	now   func() time.Time

	genMu sync.Mutex
	gen   uint64

	status     cacheSlot[DockerStatusDto]
	containers cacheSlot[map[string]ContainerInfo]
	saved      cacheSlot[map[string]bool]
}

func NewCachedRuntime(inner ContainerRuntime, ttl CacheTTL) *CachedRuntime {
	return &CachedRuntime{inner: inner, ttl: ttl, now: time.Now}
}

// cacheSlot is one cached answer. Its mutex is held while the answer is fetched, so concurrent callers wait for the
// one fetch in flight instead of starting their own.
type cacheSlot[T any] struct {
	mu  sync.Mutex
	at  time.Time
	gen uint64
	val T
	has bool
}

func (c *CachedRuntime) generation() uint64 {
	c.genMu.Lock()
	defer c.genMu.Unlock()
	return c.gen
}

// Invalidate forgets every cached answer. A fetch that started before it is not stored when it finishes.
func (c *CachedRuntime) Invalidate() {
	c.genMu.Lock()
	c.gen++
	c.genMu.Unlock()
}

func cached[T any](c *CachedRuntime, s *cacheSlot[T], ttl func(T) time.Duration, fetch func() T, clone func(T) T) T {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen := c.generation()
	if s.has && s.gen == gen && c.now().Sub(s.at) < ttl(s.val) {
		return clone(s.val)
	}
	val := fetch()
	s.val, s.at, s.gen, s.has = val, c.now(), gen, true
	return clone(val)
}

// detached keeps a shared fetch going when the caller that happened to start it goes away: others are waiting for it.
func detached(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

func (c *CachedRuntime) Status(ctx context.Context) DockerStatusDto {
	return cached(c, &c.status,
		func(v DockerStatusDto) time.Duration {
			if v.Available {
				return c.ttl.Status
			}
			return c.ttl.StatusDown
		},
		func() DockerStatusDto { return c.inner.Status(detached(ctx)) },
		func(v DockerStatusDto) DockerStatusDto { return v })
}

func (c *CachedRuntime) Containers(ctx context.Context) map[string]ContainerInfo {
	return cached(c, &c.containers,
		func(map[string]ContainerInfo) time.Duration { return c.ttl.Containers },
		func() map[string]ContainerInfo { return c.inner.Containers(detached(ctx)) },
		func(m map[string]ContainerInfo) map[string]ContainerInfo {
			out := make(map[string]ContainerInfo, len(m))
			for k, v := range m {
				out[k] = v
			}
			return out
		})
}

func (c *CachedRuntime) SavedData(ctx context.Context) map[string]bool {
	return cached(c, &c.saved,
		func(map[string]bool) time.Duration { return c.ttl.Saved },
		func() map[string]bool { return c.inner.SavedData(detached(ctx)) },
		func(m map[string]bool) map[string]bool {
			out := make(map[string]bool, len(m))
			for k, v := range m {
				out[k] = v
			}
			return out
		})
}

// ---- what changes something: straight through, with the cache dropped before and after --------------------------------

func (c *CachedRuntime) Start(ctx context.Context, def ProviderDef, hostEnv map[string]string, persist bool) error {
	c.Invalidate()
	defer c.Invalidate()
	return c.inner.Start(ctx, def, hostEnv, persist)
}

func (c *CachedRuntime) Stop(ctx context.Context, def ProviderDef) error {
	c.Invalidate()
	defer c.Invalidate()
	return c.inner.Stop(ctx, def)
}

func (c *CachedRuntime) Remove(ctx context.Context, def ProviderDef) error {
	c.Invalidate()
	defer c.Invalidate()
	return c.inner.Remove(ctx, def)
}

func (c *CachedRuntime) DeleteData(ctx context.Context, def ProviderDef) error {
	c.Invalidate()
	defer c.Invalidate()
	return c.inner.DeleteData(ctx, def)
}

// Logs are never cached: they are what you look at to see what just happened.
func (c *CachedRuntime) Logs(ctx context.Context, def ProviderDef, tail int) (string, error) {
	return c.inner.Logs(ctx, def, tail)
}
