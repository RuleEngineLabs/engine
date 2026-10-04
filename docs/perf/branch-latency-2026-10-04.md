# RuleEngine — Branch Latency Breakdown

**Date**: 2026-10-04 · **c=10** · **duration**: 6s  

| Branch | Conditions | RPS | p50 | p99 | p99.9 | max |
|:-------|----------:|----:|----:|----:|------:|----:|
| approved (depth=1) | 1 | 183/s | 52.2082ms | 99.1951ms | 140.6521ms | 145.2602ms |
| manual   (depth=2) | 2 | 185/s | 51.8962ms | 97.9951ms | 116.6946ms | 127.1026ms |
| rejected (depth=3) | 3 | 197/s | 48.5545ms | 86.3795ms | 102.719ms | 106.0441ms |

> Δp99 (rejected − approved) = **-12.8156ms**
