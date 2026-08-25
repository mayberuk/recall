import { defineConfig } from 'astro/config';

export default defineConfig({
  site: 'https://mayberuk.github.io',
  base: '/recall',
  build: { inlineStylesheets: 'always' },
});
