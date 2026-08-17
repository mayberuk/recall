# recall benchmark results

AMD Ryzen 7 5700X3D 8-Core Processor · 16 cores · linux/amd64 · go1.25.4  
measured 2026-08-17T17:47:49Z

Every number here comes from a corpus generated from a seed (`internal/corpusgen`),
never from a session store, so a run on another machine measures the same bytes.
Small is about 5 MB and Medium about 50 MB of transcript.

## The corpus these numbers came from

| corpus | files | on disk | turns | conversation | invocation | result |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| small | 17 | 5.1 MB | 2,972 | 594 turns / 0.6 MB | 1,189 turns / 0.3 MB | 1,189 turns / 2.9 MB |
| medium | 56 | 51.4 MB | 29,564 | 5,898 turns / 6.0 MB | 11,833 turns / 3.1 MB | 11,833 turns / 28.5 MB |

The generator reproduces the tier shape of a working session store: conversation, invocation and result hold 20.0% / 40.5% / 39.5% of the turns and 16.1% / 8.4% / 75.5% of the bytes in the store internal/corpusgen measured, and every corpus above lands within 2% of each of those six shares.
Tool output is most of a real store and none of what recall searches by default,
so an all-tier number below costs several times its conversation-tier neighbour,
as it does on a real machine.

The generator is denser than a real store, though: about 72% of this corpus's
on-disk bytes strip into text, where a real store measured at 1,402 MB of JSONL
yielded only 244 MB, roughly 17%. The tier ratio above still holds, so the
all-tier-versus-conversation-tier comparison is sound — but an absolute figure
like a cold-strip time does not transfer directly to a real machine, because
50 MB of this corpus holds several times the searchable text that 50 MB of a
real store does.

## Micro benchmarks

| benchmark | corpus | ns/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| `BenchmarkArchive/medium/cold` | medium | 184,522,870 | 484,460,856 | 634,059 |
| `BenchmarkArchive/medium/warm` | medium | 468,363 | 169,441 | 889 |
| `BenchmarkArchive/small/cold` | small | 24,545,152 | 58,331,183 | 64,400 |
| `BenchmarkArchive/small/warm` | small | 250,183 | 65,719 | 436 |
| `BenchmarkLoad/medium/all-tiers` | medium | 5,089,920 | 50,768,414 | 95 |
| `BenchmarkLoad/medium/conversation` | medium | 1,157,816 | 8,571,725 | 30 |
| `BenchmarkLoad/small/all-tiers` | small | 1,218,554 | 5,107,765 | 57 |
| `BenchmarkLoad/small/conversation` | small | 272,415 | 861,179 | 17 |
| `BenchmarkRank/medium/collapse` | medium | 3,335,265 | 6,776,064 | 66 |
| `BenchmarkRank/medium/score` | medium | 13,888,000 | 23,167,416 | 1,813 |
| `BenchmarkRank/small/collapse` | small | 340,201 | 745,633 | 10 |
| `BenchmarkRank/small/score` | small | 1,381,277 | 2,280,952 | 460 |
| `BenchmarkSearch/medium/conjunction` | medium | 3,118,230 | 28,401,484 | 526 |
| `BenchmarkSearch/medium/miss` | medium | 2,351,503 | 114,342 | 378 |
| `BenchmarkSearch/medium/phrase` | medium | 498,625 | 210,961 | 345 |
| `BenchmarkSearch/medium/relaxed` | medium | 369,648 | 47,004 | 273 |
| `BenchmarkSearch/medium/single-common-term` | medium | 1,558,231 | 13,386,254 | 476 |
| `BenchmarkSearch/medium/single-term` | medium | 350,064 | 46,088 | 261 |
| `BenchmarkSearch/medium/single-term-all-tiers` | medium | 855,941 | 75,665 | 274 |
| `BenchmarkSearch/small/conjunction` | small | 754,334 | 1,422,309 | 67 |
| `BenchmarkSearch/small/miss` | small | 1,305,773 | 9,312 | 55 |
| `BenchmarkSearch/small/phrase` | small | 250,323 | 16,200 | 48 |
| `BenchmarkSearch/small/relaxed` | small | 136,769 | 4,976 | 53 |
| `BenchmarkSearch/small/single-common-term` | small | 409,083 | 610,528 | 56 |
| `BenchmarkSearch/small/single-term` | small | 127,476 | 4,296 | 43 |
| `BenchmarkSearch/small/single-term-all-tiers` | small | 459,885 | 7,016 | 44 |
| `BenchmarkStrip/medium/cold` | medium | 204,458,543 | 125,512,808 | 424,801 |
| `BenchmarkStrip/medium/incremental` | medium | 10,863,335 | 59,360,324 | 4,229 |
| `BenchmarkStrip/small/cold` | small | 21,283,028 | 24,455,329 | 42,560 |
| `BenchmarkStrip/small/incremental` | small | 2,646,019 | 17,881,121 | 297 |
| `BenchmarkStripRecord/medium` | medium | 6,221 | 2,848 | 18 |
| `BenchmarkStripRecord/small` | small | 6,184 | 2,832 | 18 |

## Scenarios

The built binary, invoked end to end. Allocation figures belong to the micro
table above: a scenario measures another process, where the comparable costs
are wall clock, the size of the answer, and the memory the process reached.

| scenario | corpus | wall clock | output bytes | peak RSS |
| --- | --- | ---: | ---: | ---: |
| `recall find bare` | small | 2.9 ms | 438 | 120.7 MB |
| `recall find --all` | small | 2.8 ms | 440 | 120.7 MB |
| `recall find --results` | small | 5.3 ms | 404 | 120.7 MB |
| `recall find --tools` | small | 3.5 ms | 448 | 120.7 MB |
| `recall find --brief` | small | 3.0 ms | 338 | 120.7 MB |
| `recall find --json` | small | 3.0 ms | 1,375 | 120.7 MB |
| `recall find --format jsonl` | small | 3.0 ms | 919 | 120.7 MB |
| `recall find --ids` | small | 2.9 ms | 37 | 120.7 MB |
| `recall find --all-terms` | small | 3.0 ms | 438 | 120.7 MB |
| `recall find --not` | small | 3.0 ms | 488 | 120.7 MB |
| `recall find --since` | small | 2.8 ms | 491 | 120.7 MB |
| `recall find --author` | small | 2.9 ms | 491 | 120.7 MB |
| `recall find --repo` | small | 3.0 ms | 438 | 120.7 MB |
| `recall find --limit` | small | 2.9 ms | 438 | 120.7 MB |
| `recall turns bare` | small | 2.8 ms | 498 | 120.7 MB |
| `recall turns --budget` | small | 2.9 ms | 498 | 120.7 MB |
| `recall turns --brief` | small | 3.0 ms | 326 | 120.7 MB |
| `recall show bare` | small | 3.0 ms | 8,124 | 120.7 MB |
| `recall show --chars` | small | 2.7 ms | 2,406 | 120.7 MB |
| `recall when` | small | 2.9 ms | 521 | 120.7 MB |
| `recall doctor` | small | 40.1 ms | 791 | 120.7 MB |
| `recall guide` | small | 1.8 ms | 3,444 | 120.7 MB |
| `recall find bare` | medium | 7.0 ms | 439 | 120.7 MB |
| `recall find --all` | medium | 7.2 ms | 441 | 120.7 MB |
| `recall find --results` | medium | 15.5 ms | 402 | 120.7 MB |
| `recall find --tools` | medium | 13.2 ms | 449 | 120.7 MB |
| `recall find --brief` | medium | 7.7 ms | 338 | 120.7 MB |
| `recall find --json` | medium | 7.4 ms | 1,376 | 120.7 MB |
| `recall find --format jsonl` | medium | 7.4 ms | 916 | 120.7 MB |
| `recall find --ids` | medium | 7.3 ms | 37 | 120.7 MB |
| `recall find --all-terms` | medium | 7.3 ms | 439 | 120.7 MB |
| `recall find --not` | medium | 7.1 ms | 489 | 120.7 MB |
| `recall find --since` | medium | 7.4 ms | 492 | 120.7 MB |
| `recall find --author` | medium | 7.2 ms | 492 | 120.7 MB |
| `recall find --repo` | medium | 7.0 ms | 439 | 120.7 MB |
| `recall find --limit` | medium | 7.9 ms | 439 | 120.7 MB |
| `recall turns bare` | medium | 7.2 ms | 499 | 120.7 MB |
| `recall turns --budget` | medium | 7.1 ms | 499 | 120.7 MB |
| `recall turns --brief` | medium | 6.9 ms | 326 | 120.7 MB |
| `recall show bare` | medium | 6.6 ms | 8,158 | 120.7 MB |
| `recall show --chars` | medium | 6.8 ms | 2,409 | 120.7 MB |
| `recall when` | medium | 7.2 ms | 522 | 120.7 MB |
| `recall doctor` | medium | 324.6 ms | 796 | 274.1 MB |
| `recall guide` | medium | 1.8 ms | 3,444 | 120.7 MB |

## Gates

| gate | corpus | limit | measured | verdict |
| --- | --- | ---: | ---: | --- |
| find, conversation tier | medium | 250.0 ms | 0.4 ms | within |
| find, all tiers | medium | 1200.0 ms | 1.1 ms | within |
| cold strip of the whole corpus | medium | 4000.0 ms | 197.3 ms | within |
| cold archive build | medium | 4000.0 ms | 231.7 ms | within |
| incremental archive update | medium | 1500.0 ms | 0.5 ms | within |
| archive load, conversation tier | medium | 195.0 ms | 0.9 ms | within |
| archive load, all tiers | medium | 890.0 ms | 5.0 ms | within |
