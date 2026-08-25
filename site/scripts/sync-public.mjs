// llms.txt and ai.txt are authored at the repository root, where raw.githubusercontent
// serves them, but an agent looks for them at a domain root. Copying at build time
// keeps one source: a checked-in second copy is a copy that goes stale.
import { copyFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, '..', '..');
for (const f of ['llms.txt', 'ai.txt']) {
  copyFileSync(join(root, f), join(here, '..', 'public', f));
}
