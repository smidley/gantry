// Animation preference: system|on|off, persisted to localStorage --
// mirrors theme.svelte.ts's exact ThemeStore shape. Every animated
// surface in this app (Tween durations, animate:flip/transition:fade)
// reads `motion.reduced` instead of svelte/motion's own
// prefersReducedMotion directly, so Scott can force glides on even when
// the OS itself says to reduce motion (the suspected, but never
// confirmed, explanation for list reorders reading as a hard swap on his
// real box -- see rankStability.ts for the mechanism that turned out to
// be the actual, confirmed cause; this toggle is the hedge in case OS
// settings are ALSO a factor for him specifically).
import { resolveReducedMotion, type MotionPreference } from './motion';
import { setMotionPreference } from './streamdriver.svelte';

const STORAGE_KEY = 'gantry.motion';

function systemPrefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true;
}

function loadStoredPreference(): MotionPreference {
  if (typeof localStorage === 'undefined') return 'system';
  const stored = localStorage.getItem(STORAGE_KEY);
  return stored === 'on' || stored === 'off' || stored === 'system' ? stored : 'system';
}

class MotionStore {
  preference = $state<MotionPreference>(loadStoredPreference());
  #systemReduced = $state(systemPrefersReducedMotion());
  reduced = $derived(resolveReducedMotion(this.preference, this.#systemReduced));

  constructor() {
    if (typeof window !== 'undefined' && window.matchMedia) {
      // The OS-level media query is not itself reactive to Svelte --
      // when it fires and we're following "system", update the signal
      // `reduced` derives from directly.
      window.matchMedia('(prefers-reduced-motion: reduce)').addEventListener('change', (e) => {
        this.#systemReduced = e.matches;
      });
    }
  }

  set(pref: MotionPreference) {
    this.preference = pref;
    if (typeof localStorage !== 'undefined') localStorage.setItem(STORAGE_KEY, pref);
    // streamdriver.svelte.ts's shared rAF driver keeps its own,
    // independent reduced-motion signal (deliberately free of any
    // svelte/motion or runes import -- see its own doc) -- a same-tab
    // localStorage write fires no 'storage' event of its own, so it
    // needs this direct nudge to notice a preference change right away
    // instead of only on its next OS-level matchMedia callback.
    setMotionPreference(pref);
  }
}

export const motion = new MotionStore();
