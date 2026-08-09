import { describe, expect, it } from "vitest";

import {
  DEFAULT_SETTING,
  isTheme,
  isThemeSetting,
  resolve,
  SETTING_LABEL,
  THEME_SETTINGS,
  THEMES,
  type ThemeSetting,
} from "./theme";

/**
 * The vocabulary, on its own. Everything here is a pure function of two values,
 * which is the point: resolving a setting is the one piece of theme logic that
 * happens in three places — this module, the script in `index.html` and a
 * reader's head — so it is worth being able to read the whole table.
 */
describe("a theme setting", () => {
  it("has three values, and only two of them are themes", () => {
    expect(
      THEME_SETTINGS.filter((setting) => (THEMES as readonly string[]).includes(setting)),
      "`system` is not a resolved value; it is the absence of a choice, and " +
        "the stylesheet has no selector for it",
    ).toEqual([...THEMES]);
    expect(THEME_SETTINGS).toHaveLength(THEMES.length + 1);
  });

  it("defaults to following the OS", () => {
    expect(
      (THEMES as readonly string[]).includes(DEFAULT_SETTING),
      "the default has to be the one setting an empty localStorage can mean",
    ).toBe(false);
    expect(THEME_SETTINGS).toContain(DEFAULT_SETTING);
  });

  it("has a label for every setting", () => {
    // A missing entry renders as an empty button rather than failing to build,
    // because a Record index is typed and an omission is a compile error only
    // as long as the table is written out.
    expect(Object.keys(SETTING_LABEL).sort()).toEqual([...THEME_SETTINGS].sort());
  });

  it.each([
    { setting: "light", prefersDark: false, expected: "light" },
    { setting: "light", prefersDark: true, expected: "light" },
    { setting: "dark", prefersDark: false, expected: "dark" },
    { setting: "dark", prefersDark: true, expected: "dark" },
    { setting: "system", prefersDark: false, expected: "light" },
    { setting: "system", prefersDark: true, expected: "dark" },
  ] as const)(
    "resolves $setting with prefersDark=$prefersDark to $expected",
    ({ setting, prefersDark, expected }) => {
      expect(
        resolve(setting, prefersDark),
        "the OS is consulted for exactly one of the three settings; a chosen " +
          "theme that moved with the OS would not be a choice",
      ).toBe(expected);
    },
  );

  it.each(["", "midnight", "Dark", null, undefined, 1])(
    "does not accept %s from storage as a setting",
    (value) => {
      expect(
        isThemeSetting(value),
        "localStorage is shared with every page on the origin and survives a " +
          "rename, so what comes back out of it is untrusted input",
      ).toBe(false);
    },
  );

  it.each(THEME_SETTINGS)("accepts %s from storage", (setting: ThemeSetting) => {
    expect(isThemeSetting(setting)).toBe(true);
  });

  it("does not accept the system setting as a theme", () => {
    expect(
      isTheme(DEFAULT_SETTING),
      "`isTheme` guards what is read back off the root element, and the root " +
        "never carries an unresolved setting",
    ).toBe(false);
    for (const theme of THEMES) expect(isTheme(theme)).toBe(true);
  });
});
