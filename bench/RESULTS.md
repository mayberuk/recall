# recall benchmark results

AMD Ryzen 7 5700X3D 8-Core Processor · 16 cores · linux/amd64 · go1.25.4  
measured 2026-08-24T01:13:46Z

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
| `BenchmarkArchive/medium/cold` | medium | 202,445,714 | 498,370,252 | 634,105 |
| `BenchmarkArchive/medium/warm` | medium | 472,024 | 169,336 | 889 |
| `BenchmarkArchive/small/cold` | small | 25,203,072 | 58,563,330 | 64,401 |
| `BenchmarkArchive/small/warm` | small | 247,968 | 65,751 | 436 |
| `BenchmarkLoad/medium/all-tiers` | medium | 4,986,924 | 51,243,552 | 95 |
| `BenchmarkLoad/medium/conversation` | medium | 1,001,900 | 8,661,838 | 30 |
| `BenchmarkLoad/small/all-tiers` | small | 1,325,827 | 5,148,715 | 57 |
| `BenchmarkLoad/small/conversation` | small | 281,367 | 869,373 | 17 |
| `BenchmarkRank/medium/collapse` | medium | 3,391,759 | 6,776,064 | 66 |
| `BenchmarkRank/medium/score` | medium | 13,964,783 | 23,167,416 | 1,813 |
| `BenchmarkRank/small/collapse` | small | 339,407 | 745,633 | 10 |
| `BenchmarkRank/small/score` | small | 1,392,153 | 2,280,958 | 460 |
| `BenchmarkSearch/medium/conjunction` | medium | 3,243,023 | 28,401,960 | 526 |
| `BenchmarkSearch/medium/miss` | medium | 2,405,271 | 115,190 | 378 |
| `BenchmarkSearch/medium/phrase` | medium | 483,021 | 211,441 | 345 |
| `BenchmarkSearch/medium/relaxed` | medium | 387,444 | 47,481 | 273 |
| `BenchmarkSearch/medium/single-common-term` | medium | 1,509,956 | 13,386,734 | 476 |
| `BenchmarkSearch/medium/single-term` | medium | 344,094 | 46,577 | 261 |
| `BenchmarkSearch/medium/single-term-all-tiers` | medium | 822,611 | 76,145 | 274 |
| `BenchmarkSearch/small/conjunction` | small | 826,967 | 1,422,341 | 67 |
| `BenchmarkSearch/small/miss` | small | 1,295,777 | 9,384 | 55 |
| `BenchmarkSearch/small/phrase` | small | 254,814 | 16,232 | 48 |
| `BenchmarkSearch/small/relaxed` | small | 152,185 | 5,008 | 53 |
| `BenchmarkSearch/small/single-common-term` | small | 420,547 | 610,560 | 56 |
| `BenchmarkSearch/small/single-term` | small | 127,979 | 4,328 | 43 |
| `BenchmarkSearch/small/single-term-all-tiers` | small | 476,105 | 7,048 | 44 |
| `BenchmarkStrip/medium/cold` | medium | 187,963,487 | 125,985,685 | 424,799 |
| `BenchmarkStrip/medium/incremental` | medium | 13,555,368 | 59,364,373 | 4,226 |
| `BenchmarkStrip/small/cold` | small | 19,679,519 | 24,502,885 | 42,560 |
| `BenchmarkStrip/small/incremental` | small | 3,096,095 | 17,881,338 | 296 |
| `BenchmarkStripRecord/medium` | medium | 5,327 | 2,864 | 18 |
| `BenchmarkStripRecord/small` | small | 5,396 | 2,848 | 18 |

## Scenarios

The built binary, invoked end to end. Allocation figures belong to the micro
table above: a scenario measures another process, where the comparable costs
are wall clock, the size of the answer, and the memory the process reached.

| scenario | corpus | wall clock | output bytes | peak RSS |
| --- | --- | ---: | ---: | ---: |
| `recall find bare` | small | 4.0 ms | 483 | 110.9 MB |
| `recall find --all` | small | 3.9 ms | 485 | 110.9 MB |
| `recall find --results` | small | 6.3 ms | 460 | 110.9 MB |
| `recall find --tools` | small | 4.3 ms | 493 | 110.9 MB |
| `recall find --brief` | small | 3.7 ms | 383 | 110.9 MB |
| `recall find --json` | small | 4.3 ms | 1,500 | 110.9 MB |
| `recall find --format jsonl` | small | 3.7 ms | 1,042 | 110.9 MB |
| `recall find --ids` | small | 3.9 ms | 37 | 110.9 MB |
| `recall find --all-terms` | small | 3.9 ms | 483 | 110.9 MB |
| `recall find --not` | small | 3.8 ms | 533 | 110.9 MB |
| `recall find --since` | small | 3.9 ms | 536 | 110.9 MB |
| `recall find --author` | small | 3.8 ms | 536 | 110.9 MB |
| `recall find --repo` | small | 3.9 ms | 483 | 110.9 MB |
| `recall find --limit` | small | 3.6 ms | 483 | 110.9 MB |
| `recall find --words` | small | 4.0 ms | 511 | 110.9 MB |
| `recall find --all --words` | small | 4.7 ms | 514 | 110.9 MB |
| `recall turns bare` | small | 3.8 ms | 543 | 110.9 MB |
| `recall turns --budget` | small | 3.7 ms | 543 | 110.9 MB |
| `recall turns --brief` | small | 3.8 ms | 371 | 110.9 MB |
| `recall show bare` | small | 3.7 ms | 8,167 | 110.9 MB |
| `recall show --chars` | small | 3.8 ms | 2,449 | 110.9 MB |
| `recall when` | small | 3.7 ms | 566 | 110.9 MB |
| `recall doctor` | small | 44.6 ms | 791 | 110.9 MB |
| `recall guide` | small | 2.7 ms | 3,455 | 110.9 MB |
| `recall find bare` | medium | 8.5 ms | 484 | 110.9 MB |
| `recall find --all` | medium | 8.2 ms | 485 | 110.9 MB |
| `recall find --results` | medium | 16.3 ms | 461 | 110.9 MB |
| `recall find --tools` | medium | 14.7 ms | 494 | 110.9 MB |
| `recall find --brief` | medium | 8.3 ms | 383 | 110.9 MB |
| `recall find --json` | medium | 8.0 ms | 1,501 | 110.9 MB |
| `recall find --format jsonl` | medium | 8.1 ms | 1,041 | 110.9 MB |
| `recall find --ids` | medium | 8.1 ms | 37 | 110.9 MB |
| `recall find --all-terms` | medium | 8.3 ms | 484 | 110.9 MB |
| `recall find --not` | medium | 8.3 ms | 534 | 110.9 MB |
| `recall find --since` | medium | 8.1 ms | 537 | 110.9 MB |
| `recall find --author` | medium | 8.1 ms | 537 | 110.9 MB |
| `recall find --repo` | medium | 8.2 ms | 484 | 110.9 MB |
| `recall find --limit` | medium | 8.1 ms | 484 | 110.9 MB |
| `recall find --words` | medium | 9.1 ms | 513 | 110.9 MB |
| `recall find --all --words` | medium | 12.2 ms | 517 | 110.9 MB |
| `recall turns bare` | medium | 7.8 ms | 544 | 110.9 MB |
| `recall turns --budget` | medium | 7.9 ms | 544 | 110.9 MB |
| `recall turns --brief` | medium | 8.1 ms | 371 | 110.9 MB |
| `recall show bare` | medium | 7.7 ms | 8,203 | 110.9 MB |
| `recall show --chars` | medium | 7.9 ms | 2,454 | 110.9 MB |
| `recall when` | medium | 7.9 ms | 567 | 110.9 MB |
| `recall doctor` | medium | 321.2 ms | 797 | 294.6 MB |
| `recall guide` | medium | 2.7 ms | 3,455 | 110.9 MB |

## Gates

| gate | corpus | limit | measured | verdict |
| --- | --- | ---: | ---: | --- |
| find, conversation tier | medium | 250.0 ms | 0.3 ms | within |
| find, all tiers | medium | 1200.0 ms | 0.8 ms | within |
| cold strip of the whole corpus | medium | 4000.0 ms | 185.3 ms | within |
| cold archive build | medium | 4000.0 ms | 467.5 ms | within |
| incremental archive update | medium | 1500.0 ms | 0.6 ms | within |
| archive load, conversation tier | medium | 195.0 ms | 1.3 ms | within |
| archive load, all tiers | medium | 890.0 ms | 5.7 ms | within |
