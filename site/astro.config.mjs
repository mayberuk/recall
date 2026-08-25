import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

export default defineConfig({
  site: 'https://recall.mayberuk.com',
  base: '/',
  build: { inlineStylesheets: 'always' },
  // A sitemap is the one discovery surface every crawler agrees on, including
  // the ones that ignore llms.txt. Three pages, but it costs nothing to be
  // legible to the retrieval layer that decides whether this site is quotable.
  integrations: [sitemap({ filter: p => !p.endsWith('.md') })],
  // Shiki stamps an inline background on every <pre>, which lands a near-black
  // slab in a light ledger page and brings a whole second palette with it. The
  // documentation page tokenises its own shell samples instead, in the four
  // inks this stylesheet already owns.
  markdown: { syntaxHighlight: false },
});
