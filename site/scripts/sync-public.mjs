// One source, several representations. llms.txt and ai.txt are authored at the
// repository root, where raw.githubusercontent serves them, but an agent looks
// for them at a domain root. The benchmark page reads the same RESULTS.md the
// bench harness writes. Everything here is generated at build time, because a
// checked-in second copy is a copy that goes stale.
import { copyFileSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const site = join(here, '..');
const root = join(site, '..');

for (const f of ['llms.txt', 'ai.txt']) {
  copyFileSync(join(root, f), join(site, 'public', f));
}

const results = readFileSync(join(root, 'bench', 'RESULTS.md'), 'utf8');
copyFileSync(join(root, 'bench', 'RESULTS.md'), join(site, 'public', 'bench.md'));

// llms-full.txt is the whole documentation surface in one fetch. llms.txt is a
// map an agent has to follow; this is the thing the map points at, so a crawler
// that reads one file reads all of it.
const docs = readFileSync(join(site, 'src', 'content', 'docs.md'), 'utf8');
writeFileSync(join(site, 'public', 'llms-full.txt'), [
  '# recall',
  '',
  readFileSync(join(root, 'llms.txt'), 'utf8').split('\n').slice(2).join('\n').split('## Docs')[0].trim(),
  '',
  '---',
  '',
  docs,
  '',
  '---',
  '',
  results,
].join('\n'));

/* ── bench/RESULTS.md, parsed ──────────────────────────────────────────
   The benchmark page renders the harness's own output rather than numbers
   retyped beside it. A parse that silently returns nothing would publish an
   empty page, so every table this file expects is required by name. */

const rows = (heading) => {
  const body = results.split(`\n## ${heading}\n`)[1];
  if (!body) throw new Error(`bench/RESULTS.md has no "## ${heading}" section`);
  const lines = body.split('\n## ')[0].split('\n')
    .filter(l => l.trim().startsWith('|'))
    .map(l => l.trim().replace(/^\|/, '').replace(/\|$/, '').split('|').map(c => c.trim()));
  const out = lines.slice(2).map(cells => cells.map(c => c.replace(/^`|`$/g, '')));
  if (!out.length) throw new Error(`bench/RESULTS.md: "## ${heading}" holds no rows`);
  return out;
};

const num = s => Number(String(s).replace(/[^0-9.]/g, ''));

// When a page last actually changed, for the structured data. A commit date
// rather than an mtime, because a fresh clone stamps every file with the
// moment CI checked it out.
const lastChanged = (path, fallback) => {
  try {
    const d = execFileSync('git', ['log', '-1', '--format=%cI', '--', path],
      { cwd: root, encoding: 'utf8' }).trim();
    return d || fallback;
  } catch {
    return fallback;
  }
};

const bench = {
  docsUpdated: lastChanged('site/src/content/docs.md', ''),
  hardware: results.split('\n')[2].replace(/\s+$/, ''),
  measured: (results.match(/measured (\S+)/) || [])[1] || '',
  corpus: rows('The corpus these numbers came from').map(r => ({
    corpus: r[0], files: r[1], onDisk: r[2], turns: r[3],
    conversation: r[4], invocation: r[5], result: r[6],
  })),
  micro: rows('Micro benchmarks').map(r => ({
    name: r[0], corpus: r[1], ns: num(r[2]), bytes: num(r[3]), allocs: num(r[4]),
  })),
  scenarios: rows('Scenarios').map(r => ({
    name: r[0], corpus: r[1], ms: num(r[2]), out: num(r[3]), rss: num(r[4]),
  })),
  gates: rows('Gates').map(r => ({
    gate: r[0], corpus: r[1], limit: num(r[2]), measured: num(r[3]), verdict: r[4],
  })),
};

mkdirSync(join(site, 'src', 'data'), { recursive: true });
writeFileSync(join(site, 'src', 'data', 'bench.json'), JSON.stringify(bench, null, 2) + '\n');
