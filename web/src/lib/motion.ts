// Pure, DOM-free motion-preference resolution split out from
// motion.svelte.ts (whose $state/$derived-using MotionStore needs the
// Svelte compiler plugin, which vitest.config.ts doesn't wire in) --
// mirrors theme.ts/theme.svelte.ts's own split, for the same reason.
export type MotionPreference = 'system' | 'on' | 'off';

// resolveReducedMotion is the one branch every animated surface's own
// "should this glide or snap" question reduces to: 'on' forces
// animations regardless of the OS setting, 'off' forces them off, and
// 'system' just mirrors prefers-reduced-motion -- exactly today's
// behavior, before Scott's own "Animations" toggle existed.
export function resolveReducedMotion(pref: MotionPreference, systemReduced: boolean): boolean {
  if (pref === 'on') return false;
  if (pref === 'off') return true;
  return systemReduced;
}
