# RuleEngine — Branch Latency Breakdown

**Date**: 2026-10-04 · **c=10** · **duration**: 6s  

| Branch | Conditions | RPS | p50 | p99 | p99.9 | max |
|:-------|----------:|----:|----:|----:|------:|----:|
| approved (depth=1) | 1 | 140/s | 65.3831ms | 142.5155ms | 888.4347ms | 888.4347ms |
| manual   (depth=2) | 2 | 133/s | 67.1081ms | 156.3831ms | 211.6595ms | 211.6595ms |
| rejected (depth=3) | 3 | 145/s | 63.4116ms | 130.0261ms | 369.7096ms | 369.7096ms |

> Δp99 (rejected − approved) = **-12.4894ms**
