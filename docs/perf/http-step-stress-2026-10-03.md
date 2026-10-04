# RuleEngine — HTTP Step Stress Test

**Date**: 2026-10-03  
**Go**: go1.27.0  
**OS/Arch**: windows/amd64  
**CPU cores**: 8 logical · GOMAXPROCS=8  
**Server**: `httptest.NewServer` (in-process, loopback TCP)  
**Endpoint**: `POST /execute/{id}` · credit-eval (3 branches)  
**Step duration**: 8s  
**Abort threshold**: error_rate > 1%  

## Summary

| | Value |
|---|---|
| Sustainable ceiling | **238/s** @ 250 workers |
| p99 at ceiling      | **2.124697s** |
| p99.9 at ceiling    | 2.2184034s |
| Break point         | **500 workers** (err 11.14%) |

## Step Results

| Workers | RPS | Err% | p50 | p90 | p99 | p99.9 | max | Reqs | Status |
|--------:|----:|-----:|----:|----:|----:|------:|----:|-----:|:------:|
| 1 | 42/s | 0.000% | 20.2288ms | 27.1057ms | 93.5562ms | 178.5847ms | 178.5847ms | 340 | ✅ |
| 10 | 203/s | 0.000% | 44.6684ms | 65.8818ms | 114.5324ms | 682.2075ms | 791.173ms | 1625 | ✅ |
| 50 | 230/s | 0.000% | 212.4393ms | 270.0612ms | 382.4226ms | 619.3371ms | 680.3669ms | 1843 | ✅ |
| 100 | 218/s | 0.000% | 459.0567ms | 592.8097ms | 726.4498ms | 822.915ms | 851.1841ms | 1746 | ✅ |
| 250 | 238/s | 0.000% | 1.1572526s | 1.2918292s | 2.124697s | 2.2184034s | 2.4879907s | 1905 | ✅ |
| 500 | 223/s | 11.136% | 1.5502777s | 7.3226789s | 7.9112585s | 7.9446157s | 7.9986361s | 1787 | ❌ |

## vs Executor Baseline (no HTTP)

| Layer | p99 | RPS (parallel) |
|:------|----:|---------------:|
| Pure executor (Go calls) | ~20µs | ~455k/s |
| HTTP + JSON (this test) | 2.124697s | 238/s |

> HTTP/JSON overhead ≈ **2.124677s** per request (p99 delta).

## Environment

```
Go:         go1.27.0
OS/Arch:    windows/amd64
CPU cores:  8 logical
GOMAXPROCS: 8
Step dur:   8s
Steps:      [1 10 50 100 250 500 1000]
```
