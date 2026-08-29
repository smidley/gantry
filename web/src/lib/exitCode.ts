// Exit-code translation: the small set of docker exit codes common
// enough to name in plain language, for the anomaly banner's "Stopped —
// exit code N (...)" headline (containerAnomaly.ts). Pure and total --
// an uncommon/unmapped code isn't an error, it just renders without a
// parenthetical explanation (see stoppedHeadline).
//
// 137/143 follow the standard "128 + signal number" unix convention
// (SIGKILL=9, SIGTERM=15); 137 is called out as OOM-LIKELY rather than a
// bare "killed" because that's overwhelmingly the real-world cause for a
// container (an external `docker kill`/host shutdown looks the same on
// the wire, but a home-lab container hitting its own memory ceiling is
// the far more common story this banner exists to explain).
const EXIT_CODE_MEANINGS: Record<number, string> = {
  0: 'clean exit',
  1: 'generic error',
  126: 'command not executable',
  127: 'command not found',
  137: 'killed, likely out of memory',
  143: 'terminated',
};

// describeExitCode returns the plain-language meaning of a common exit
// code, or '' for one not in the table -- callers render the code alone
// in that case, with no invented explanation.
export function describeExitCode(code: number): string {
  return EXIT_CODE_MEANINGS[code] ?? '';
}
