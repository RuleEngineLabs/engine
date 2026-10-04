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
| **Optimal point** (p99 ≤ 200ms) | **27/s RPS @ 1 workers** |
| p50 at optimal | 34.6007ms |
| p99 at optimal | 66.56ms |
| Sustainable ceiling | 190/s @ 300 workers |
| p99 at ceiling | 3.0875032s |
| Break point | **500 workers** (err 6.93%) |

## Step Results

| Workers | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs | Note |
|--------:|----:|-----:|----:|----:|----:|------:|----:|-----:|:----:|
| 1 | 27/s | 0.000% | 34.6007ms | 45.2672ms | 66.56ms | 72.9138ms | 72.9138ms | 163 | ⭐ optimal |
| 5 | 55/s | 0.000% | 83.421ms | 133.8446ms | 233.5745ms | 288.0106ms | 288.0106ms | 328 |  |
| 10 | 81/s | 0.000% | 111.6914ms | 170.8755ms | 274.0911ms | 367.5321ms | 367.5321ms | 484 |  |
| 15 | 118/s | 0.000% | 106.5512ms | 189.9861ms | 302.9736ms | 466.8808ms | 466.8808ms | 707 |  |
| 20 | 87/s | 0.000% | 202.1044ms | 371.52ms | 474.6748ms | 586.8952ms | 586.8952ms | 522 |  |
| 30 | 106/s | 0.000% | 298.0438ms | 382.5998ms | 452.0342ms | 490.415ms | 490.415ms | 637 |  |
| 50 | 126/s | 0.000% | 393.7882ms | 585.5495ms | 962.1406ms | 1.2536026s | 1.2536026s | 758 |  |
| 75 | 106/s | 0.000% | 709.7707ms | 1.0212121s | 2.0470731s | 2.4907428s | 2.4907428s | 633 |  |
| 100 | 115/s | 0.000% | 768.7747ms | 1.3107943s | 1.6052337s | 1.930826s | 1.930826s | 691 |  |
| 150 | 118/s | 0.000% | 1.3096498s | 1.5168119s | 2.4105686s | 2.5677177s | 2.5677177s | 708 |  |
| 200 | 112/s | 0.000% | 1.7878918s | 2.4952277s | 2.8727231s | 3.1813154s | 3.1813154s | 672 |  |
| 300 | 190/s | 0.000% | 1.6109592s | 1.7916506s | 3.0875032s | 3.2519669s | 3.3999637s | 1143 | 🔝 ceiling |
| 500 | 159/s | 6.925% | 3.3349506s | 4.8795518s | 5.5025028s | 5.6741174s | 5.6741174s | 953 | ❌ |

## vs Executor Baseline (no HTTP)

| Layer | p99 (c=1) | RPS |
|:------|----------:|----:|
| Pure executor (Go calls) | ~20µs | ~455k/s |
| HTTP + JSON (this test, c=1) | 66.56ms | 27/s |

> HTTP/JSON overhead per request ≈ **66.54ms** (c=1 p99 − 20µs executor baseline).

## Environment

```
Go:         go1.27.0
OS/Arch:    windows/amd64
CPU cores:  8 logical
GOMAXPROCS: 1
Step dur:   6s
Steps:      [1 5 10 15 20 30 50 75 100 150 200 300 500]
```
