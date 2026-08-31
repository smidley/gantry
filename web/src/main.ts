// Self-hosted fonts only (no CDN, ever). Legibility corrective pass:
// Inter is now the single UI+display+data face (body text, page
// titles, stat-tile hero numbers, and every headline) -- Space
// Grotesk's flagged '1' and other quirky letterforms read poorly at
// the sizes this app actually uses them at, and IBM Plex Sans is
// retired alongside it so there's one sans face, not two. One variable-
// weight import covers the whole 100-900 range Inter ships (400/500/
// 600/700 are all drawn from this one file); the browser only fetches
// the unicode-range subset (e.g. latin) actually used on the page, so
// this isn't heavier than the old per-weight static imports. IBM Plex
// Mono is loaded only for the raw log viewer; the rest of the interface,
// including micro-labels and chart axes, uses Inter for a uniform rhythm.
import '@fontsource-variable/inter/wght.css';
import '@fontsource/ibm-plex-mono/latin-400.css';
import '@fontsource/ibm-plex-mono/latin-500.css';
import 'uplot/dist/uPlot.min.css';
import './app.css';

import { mount } from 'svelte';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) {
  throw new Error('gantry: missing #app mount element');
}

mount(App, { target });
