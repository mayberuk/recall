# recall benchmark results

Apple M4 Max · 16 cores · darwin/arm64 · go1.26.5  
measured 2026-08-15T16:51:40Z

Every number here comes from a corpus generated from a seed (`internal/corpusgen`),
never from a session store, so a run on another machine measures the same bytes.
Small is about 5 MB and Medium about 50 MB of transcript.

## The corpus these numbers came from

| corpus | files | on disk | turns | conversation | invocation | result |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| small | 17 | 5.3 MB | 2,972 | 594 turns / 0.6 MB | 1,189 turns / 0.3 MB | 1,189 turns / 2.9 MB |
| medium | 56 | 52.6 MB | 29,564 | 5,898 turns / 6.0 MB | 11,833 turns / 3.1 MB | 11,833 turns / 28.5 MB |

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
| `BenchmarkArchive/medium/cold` | medium | 129,964,849 | 396,335,848 | 633,942 |
| `BenchmarkArchive/medium/warm` | medium | 870,926 | 206,984 | 816 |
| `BenchmarkArchive/small/cold` | small | 40,874,311 | 41,572,743 | 64,310 |
| `BenchmarkArchive/small/warm` | small | 482,671 | 83,864 | 392 |
| `BenchmarkRank/medium/collapse` | medium | 2,170,666 | 6,776,073 | 66 |
| `BenchmarkRank/medium/score` | medium | 8,764,090 | 23,167,417 | 1,813 |
| `BenchmarkRank/small/collapse` | small | 211,258 | 745,632 | 10 |
| `BenchmarkRank/small/score` | small | 877,215 | 2,280,958 | 460 |
| `BenchmarkSearch/medium/conjunction` | medium | 11,430,997 | 20,188,618 | 108 |
| `BenchmarkSearch/medium/miss` | medium | 13,540,008 | 15,656 | 82 |
| `BenchmarkSearch/medium/phrase` | medium | 4,552,486 | 108,872 | 84 |
| `BenchmarkSearch/medium/relaxed` | medium | 3,211,376 | 11,544 | 85 |
| `BenchmarkSearch/medium/single-term` | medium | 3,184,699 | 10,888 | 75 |
| `BenchmarkSearch/medium/single-term-all-tiers` | medium | 19,516,945 | 13,608 | 76 |
| `BenchmarkSearch/small/conjunction` | small | 1,024,313 | 1,427,372 | 60 |
| `BenchmarkSearch/small/miss` | small | 1,352,936 | 14,136 | 43 |
| `BenchmarkSearch/small/phrase` | small | 375,143 | 21,272 | 41 |
| `BenchmarkSearch/small/relaxed` | small | 310,998 | 10,032 | 46 |
| `BenchmarkSearch/small/single-term` | small | 294,393 | 9,369 | 36 |
| `BenchmarkSearch/small/single-term-all-tiers` | small | 1,776,036 | 12,088 | 37 |
| `BenchmarkStrip/medium/cold` | medium | 138,077,969 | 82,422,946 | 424,798 |
| `BenchmarkStrip/medium/incremental` | medium | 3,300,416 | 15,332,864 | 4,225 |
| `BenchmarkStrip/small/cold` | small | 14,599,607 | 11,229,885 | 42,559 |
| `BenchmarkStrip/small/incremental` | small | 668,504 | 4,513,655 | 295 |
| `BenchmarkStripRecord/medium` | medium | 4,155 | 2,880 | 18 |
| `BenchmarkStripRecord/small` | small | 4,262 | 2,880 | 18 |

## Scenarios

The built binary, invoked end to end. Allocation figures belong to the micro
table above: a scenario measures another process, where the comparable costs
are wall clock, the size of the answer, and the memory the process reached.

| scenario | corpus | wall clock | output bytes | peak RSS |
| --- | --- | ---: | ---: | ---: |
| `recall find bare` | small | 13.1 ms | 438 | 7.9 MB |
| `recall find --all` | small | 9.5 ms | 440 | 8.1 MB |
| `recall find --results` | small | 12.6 ms | 404 | 15.2 MB |
| `recall find --tools` | small | 13.6 ms | 448 | 10.4 MB |
| `recall find --brief` | small | 10.8 ms | 338 | 7.9 MB |
| `recall find --json` | small | 9.2 ms | 1,427 | 8.0 MB |
| `recall find --format jsonl` | small | 9.7 ms | 919 | 8.0 MB |
| `recall find --ids` | small | 10.0 ms | 37 | 7.9 MB |
| `recall find --all-terms` | small | 9.5 ms | 438 | 8.0 MB |
| `recall find --not` | small | 8.8 ms | 488 | 7.9 MB |
| `recall find --since` | small | 8.9 ms | 491 | 7.9 MB |
| `recall find --author` | small | 9.1 ms | 491 | 8.0 MB |
| `recall find --repo` | small | 9.7 ms | 438 | 8.1 MB |
| `recall find --limit` | small | 9.2 ms | 438 | 7.9 MB |
| `recall turns bare` | small | 9.2 ms | 498 | 7.9 MB |
| `recall turns --budget` | small | 12.6 ms | 498 | 7.9 MB |
| `recall turns --brief` | small | 9.0 ms | 326 | 8.0 MB |
| `recall show bare` | small | 8.9 ms | 8,124 | 7.9 MB |
| `recall show --chars` | small | 9.3 ms | 2,406 | 7.8 MB |
| `recall when` | small | 13.0 ms | 521 | 8.3 MB |
| `recall doctor` | small | 56.2 ms | 923 | 33.1 MB |
| `recall guide` | small | 7.8 ms | 3,444 | 4.6 MB |
| `recall find bare` | medium | 13.9 ms | 439 | 27.9 MB |
| `recall find --all` | medium | 15.6 ms | 441 | 27.9 MB |
| `recall find --results` | medium | 42.3 ms | 402 | 97.7 MB |
| `recall find --tools` | medium | 22.6 ms | 449 | 46.8 MB |
| `recall find --brief` | medium | 22.0 ms | 338 | 28.0 MB |
| `recall find --json` | medium | 13.2 ms | 1,428 | 28.2 MB |
| `recall find --format jsonl` | medium | 13.0 ms | 916 | 28.0 MB |
| `recall find --ids` | medium | 12.7 ms | 37 | 28.0 MB |
| `recall find --all-terms` | medium | 12.8 ms | 439 | 28.1 MB |
| `recall find --not` | medium | 13.3 ms | 489 | 28.0 MB |
| `recall find --since` | medium | 13.1 ms | 492 | 28.0 MB |
| `recall find --author` | medium | 12.8 ms | 492 | 27.9 MB |
| `recall find --repo` | medium | 13.2 ms | 439 | 28.0 MB |
| `recall find --limit` | medium | 13.4 ms | 439 | 28.0 MB |
| `recall turns bare` | medium | 12.9 ms | 499 | 27.8 MB |
| `recall turns --budget` | medium | 13.2 ms | 499 | 27.9 MB |
| `recall turns --brief` | medium | 12.8 ms | 326 | 27.8 MB |
| `recall show bare` | medium | 12.8 ms | 8,158 | 27.8 MB |
| `recall show --chars` | medium | 12.7 ms | 2,409 | 27.8 MB |
| `recall when` | medium | 14.1 ms | 522 | 27.9 MB |
| `recall doctor` | medium | 205.6 ms | 926 | 263.9 MB |
| `recall guide` | medium | 10.1 ms | 3,444 | 4.8 MB |

## Gates

| gate | corpus | limit | measured | verdict |
| --- | --- | ---: | ---: | --- |
| find, conversation tier | medium | 250.0 ms | 3.3 ms | within |
| find, all tiers | medium | 1200.0 ms | 17.3 ms | within |
| cold strip of the whole corpus | medium | 4000.0 ms | 144.6 ms | within |
| cold archive build | medium | 4000.0 ms | 129.5 ms | within |
| incremental archive update | medium | 1500.0 ms | 1.0 ms | within |
| archive load, conversation tier | medium | 195.0 ms | 2.3 ms | within |
| archive load, all tiers | medium | 890.0 ms | 10.8 ms | within |
