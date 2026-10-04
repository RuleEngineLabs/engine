package main

import (
	"runtime"
	"strconv"
	"testing"
)

func TestApplyGCTuning_EnvVarsHonoured(t *testing.T) {
	// When GOGC/GOMEMLIMIT are set, applyGCTuning should log and leave them as-is.
	t.Setenv("GOGC", "100")
	t.Setenv("GOMEMLIMIT", "512MiB")
	applyGCTuning() // exercises the "env var already set" branches
}

func TestApplyMaxProcs_EnvVarOverride(t *testing.T) {
	// Use NumCPU so the side-effect is a no-op on the test runner.
	t.Setenv("GOMAXPROCS", strconv.Itoa(runtime.NumCPU()))
	applyMaxProcs()
}
