# recall benchmark results

AMD Ryzen 7 5700X3D 8-Core Processor · 16 cores · linux/amd64 · go1.25.4  
measured 2026-08-18T02:54:47Z

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
| `BenchmarkArchive/medium/cold` | medium | 185,389,326 | 484,510,130 | 634,063 |
| `BenchmarkArchive/medium/warm` | medium | 459,461 | 169,205 | 889 |
| `BenchmarkArchive/small/cold` | small | 24,248,723 | 58,330,120 | 64,401 |
| `BenchmarkArchive/small/warm` | small | 245,081 | 65,708 | 436 |
| `BenchmarkLoad/medium/all-tiers` | medium | 5,001,581 | 50,768,401 | 95 |
| `BenchmarkLoad/medium/conversation` | medium | 1,051,425 | 8,571,725 | 30 |
| `BenchmarkLoad/small/all-tiers` | small | 1,296,806 | 5,107,766 | 57 |
| `BenchmarkLoad/small/conversation` | small | 243,486 | 861,175 | 17 |
| `BenchmarkRank/medium/collapse` | medium | 3,252,332 | 6,776,064 | 66 |
| `BenchmarkRank/medium/score` | medium | 14,061,578 | 23,167,416 | 1,813 |
| `BenchmarkRank/small/collapse` | small | 343,799 | 745,633 | 10 |
| `BenchmarkRank/small/score` | small | 1,409,580 | 2,280,958 | 460 |
| `BenchmarkSearch/medium/conjunction` | medium | 3,115,086 | 28,401,964 | 526 |
| `BenchmarkSearch/medium/miss` | medium | 2,371,618 | 115,190 | 378 |
| `BenchmarkSearch/medium/phrase` | medium | 482,200 | 211,441 | 345 |
| `BenchmarkSearch/medium/relaxed` | medium | 347,681 | 47,480 | 273 |
| `BenchmarkSearch/medium/single-common-term` | medium | 1,493,273 | 13,386,740 | 476 |
| `BenchmarkSearch/medium/single-term` | medium | 342,529 | 46,576 | 261 |
| `BenchmarkSearch/medium/single-term-all-tiers` | medium | 1,181,585 | 76,145 | 274 |
| `BenchmarkSearch/small/conjunction` | small | 755,885 | 1,422,341 | 67 |
| `BenchmarkSearch/small/miss` | small | 1,289,732 | 9,384 | 55 |
| `BenchmarkSearch/small/phrase` | small | 251,999 | 16,232 | 48 |
| `BenchmarkSearch/small/relaxed` | small | 137,375 | 5,008 | 53 |
| `BenchmarkSearch/small/single-common-term` | small | 411,599 | 610,560 | 56 |
| `BenchmarkSearch/small/single-term` | small | 123,942 | 4,328 | 43 |
| `BenchmarkSearch/small/single-term-all-tiers` | small | 462,147 | 7,048 | 44 |
| `BenchmarkStrip/medium/cold` | medium | 207,158,173 | 125,512,760 | 424,800 |
| `BenchmarkStrip/medium/incremental` | medium | 13,599,346 | 59,360,049 | 4,226 |
| `BenchmarkStrip/small/cold` | small | 21,544,806 | 24,455,256 | 42,560 |
| `BenchmarkStrip/small/incremental` | small | 3,345,034 | 17,881,083 | 296 |
| `BenchmarkStripRecord/medium` | medium | 6,156 | 2,848 | 18 |
| `BenchmarkStripRecord/small` | small | 6,187 | 2,832 | 18 |

## Scenarios

The built binary, invoked end to end. Allocation figures belong to the micro
table above: a scenario measures another process, where the comparable costs
are wall clock, the size of the answer, and the memory the process reached.

| scenario | corpus | wall clock | output bytes | peak RSS |
| --- | --- | ---: | ---: | ---: |
| `recall find bare` | small | 3.1 ms | 483 | 112.1 MB |
| `recall find --all` | small | 3.1 ms | 485 | 112.1 MB |
| `recall find --results` | small | 5.4 ms | 460 | 112.1 MB |
| `recall find --tools` | small | 3.5 ms | 493 | 112.1 MB |
| `recall find --brief` | small | 2.8 ms | 383 | 112.1 MB |
| `recall find --json` | small | 3.1 ms | 1,500 | 112.1 MB |
| `recall find --format jsonl` | small | 3.0 ms | 1,044 | 112.1 MB |
| `recall find --ids` | small | 2.8 ms | 37 | 112.1 MB |
| `recall find --all-terms` | small | 2.9 ms | 483 | 112.1 MB |
| `recall find --not` | small | 2.9 ms | 533 | 112.1 MB |
| `recall find --since` | small | 2.9 ms | 536 | 112.1 MB |
| `recall find --author` | small | 2.9 ms | 536 | 112.1 MB |
| `recall find --repo` | small | 2.9 ms | 483 | 112.1 MB |
| `recall find --limit` | small | 3.1 ms | 483 | 112.1 MB |
| `recall find --words` | small | 3.1 ms | 511 | 112.1 MB |
| `recall find --all --words` | small | 3.8 ms | 514 | 112.1 MB |
| `recall turns bare` | small | 3.1 ms | 543 | 112.1 MB |
| `recall turns --budget` | small | 3.1 ms | 543 | 112.1 MB |
| `recall turns --brief` | small | 2.9 ms | 371 | 112.1 MB |
| `recall show bare` | small | 2.8 ms | 8,167 | 112.1 MB |
| `recall show --chars` | small | 2.8 ms | 2,449 | 112.1 MB |
| `recall when` | small | 2.8 ms | 566 | 112.1 MB |
| `recall doctor` | small | 43.8 ms | 791 | 112.1 MB |
| `recall guide` | small | 1.9 ms | 3,444 | 112.1 MB |
| `recall find bare` | medium | 7.5 ms | 484 | 112.1 MB |
| `recall find --all` | medium | 7.9 ms | 485 | 112.1 MB |
| `recall find --results` | medium | 14.9 ms | 461 | 112.1 MB |
| `recall find --tools` | medium | 13.3 ms | 494 | 112.1 MB |
| `recall find --brief` | medium | 7.4 ms | 383 | 112.1 MB |
| `recall find --json` | medium | 7.2 ms | 1,501 | 112.1 MB |
| `recall find --format jsonl` | medium | 7.1 ms | 1,041 | 112.1 MB |
| `recall find --ids` | medium | 6.9 ms | 37 | 112.1 MB |
| `recall find --all-terms` | medium | 7.0 ms | 484 | 112.1 MB |
| `recall find --not` | medium | 7.3 ms | 534 | 112.1 MB |
| `recall find --since` | medium | 7.1 ms | 537 | 112.1 MB |
| `recall find --author` | medium | 7.1 ms | 537 | 112.1 MB |
| `recall find --repo` | medium | 7.3 ms | 484 | 112.1 MB |
| `recall find --limit` | medium | 7.2 ms | 484 | 112.1 MB |
| `recall find --words` | medium | 7.7 ms | 513 | 112.1 MB |
| `recall find --all --words` | medium | 11.2 ms | 516 | 112.1 MB |
| `recall turns bare` | medium | 7.2 ms | 544 | 112.1 MB |
| `recall turns --budget` | medium | 7.1 ms | 544 | 112.1 MB |
| `recall turns --brief` | medium | 7.1 ms | 371 | 112.1 MB |
| `recall show bare` | medium | 7.1 ms | 8,203 | 112.1 MB |
| `recall show --chars` | medium | 6.7 ms | 2,454 | 112.1 MB |
| `recall when` | medium | 6.9 ms | 567 | 112.1 MB |
| `recall doctor` | medium | 304.9 ms | 796 | 253.5 MB |
| `recall guide` | medium | 1.9 ms | 3,444 | 112.1 MB |

## Gates

| gate | corpus | limit | measured | verdict |
| --- | --- | ---: | ---: | --- |
| find, conversation tier | medium | 250.0 ms | 0.4 ms | within |
| find, all tiers | medium | 1200.0 ms | 0.8 ms | within |
| cold strip of the whole corpus | medium | 4000.0 ms | 192.0 ms | within |
| cold archive build | medium | 4000.0 ms | 228.6 ms | within |
| incremental archive update | medium | 1500.0 ms | 0.7 ms | within |
| archive load, conversation tier | medium | 195.0 ms | 2.6 ms | within |
| archive load, all tiers | medium | 890.0 ms | 9.1 ms | within |
