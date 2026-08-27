// fallbackLetter picks the single glyph ContainerIcon.svelte's fallback
// avatar shows when a container has no net.unraid.docker.icon label (or
// its <img> failed to load): the name's first character, uppercased --
// "jellyfin" -> "J". A name that's empty, or whitespace-only, falls back
// to "?" rather than rendering a blank avatar.
export function fallbackLetter(name: string): string {
  return name.trim().charAt(0).toUpperCase() || '?';
}
