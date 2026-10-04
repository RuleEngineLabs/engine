package main

import (
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
)

// init applies Go runtime tuning for ultra-low latency before the server starts.
// All knobs are overridable via environment variables so they can be adjusted
// without a rebuild (e.g. in staging or cloud deployments).
//
// Defaults chosen for an in-memory, CPU-bound policy engine on a single node:
//   - GOGC=200   — heap can grow to 3× before GC fires; halves GC frequency
//                  vs. the Go default of 100, at the cost of ~2× peak memory.
//   - GOMEMLIMIT — hard cap that pairs with a high GOGC to avoid OOM.
//                  Default: 256 MiB; override with GOMEMLIMIT env var.
//   - GOMAXPROCS — default is runtime.NumCPU(). Override only when you need to
//                  pin the engine to a subset of cores (e.g. a cgroup limit).
func init() {
	applyGCTuning()
	applyMaxProcs()
}

func applyGCTuning() {
	// GOGC: how aggressively the GC runs.
	// If already set in the environment, honour it and don't override.
	if v := os.Getenv("GOGC"); v != "" {
		slog.Debug("GOGC set by environment", "value", v)
	} else {
		debug.SetGCPercent(200)
		slog.Debug("GOGC set by tuner", "value", 200)
	}

	// GOMEMLIMIT: ceiling that keeps GOGC=200 from consuming unbounded memory.
	// Parse the env var if present; otherwise default to 256 MiB.
	// Accepts bytes as an integer or the human-readable form handled by
	// debug.SetMemoryLimit (e.g. "256MiB") — we use the integer form for
	// portability (no external parser needed).
	const defaultLimitMiB = 256
	limitBytes := int64(defaultLimitMiB * 1024 * 1024)
	if v := os.Getenv("GOMEMLIMIT"); v != "" {
		// Let Go's runtime handle the env var natively — it reads GOMEMLIMIT
		// itself since Go 1.19. We only set it programmatically when absent.
		slog.Debug("GOMEMLIMIT set by environment", "value", v)
	} else {
		debug.SetMemoryLimit(limitBytes)
		slog.Debug("GOMEMLIMIT set by tuner", "mib", defaultLimitMiB)
	}
}

func applyMaxProcs() {
	if v := os.Getenv("GOMAXPROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
			slog.Debug("GOMAXPROCS set by environment", "value", n)
			return
		}
	}
	// Default: use all available CPUs (Go's own default). Explicit for clarity.
	n := runtime.NumCPU()
	runtime.GOMAXPROCS(n)
	slog.Debug("GOMAXPROCS set by tuner", "cpus", n)
}
