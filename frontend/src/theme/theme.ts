/**
 * The theme's vocabulary: what a user can choose, what lands on the document,
 * and the three names that have to be spelled the same in four places.
 *
 * **This is the one file in the app allowed to name a theme.** It is the single
 * entry in `MAY_NAME_A_THEME` in `src/architecture.test.ts`, and the point of
 * keeping the list one long is that every other file — the store, the toggle,
 * the shell — imports a name from here and never writes one. A component that
 * cannot spell a theme cannot branch on one.
 *
 * The names below are written down in four places and only one of them is
 * typed:
 *
 *   - here;
 *   - `src/styles.css`, whose dark block is `:root[<attribute>="dark"]` and
 *     whose `dark` variant keys off the same selector;
 *   - the no-flash script in `index.html`, which is a string in HTML and cannot
 *     import;
 *   - `localStorage`, which is a string on the user's disk.
 *
 * Two tests hold those together rather than trusting them to stay in step:
 * `src/theme/noflash.test.ts` reads `index.html` off disk and both asserts and
 * *runs* the script against the constants here, and `src/test/palette.ts`
 * builds the stylesheet selector it looks for out of `THEME_ATTRIBUTE`, so a
 * rename that misses the stylesheet fails rather than silently producing a page
 * with no dark theme.
 */

/**
 * What the user can choose.
 *
 * Three settings, two themes. `system` is **not** a resolved value — it is the
 * absence of a choice, and it is the default precisely because it is what an
 * empty `localStorage` means. Storing the string "system" would make "never
 * chose" and "chose to follow the OS" two states that behave identically and
 * can disagree, so `choose(system)` removes the key rather than writing it.
 */
export const THEME_SETTINGS = ["light", "dark", "system"] as const;

export type ThemeSetting = (typeof THEME_SETTINGS)[number];

/**
 * What lands on the root element, and the only two values the stylesheet knows.
 *
 * `system` is never one of these: resolving it is the job of the script in
 * `index.html` and of the store in `ThemeProvider.tsx`, so that the stylesheet
 * has one thing to key off and no media query of its own. A
 * `prefers-color-scheme` duplicate of the dark block would be the second
 * palette this design exists to prevent.
 */
export const THEMES = ["light", "dark"] as const;

export type Theme = (typeof THEMES)[number];

/** What the toggle calls each setting. */
export const SETTING_LABEL: Record<ThemeSetting, string> = {
  light: "Light",
  dark: "Dark",
  system: "System",
};

/**
 * The one `localStorage` key, namespaced because a demo runs on `localhost`
 * beside whatever else the reader has been building there.
 */
export const STORAGE_KEY = "agentic-payments.theme";

/** The attribute the resolved theme is written to, on `<html>`. */
export const THEME_ATTRIBUTE = "data-theme";

/** The media query that answers what the OS is set to. */
export const DARK_QUERY = "(prefers-color-scheme: dark)";

/** What an absent stored choice means. */
export const DEFAULT_SETTING: ThemeSetting = "system";

/**
 * Whether a value read back out of storage is still one of the three.
 *
 * Storage is shared with every other page on the origin and survives a rename,
 * so the value that comes back is untrusted input rather than a `ThemeSetting`
 * that merely needs casting.
 */
export function isThemeSetting(value: unknown): value is ThemeSetting {
  return typeof value === "string" && (THEME_SETTINGS as readonly string[]).includes(value);
}

/** Whether a value read off the root element is one of the two themes. */
export function isTheme(value: unknown): value is Theme {
  return typeof value === "string" && (THEMES as readonly string[]).includes(value);
}

/**
 * Whether this setting is decided by the OS rather than by the user.
 *
 * A type predicate rather than a boolean, so that `resolve` below narrows: the
 * other two settings *are* themes, and saying so in the type is what keeps the
 * function from needing a cast or a third branch that cannot happen.
 */
export function followsSystem(setting: ThemeSetting): setting is "system" {
  return setting === "system";
}

/** The theme the OS is asking for. */
export function themeOfSystem(prefersDark: boolean): Theme {
  return prefersDark ? "dark" : "light";
}

/** The theme a setting resolves to, given what the OS currently prefers. */
export function resolve(setting: ThemeSetting, prefersDark: boolean): Theme {
  return followsSystem(setting) ? themeOfSystem(prefersDark) : setting;
}
