# RuleEngine — Local Performance Baseline

**Date**: 2026-10-03  
**Go**: go1.27.0  
**OS/Arch**: windows/amd64  
**CPU cores**: 8 logical · GOMAXPROCS=8  
**Samples**: 50k calls · 500 latency batches · warmup 1k  

> **Purpose**: local baseline for cloud deployment comparison.
> When deploying, compare cloud p99 against these values to quantify integration overhead.

---

## 1. Latency Distribution (single-threaded, 2 k batch samples)

_Each sample is the average over 100 consecutive calls (batch timing) to overcome Windows QPC floor._

| Scenario | Mean | p50 | p90 | p99 | p99.9 | Max | StdDev |
|:---------|-----:|----:|----:|----:|------:|----:|-------:|
| TwoState / nil input                         | 1.357µs | 0s | 6.548µs | 10.166µs | 29.906µs | 29.906µs | 3.323µs |
| CreditEval / approved (1st branch)           | 3.126µs | 0s | 9.989µs | 19.997µs | 30.022µs | 30.022µs | 4.342µs |
| CreditEval / rejected (last branch)          | 3.226µs | 0s | 10.009µs | 18.114µs | 30.287µs | 30.287µs | 4.61µs |
| DeepChain / 5 states                         | 2.375µs | 0s | 5.87µs | 12.629µs | 17.59µs | 17.59µs | 3.318µs |
| CreditEval / with ISO dates                  | 6.018µs | 5.658µs | 10.229µs | 20.137µs | 35.367µs | 35.367µs | 5.849µs |

## 2. Tail Latency Amplification

p99/p50 ratio: how much worse the 99th percentile is vs the median.
Values < 3× indicate consistent execution; > 10× suggests GC pauses or OS scheduling jitter.

| Scenario | p50 | p99 | p99/p50 | p99.9/p50 |
|:---------|----:|----:|--------:|----------:|
| TwoState / nil input                         | 0s | 10.166µs | +Infx | +Infx |
| CreditEval / approved (1st branch)           | 0s | 19.997µs | +Infx | +Infx |
| CreditEval / rejected (last branch)          | 0s | 18.114µs | +Infx | +Infx |
| DeepChain / 5 states                         | 0s | 12.629µs | +Infx | +Infx |
| CreditEval / with ISO dates                  | 5.658µs | 20.137µs | 3.6x | 6.3x |

## 3. Throughput (RPS)

### Single-threaded

| Scenario | RPS | Latency budget used |
|:---------|----:|--------------------:|
| TwoState / nil input                         | 736.4k/s | — |
| CreditEval / approved (1st branch)           | 319.8k/s | — |
| CreditEval / rejected (last branch)          | 310.0k/s | — |
| DeepChain / 5 states                         | 420.9k/s | — |
| CreditEval / with ISO dates                  | 166.2k/s | — |

### Parallel (8 workers · 3s window)

| Scenario | RPS (peak) | Scale vs single | Efficiency |
|:---------|----------:|----------------:|-----------:|
| TwoState / nil input                         | 1.40M/s | 1.9x | 24% |
| CreditEval / approved (1st branch)           | 455.2k/s | 1.4x | 18% |
| CreditEval / rejected (last branch)          | 524.3k/s | 1.7x | 21% |
| DeepChain / 5 states                         | 643.8k/s | 1.5x | 19% |
| CreditEval / with ISO dates                  | 202.8k/s | 1.2x | 15% |

> **Efficiency** = scale / GOMAXPROCS × 100. 100% means perfect linear scaling. Lower values indicate lock contention or shared-state bottlenecks.

## 4. Memory per Call

Measured as (TotalAlloc after − before) / N. Includes all heap allocations inside `Execute()`.

| Scenario | Bytes/call | Allocs/call | GC cycles (200k calls) |
|:---------|----------:|------------:|-----------------------:|
| TwoState / nil input                         |  416.3 B |    4.0 |      5 |
| CreditEval / approved (1st branch)           |  816.1 B |   10.0 |     13 |
| CreditEval / rejected (last branch)          |  880.1 B |   12.0 |     15 |
| DeepChain / 5 states                         |  848.1 B |    9.0 |     15 |
| CreditEval / with ISO dates                  | 1380.4 B |   17.8 |     21 |

## 5. Cloud Comparison Targets

Use these thresholds when profiling the deployed service.
The "integration overhead" is the cost added by HTTP transport, serialisation, and cold starts.

| Scenario | Local p99 | Warm cloud budget (×3) | Cold start budget (×20) |
|:---------|----------:|----------------------:|------------------------:|
| TwoState / nil input                         | 10.166µs | 30.498µs | 203.32µs |
| CreditEval / approved (1st branch)           | 19.997µs | 59.991µs | 399.94µs |
| CreditEval / rejected (last branch)          | 18.114µs | 54.342µs | 362.28µs |
| DeepChain / 5 states                         | 12.629µs | 37.887µs | 252.58µs |
| CreditEval / with ISO dates                  | 20.137µs | 60.411µs | 402.74µs |

> **Rule**: if cloud (warm) p99 > local p99 × 3, investigate HTTP/JSON serialisation overhead.

## 6. Environment

```
Go:         go1.27.0
OS/Arch:    windows/amd64
CPU cores:  8 logical
GOMAXPROCS: 8
N samples:  50k per scenario
Warmup:     1k iterations
RPS test:   3s window · 8 goroutines
```

