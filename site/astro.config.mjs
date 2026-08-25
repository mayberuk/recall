import { defineConfig } from 'astro/config';

export default defineConfig({
  site: 'https://recall.mayberuk.com',
  base: '/',
  build: { inlineStylesheets: 'always' },
});
