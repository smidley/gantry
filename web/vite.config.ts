import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// Output lands in internal/server/webdist/, which webfs_dist.go
// (-tags webdist) embeds -- see that file and Makefile's `web`/`release`
// targets for the rest of the build-tag flip this feeds. emptyOutDir is
// explicit (rather than relying on Vite's default-true-only-inside-
// root behavior) because webdist sits outside web/'s own directory
// tree, where Vite would otherwise refuse to clean it without asking.
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: '../internal/server/webdist',
    emptyOutDir: true,
  },
});
