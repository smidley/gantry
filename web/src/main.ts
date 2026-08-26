// Self-hosted fonts only (latin subset; no CDN, ever) -- see the design
// direction: IBM Plex Sans (UI/body), IBM Plex Mono (all data numerals +
// micro-labels), Space Grotesk (display: page titles, stat-tile hero
// numbers).
import '@fontsource/ibm-plex-sans/latin-400.css';
import '@fontsource/ibm-plex-sans/latin-500.css';
import '@fontsource/ibm-plex-sans/latin-600.css';
import '@fontsource/ibm-plex-mono/latin-400.css';
import '@fontsource/ibm-plex-mono/latin-500.css';
import '@fontsource/space-grotesk/latin-500.css';
import '@fontsource/space-grotesk/latin-700.css';
import 'uplot/dist/uPlot.min.css';
import './app.css';

import { mount } from 'svelte';
import App from './App.svelte';

const target = document.getElementById('app');
if (!target) {
  throw new Error('gantry: missing #app mount element');
}

mount(App, { target });
