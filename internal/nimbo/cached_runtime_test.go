package nimbo

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingRuntime counts how often docker would have been asked.
type countingRuntime struct {
	statusCalls, containerCalls, savedCalls atomic.Int32
	up                                      atomic.Bool
	running                                 atomic.Bool
	delay                                   time.Duration
	started                                 atomic.Int32
}

func (r *countingRuntime) Status(context.Context) DockerStatusDto {
	r.statusCalls.Add(1)
	time.Sleep(r.delay)
	if r.up.Load() {
		return DockerStatusDto{Available: true, Version: coreStr("29.0")}
	}
	return DockerStatusDto{Available: false, Error: coreStr("Docker daemon not reachable")}
}

func (r *countingRuntime) Containers(context.Context) map[string]ContainerInfo {
	r.containerCalls.Add(1)
	time.Sleep(r.delay)
	out := map[string]ContainerInfo{}
	if r.running.Load() {
		out["redis"] = ContainerInfo{ProviderID: "redis", Running: true, Status: "Up"}
	}
	return out
}

func (r *countingRuntime) SavedData(context.Context) map[string]bool {
	r.savedCalls.Add(1)
	return map[string]bool{"redis": true}
}

func (r *countingRuntime) Start(context.Context, ProviderDef, map[string]string, bool) error {
	r.started.Add(1)
	r.running.Store(true)
	return nil
}
func (r *countingRuntime) Stop(context.Context, ProviderDef) error {
	r.running.Store(false)
	return nil
}
func (r *countingRuntime) Remove(context.Context, ProviderDef) error {
	r.running.Store(false)
	return nil
}
func (r *countingRuntime) Logs(context.Context, ProviderDef, int) (string, error) {
	return "logs", nil
}
func (r *countingRuntime) DeleteData(context.Context, ProviderDef) error { return nil }

func cachedFor(inner *countingRuntime, now *time.Time) *CachedRuntime {
	c := NewCachedRuntime(inner, CacheTTL{Status: 5 * time.Second, StatusDown: time.Second, Containers: 1500 * time.Millisecond, Saved: 3 * time.Second})
	c.now = func() time.Time { return *now }
	return c
}

func TestRepeatedQuestionsShareOneRunOfDocker(t *testing.T) {
	inner := &countingRuntime{}
	inner.up.Store(true)
	now := time.Unix(1000, 0)
	c := cachedFor(inner, &now)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		c.Status(ctx)
		c.Containers(ctx)
		c.SavedData(ctx)
		now = now.Add(50 * time.Millisecond)
	}
	coreEq(t, int32(1), inner.statusCalls.Load())
	coreEq(t, int32(1), inner.containerCalls.Load())
	coreEq(t, int32(1), inner.savedCalls.Load())
}

func TestTheCacheExpiresOnItsOwnTimeToLive(t *testing.T) {
	inner := &countingRuntime{}
	inner.up.Store(true)
	now := time.Unix(1000, 0)
	c := cachedFor(inner, &now)
	ctx := context.Background()
	c.Containers(ctx)
	now = now.Add(1400 * time.Millisecond)
	c.Containers(ctx)
	coreEq(t, int32(1), inner.containerCalls.Load(), "still fresh")
	now = now.Add(200 * time.Millisecond)
	c.Containers(ctx)
	coreEq(t, int32(2), inner.containerCalls.Load(), "expired")
}

func TestDockerDownIsBelievedForLessTimeSoStartingItIsNoticed(t *testing.T) {
	inner := &countingRuntime{}
	now := time.Unix(1000, 0)
	c := cachedFor(inner, &now)
	ctx := context.Background()
	coreTrue(t, !c.Status(ctx).Available)
	inner.up.Store(true)
	now = now.Add(500 * time.Millisecond)
	coreTrue(t, !c.Status(ctx).Available, "inside the short window it is still the old answer")
	now = now.Add(600 * time.Millisecond)
	coreTrue(t, c.Status(ctx).Available, "after a second Docker coming up is seen")
	// and once up, it is believed for the long time
	now = now.Add(3 * time.Second)
	c.Status(ctx)
	coreEq(t, int32(2), inner.statusCalls.Load())
}

func TestEveryChangeDropsTheCacheSoWhatYouDidShowsAtOnce(t *testing.T) {
	inner := &countingRuntime{}
	inner.up.Store(true)
	now := time.Unix(1000, 0)
	c := cachedFor(inner, &now)
	ctx := context.Background()
	coreEq(t, 0, len(c.Containers(ctx)))
	coreTrue(t, c.Start(ctx, *CatalogFind("redis"), nil, false) == nil)
	coreEq(t, 1, len(c.Containers(ctx)), "no waiting for the time to live after a start")
	coreTrue(t, c.Stop(ctx, *CatalogFind("redis")) == nil)
	coreEq(t, 0, len(c.Containers(ctx)))
	coreTrue(t, c.Start(ctx, *CatalogFind("redis"), nil, false) == nil)
	coreTrue(t, c.Remove(ctx, *CatalogFind("redis")) == nil)
	coreEq(t, 0, len(c.Containers(ctx)))
	c.SavedData(ctx)
	coreTrue(t, c.DeleteData(ctx, *CatalogFind("redis")) == nil)
	c.SavedData(ctx)
	coreEq(t, int32(2), inner.savedCalls.Load(), "deleting data refreshes the list of saved data")
}

func TestManyCallersAtOnceStartOneDockerProcess(t *testing.T) {
	inner := &countingRuntime{delay: 80 * time.Millisecond}
	inner.up.Store(true)
	c := NewCachedRuntime(inner, DefaultCacheTTL)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Containers(context.Background())
			c.Status(context.Background())
		}()
	}
	wg.Wait()
	coreEq(t, int32(1), inner.containerCalls.Load())
	coreEq(t, int32(1), inner.statusCalls.Load())
}

func TestAChangeWhileAnAnswerIsBeingFetchedDoesNotLeaveAStaleAnswer(t *testing.T) {
	inner := &countingRuntime{delay: 120 * time.Millisecond}
	inner.up.Store(true)
	c := NewCachedRuntime(inner, DefaultCacheTTL)
	ctx := context.Background()
	done := make(chan struct{})
	go func() { c.Containers(ctx); close(done) }() // reads "nothing runs" slowly
	time.Sleep(30 * time.Millisecond)
	coreTrue(t, c.Start(ctx, *CatalogFind("redis"), nil, false) == nil) // changes the world meanwhile
	<-done
	coreEq(t, 1, len(c.Containers(ctx)), "the slow old answer was not kept")
}

func TestCallersGetCopiesTheyCanChange(t *testing.T) {
	inner := &countingRuntime{}
	inner.up.Store(true)
	inner.running.Store(true)
	c := NewCachedRuntime(inner, DefaultCacheTTL)
	a := c.Containers(context.Background())
	delete(a, "redis")
	coreEq(t, 1, len(c.Containers(context.Background())))
	s := c.SavedData(context.Background())
	s["x"] = true
	coreEq(t, 1, len(c.SavedData(context.Background())))
}

func TestACallerThatGivesUpDoesNotBreakTheSharedAnswer(t *testing.T) {
	inner := &countingRuntime{delay: 60 * time.Millisecond}
	inner.up.Store(true)
	inner.running.Store(true)
	c := NewCachedRuntime(inner, DefaultCacheTTL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	c.Containers(ctx)
	coreEq(t, 1, len(c.Containers(context.Background())))
	coreEq(t, int32(1), inner.containerCalls.Load())
}

func TestLogsAreNeverCached(t *testing.T) {
	inner := &countingRuntime{}
	c := NewCachedRuntime(inner, DefaultCacheTTL)
	out, err := c.Logs(context.Background(), *CatalogFind("redis"), 10)
	coreTrue(t, err == nil && out == "logs")
}
