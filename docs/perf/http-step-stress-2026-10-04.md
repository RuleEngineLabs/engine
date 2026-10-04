# RuleEngine — HTTP Step Stress Test

**Date**: 2026-10-04  
**Go**: go1.27.0  
**OS/Arch**: windows/amd64  
**CPU cores**: 8 logical · GOMAXPROCS=8  
**Server**: `httptest.NewServer` (in-process, loopback TCP)  
**Endpoint**: `POST /execute/{id}` · credit-eval (3 branches)  
**Step duration**: 6s · p99 SLO: 200ms  

## Summary

| | Value |
|---|---|
| **Optimal point** (p99 ≤ 200ms) | **220/s RPS @ 15 workers** |
| p50 at optimal | 64.4895ms |
| p99 at optimal | 140.6197ms |
| Sustainable ceiling | 226/s @ 300 workers |
| p99 at ceiling | 2.1238661s |
| Break point | **500 workers** (err 3.61%) |

## Step Results

| Workers | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs | Note |
|--------:|----:|-----:|----:|----:|----:|------:|----:|-----:|:----:|
| 1 | 62/s | 0.000% | 15.2211ms | 21.3353ms | 23.5423ms | 24.6206ms | 24.6206ms | 372 |  |
| 5 | 196/s | 0.000% | 25.0481ms | 28.4839ms | 31.4924ms | 34.5857ms | 36.4244ms | 1177 |  |
| 10 | 225/s | 0.000% | 42.976ms | 57.0832ms | 74.4337ms | 106.1094ms | 114.3377ms | 1350 |  |
| 15 | 220/s | 0.000% | 64.4895ms | 94.6543ms | 140.6197ms | 347.8066ms | 366.2411ms | 1318 | ⭐ optimal |
| 20 | 209/s | 0.000% | 88.6225ms | 130.5551ms | 266.7306ms | 542.609ms | 766.5897ms | 1252 |  |
| 30 | 223/s | 0.000% | 131.2535ms | 171.8225ms | 220.1342ms | 266.7634ms | 468.455ms | 1340 |  |
| 50 | 242/s | 0.000% | 205.7298ms | 250.72ms | 314.5978ms | 419.1267ms | 660.0352ms | 1453 |  |
| 75 | 252/s | 0.000% | 301.0433ms | 352.4565ms | 437.8115ms | 506.1767ms | 1.1648472s | 1511 |  |
| 100 | 199/s | 0.000% | 457.5921ms | 787.9614ms | 1.2271891s | 1.295851s | 1.5038272s | 1194 |  |
| 150 | 236/s | 0.000% | 661.931ms | 781.2197ms | 971.7283ms | 1.7145511s | 1.8971692s | 1413 |  |
| 200 | 249/s | 0.000% | 865.3836ms | 1.0218407s | 1.3288079s | 1.4146106s | 1.7617237s | 1496 |  |
| 300 | 226/s | 0.000% | 1.3602543s | 1.9144921s | 2.1238661s | 2.3777842s | 2.4617859s | 1359 | 🔝 ceiling |
| 500 | 282/s | 3.607% | 1.4004751s | 4.2866406s | 6.148127s | 6.729604s | 6.7832261s | 1691 | ❌ |

## vs Executor Baseline (no HTTP)

| Layer | p99 (c=1) | RPS |
|:------|----------:|----:|
| Pure executor (Go calls) | ~20µs | ~455k/s |
| HTTP + JSON (this test, c=1) | 23.5423ms | 62/s |

> HTTP/JSON overhead per request ≈ **23.5223ms** (c=1 p99 − 20µs executor baseline).

## Environment

```
Go:         go1.27.0
OS/Arch:    windows/amd64
CPU cores:  8 logical
GOMAXPROCS: 8
Step dur:   6s
Steps:      [1 5 10 15 20 30 50 75 100 150 200 300 500]
```
