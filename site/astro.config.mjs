import { defineConfig } from 'astro/config';

export default defineConfig({
  site: 'https://recall.mayberuk.com',
  base: '/',
  build: { inlineStylesheets: 'always' },
  // Shiki stamps an inline background on every <pre>, which lands a near-black
  // slab in a light ledger page and brings a whole second palette with it. The
  // samples here are shell one-liners with nothing worth colouring, so the
  // stylesheet owns code blocks and the four-ink rule holds.
  markdown: { syntaxHighlight: false },
});
