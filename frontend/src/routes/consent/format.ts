/**
 * Formats a lifetime in seconds as a plain `"1 hour"` / `"24 hours"` — no
 * `Intl`, so a screenshot taken on this machine reads the same on any other.
 * `open_mandate_lifetime_seconds` is always a whole number of hours on the
 * surfaces this app talks to; a value that is not falls back to whole
 * minutes rather than round to the wrong hour.
 *
 * Lives here rather than on `Consent` or `Signing`, the two callers: `Consent`
 * states the lifetime before anything is signed and `Signing`'s stranded
 * screen restates it afterwards — the one fact that makes two open mandates
 * nobody revoked tolerable is a duration, and both screens have to give the
 * same one. `Consent` already imports `Signing` to render it once the user
 * signs, so a helper defined on either component and imported by the other
 * would be a cycle; a third file is not.
 */
export function lifetime(seconds: number): string {
  if (seconds % 3600 === 0) {
    const hours = seconds / 3600;
    return `${hours} ${hours === 1 ? "hour" : "hours"}`;
  }
  const minutes = Math.round(seconds / 60);
  return `${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
}
