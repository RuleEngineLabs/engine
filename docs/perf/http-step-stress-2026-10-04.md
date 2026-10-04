# RuleEngine — HTTP Step Stress Test

**Date**: 2026-10-04  
**Go**: go1.27.0  
**OS/Arch**: windows/amd64  
**CPU cores**: 8 logical · GOMAXPROCS=1  
**Server**: `httptest.NewServer` (in-process, loopback TCP)  
**Endpoint**: `POST /execute/{id}` · credit-eval (3 branches)  
**Step duration**: 6s · p99 SLO: 200ms  

## Summary

| | Value |
|---|---|
| **Optimal point** (p99 ≤ 200ms) | **137/s RPS @ 10 workers** |
| p50 at optimal | 68.243ms |
| p99 at optimal | 118.8203ms |
| Sustainable ceiling | 172/s @ 100 workers |
| p99 at ceiling | 1.9598949s |
| Break point | **150 workers** (err 2.35%) |

## Step Results

| Workers | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs | Note |
|--------:|----:|-----:|----:|----:|----:|------:|----:|-----:|:----:|
| 1 | 35/s | 0.000% | 28.3575ms | 34.4816ms | 55.0815ms | 60.7814ms | 60.7814ms | 208 |  |
| 5 | 115/s | 0.000% | 40.7329ms | 52.2576ms | 73.5634ms | 115.5881ms | 115.5881ms | 691 |  |
| 10 | 137/s | 0.000% | 68.243ms | 93.2111ms | 118.8203ms | 262.2859ms | 262.2859ms | 824 | ⭐ optimal |
| 15 | 139/s | 0.000% | 97.0873ms | 142.9541ms | 207.942ms | 239.4608ms | 239.4608ms | 836 |  |
| 20 | 142/s | 0.000% | 129.5312ms | 178.6195ms | 233.3295ms | 274.3921ms | 274.3921ms | 853 |  |
| 30 | 156/s | 0.000% | 178.6566ms | 236.6401ms | 317.2244ms | 360.2577ms | 360.2577ms | 933 |  |
| 50 | 150/s | 0.000% | 292.1356ms | 392.5591ms | 590.4005ms | 1.057078s | 1.057078s | 903 |  |
| 75 | 155/s | 0.000% | 417.804ms | 518.0865ms | 1.5179518s | 2.4255468s | 2.4255468s | 929 |  |
| 100 | 172/s | 0.000% | 513.5922ms | 660.5692ms | 1.9598949s | 2.5026488s | 2.6126364s | 1031 | 🔝 ceiling |
| 150 | 170/s | 2.353% | 720.4175ms | 881.7086ms | 5.4259079s | 5.7924376s | 5.7984604s | 1020 | ❌ |

## vs Executor Baseline (no HTTP)

| Layer | p99 (c=1) | RPS |
|:------|----------:|----:|
| Pure executor (Go calls) | ~20µs | ~455k/s |
| HTTP + JSON (this test, c=1) | 55.0815ms | 35/s |

> HTTP/JSON overhead per request ≈ **55.0615ms** (c=1 p99 − 20µs executor baseline).

## Environment

```
Go:         go1.27.0
OS/Arch:    windows/amd64
CPU cores:  8 logical
GOMAXPROCS: 1
Step dur:   6s
Steps:      [1 5 10 15 20 30 50 75 100 150 200 300 500]
```
