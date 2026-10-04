# RuleEngine — Branch Latency Breakdown

**Date**: 2026-10-04 · **c=10** · **duration**: 6s  

| Branch | Conditions | RPS | p50 | p99 | p99.9 | max |
|:-------|----------:|----:|----:|----:|------:|----:|
| approved (depth=1) | 1 | 137/s | 61.6877ms | 176.3304ms | 199.5854ms | 199.5854ms |
| manual   (depth=2) | 2 | 174/s | 53.8106ms | 89.0768ms | 103.0028ms | 669.1173ms |
| rejected (depth=3) | 3 | 138/s | 59.995ms | 189.1676ms | 524.9191ms | 524.9191ms |

> Δp99 (rejected − approved) = **12.8372ms**
