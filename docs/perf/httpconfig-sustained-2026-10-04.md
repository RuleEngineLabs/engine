# RuleEngine — HTTPConfig Sustained Load Test

**Date**: 2026-10-04  
**Go**: go1.27.0  
**OS/Arch**: windows/amd64  
**CPU cores**: 8 logical · GOMAXPROCS=8  
**Server**: `httptest.NewServer` (in-process, loopback TCP)  
**Duration per scenario**: 6m0s · **Concurrency**: 10 workers  
**Scenarios**: 5 sequential · Total duration: 30m0s  

## Scenario Results

| Scenario | Feature | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs |
|:---------|:--------|----:|-----:|----:|----:|----:|------:|----:|-----:|
| `1-baseline-post` | no HTTPConfig (control) | 177/s | 0.000% | 50.9142ms | 74.8371ms | 156.9561ms | 473.6117ms | 2.0320845s | 63844 |
| `2-meta-idle` | TimeoutMs=5000 (never fires) — GetMeta overhead | 186/s | 0.000% | 48.6957ms | 68.9455ms | 147.7208ms | 392.3283ms | 2.977589s | 66852 |
| `3-cache-get` | CacheMaxAgeSeconds=300 (GET) | 156/s | 0.114% | 36.6806ms | 70.2582ms | 169.1161ms | 10.0096605s | 50.1797104s | 56063 |
| `4-retry-after-429` | RetryOn=[429] RetryAfterSeconds=5 | 256/s | 0.000% | 35.4228ms | 52.9052ms | 108.0038ms | 281.0631ms | 1.7254978s | 92059 |
| `5-timeout-circuit` | TimeoutMs=30ms, jitter server every-3rd=50ms (≈33% cut) | 106/s | 0.000% | 85.6008ms | 124.2947ms | 261.9645ms | 547.3917ms | 1.4528084s | 38053 |

## Overhead vs Baseline

| Scenario | Δ RPS | Δ p50 | Δ p99 | Δ Err% |
|:---------|------:|------:|------:|-------:|
| `2-meta-idle` | +8/s | -2.2185ms | -9.2353ms | +0.000% |
| `3-cache-get` | -22/s | -14.2336ms | 12.16ms | +0.114% |
| `4-retry-after-429` | +78/s | -15.4914ms | -48.9523ms | +0.000% |
| `5-timeout-circuit` | -72/s | 34.6866ms | 105.0084ms | +0.000% |

## Scenario Notes

- **2-meta-idle**: measures `GetMeta` lookup cost + `TimeoutMs` range check on every request (timeout never fires at 5000ms)
- **3-cache-get**: measures Cache-Control header write path on GET; header is set but not consumed by any cache in this in-process test
- **4-retry-after-429**: policy always returns 429; `Retry-After: 5` header is always written; 429 counted as success (expected status)
- **5-timeout-circuit**: jitter server sleeps 50ms every 3rd request; `TimeoutMs=30ms` cuts those → ~33% errors from circuit-breaker; latency bimodal (fast ~5ms, cut ~30ms)

## Environment

```
Go:         go1.27.0
OS/Arch:    windows/amd64
CPU cores:  8 logical
GOMAXPROCS: 8
Scenario dur: 6m0s
Concurrency:  10 workers
```
