import { defineConfig } from 'vitest/config';

// Unit tests here are pure logic (format.ts, router.ts's parseHash) with
// no DOM dependency, so the default 'node' environment is enough --
// jsdom isn't part of the approved-deps list and isn't needed today.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.{test,spec}.ts'],
  },
});
