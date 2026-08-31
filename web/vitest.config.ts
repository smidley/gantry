import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

// Unit tests here are mostly pure logic (format.ts, router.ts's
// parseHash) with no DOM dependency, so the default 'node' environment
// is enough -- jsdom isn't part of the approved-deps list and isn't
// needed. Component tests (src/components/*.test.ts) render via
// svelte/server -- an SSR string render, still DOM-free -- which is why
// the svelte plugin is loaded: it compiles the .svelte (and .svelte.ts
// rune-module) imports those tests pull in. Vitest runs test files
// through Vite's SSR transform, so the plugin emits the server variant
// of each component automatically.
export default defineConfig({
  plugins: [svelte()],
  test: {
    environment: 'node',
    include: ['src/**/*.{test,spec}.ts'],
  },
});
