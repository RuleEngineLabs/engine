package cache_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RuleEngineLabs/engine/internal/cache"
	"github.com/RuleEngineLabs/engine/internal/compiler"
	"github.com/RuleEngineLabs/engine/internal/policy"
)

func makeArtifact(t *testing.T, id string) *compiler.Artifact {
	t.Helper()
	p := &policy.Policy{
		ID:    id,
		Entry: "s",
		States: []policy.State{
			{ID: "s", Kind: policy.KindExecution, Fallback: "end"},
			{ID: "end", Kind: policy.KindResponse, Status: 200},
		},
	}
	art, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return art
}

func TestArtifactCache_HitNoRecompile(t *testing.T) {
	c := cache.New(cache.DefaultMaxBytes)
	var compileCount atomic.Int32

	art := makeArtifact(t, "p1")
	compile := func() (*compiler.Artifact, error) {
		compileCount.Add(1)
		return art, nil
	}

	// First call: miss → compiles
	r1, err := c.GetOrCompile("p1:1", compile)
	if err != nil || r1 == nil {
		t.Fatalf("first GetOrCompile: %v", err)
	}
	if compileCount.Load() != 1 {
		t.Errorf("expected 1 compile call, got %d", compileCount.Load())
	}

	// Second call: hit → no compile
	r2, err := c.GetOrCompile("p1:1", compile)
	if err != nil || r2 == nil {
		t.Fatalf("second GetOrCompile: %v", err)
	}
	if compileCount.Load() != 1 {
		t.Errorf("expected still 1 compile call after cache hit, got %d", compileCount.Load())
	}
	if r1 != r2 {
		t.Error("expected same artifact pointer from cache")
	}
}

func TestArtifactCache_LRUEviction(t *testing.T) {
	// Tiny cache to force eviction: allow only ~1 artifact
	tinyCache := cache.New(200)

	art1 := makeArtifact(t, "tiny1")
	art2 := makeArtifact(t, "tiny2")

	tinyCache.GetOrCompile("k1", func() (*compiler.Artifact, error) { return art1, nil })
	tinyCache.GetOrCompile("k2", func() (*compiler.Artifact, error) { return art2, nil })

	// Cache should not exceed size — at most 1 entry given tiny limit
	if tinyCache.Len() > 1 {
		t.Errorf("expected LRU eviction to keep at most 1 entry, got %d", tinyCache.Len())
	}
}

func TestArtifactCache_Singleflight(t *testing.T) {
	c := cache.New(cache.DefaultMaxBytes)
	art := makeArtifact(t, "sf")
	var compileCount atomic.Int32

	var wg sync.WaitGroup
	errs := make([]error, 10)
	results := make([]*compiler.Artifact, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = c.GetOrCompile("sf:1", func() (*compiler.Artifact, error) {
				compileCount.Add(1)
				return art, nil
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
		if results[i] == nil {
			t.Errorf("goroutine %d: nil artifact", i)
		}
	}

	if n := compileCount.Load(); n != 1 {
		t.Errorf("singleflight: expected exactly 1 compile call, got %d", n)
	}

	// Sanity: all goroutines should get the same artifact
	for i := 1; i < 10; i++ {
		if results[i] != results[0] {
			t.Errorf("goroutine %d got different artifact than goroutine 0", i)
		}
	}
}

func TestArtifactCache_BlueGreen_Invalidation(t *testing.T) {
	c := cache.New(cache.DefaultMaxBytes)
	art1 := makeArtifact(t, "bg1")
	art2 := makeArtifact(t, "bg2")

	var compileCount atomic.Int32

	// Populate v1
	v1, _ := c.GetOrCompile("pol:1", func() (*compiler.Artifact, error) {
		compileCount.Add(1)
		return art1, nil
	})
	if v1 != art1 {
		t.Fatal("expected art1 from cache")
	}

	// Invalidate v1 (blue-green: new version published)
	c.Invalidate("pol:1")

	// Populate v2
	v2, _ := c.GetOrCompile("pol:2", func() (*compiler.Artifact, error) {
		compileCount.Add(1)
		return art2, nil
	})
	if v2 != art2 {
		t.Fatal("expected art2 for v2")
	}

	// v1 is gone from cache; new request for v1 recompiles
	var recompiled atomic.Int32
	c.GetOrCompile("pol:1", func() (*compiler.Artifact, error) {
		recompiled.Add(1)
		return art1, nil
	})
	if recompiled.Load() != 1 {
		t.Errorf("expected recompile after invalidation, got %d compiles", recompiled.Load())
	}

	// v2 is still in cache (0 additional compiles)
	prevCount := compileCount.Load()
	c.GetOrCompile("pol:2", func() (*compiler.Artifact, error) {
		compileCount.Add(1)
		return art2, nil
	})
	if compileCount.Load() != prevCount {
		t.Errorf("v2 should still be in cache; unexpected recompile")
	}
}
