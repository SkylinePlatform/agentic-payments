import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import {
  DARK_QUERY,
  DEFAULT_SETTING,
  followsSystem,
  isTheme,
  isThemeSetting,
  resolve,
  STORAGE_KEY,
  THEME_ATTRIBUTE,
  themeOfSystem,
  type Theme,
  type ThemeSetting,
} from "./theme";

/**
 * The theme store: the setting the user chose, and the resolution of it that
 * the document is currently carrying.
 *
 * **It reads the root element on mount rather than working the theme out
 * again.** The script in `index.html` has already resolved it before the first
 * paint, and re-deriving here would be a second computation of one value — two
 * answers that agree today, disagree the moment either input is read
 * differently, and disagree for exactly one frame, which is the flash the
 * script was added to remove wearing a different hat. The store trusts what the
 * script decided and corrects it only when something actually changes: the user
 * choosing, or the OS flipping under a setting that follows it.
 *
 * **The resolved theme is deliberately not on the context.** `useTheme` hands
 * back the *setting* and a way to change it, because that is all a control
 * needs; the resolution goes to the root element and is read from there by CSS
 * alone. Nothing in React can branch on which theme is on, which is the rule
 * `src/architecture.test.ts` enforces made structural rather than merely
 * checked.
 */
interface ThemeControl {
  /** What the user chose. The default is the absence of a choice. */
  readonly setting: ThemeSetting;
  /** Record a choice, and apply it. */
  choose(setting: ThemeSetting): void;
}

const ThemeContext = createContext<ThemeControl | null>(null);

/**
 * The setting last chosen, or the default when nothing was stored.
 *
 * `try` because reading storage throws outright in some privacy modes rather
 * than returning null, and a theme control is not worth a blank page.
 */
function storedSetting(): ThemeSetting {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(STORAGE_KEY);
  } catch {
    stored = null;
  }
  return isThemeSetting(stored) ? stored : DEFAULT_SETTING;
}

/**
 * Persist a choice — or, for the default, unmake one.
 *
 * Removing the key rather than writing the default is what keeps "follow the
 * OS" the same state as "has not chosen". Two spellings of one state is how a
 * user who reset the toggle ends up pinned to whatever the OS said the day they
 * did it.
 */
function remember(setting: ThemeSetting): void {
  try {
    if (setting === DEFAULT_SETTING) localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, setting);
  } catch {
    // Storage is unavailable. The choice still applies to this page; it just
    // will not survive a reload, which is a better answer than throwing out of
    // a click handler.
  }
}

/** What the OS currently prefers. */
function prefersDark(): boolean {
  return window.matchMedia(DARK_QUERY).matches;
}

/** What the root element is already carrying, if it is a theme at all. */
function applied(): Theme | null {
  const value = document.documentElement.getAttribute(THEME_ATTRIBUTE);
  return isTheme(value) ? value : null;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [setting, setSetting] = useState<ThemeSetting>(storedSetting);

  // The fallback is the case where the script did not run — a test rendering
  // this provider into a bare document, or a page served without index.html's
  // head. It is not the normal path, and it is written second on purpose:
  // the attribute is the answer, and this is what to do when there is none.
  const [theme, setTheme] = useState<Theme>(
    () => applied() ?? resolve(storedSetting(), prefersDark()),
  );

  useEffect(() => {
    document.documentElement.setAttribute(THEME_ATTRIBUTE, theme);
  }, [theme]);

  useEffect(() => {
    if (!followsSystem(setting)) return;

    // A page can be open across an OS theme change, and without this listener
    // the setting that claims to follow the system does nothing until a reload.
    // Nobody notices that in review and everybody notices it in a demo.
    const media = window.matchMedia(DARK_QUERY);
    const onChange = (event: MediaQueryListEvent) => setTheme(themeOfSystem(event.matches));
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, [setting]);

  const control = useMemo<ThemeControl>(
    () => ({
      setting,
      choose(next) {
        remember(next);
        setSetting(next);
        // Resolved here rather than in an effect: a choice is the one moment
        // the two sources are *supposed* to move, and doing it in the same
        // update as the setting keeps the mount path free of a re-derivation
        // that would defeat the paragraph at the top of this file.
        setTheme(resolve(next, prefersDark()));
      },
    }),
    [setting],
  );

  return <ThemeContext.Provider value={control}>{children}</ThemeContext.Provider>;
}

/**
 * The theme setting, and a way to change it.
 *
 * Throws outside a provider rather than falling back to a default: a toggle
 * rendered outside one would move a piece of state nothing reads, and a control
 * that silently does nothing is the harder failure to find.
 */
export function useTheme(): ThemeControl {
  const control = useContext(ThemeContext);
  if (!control) {
    throw new Error("useTheme was called outside a ThemeProvider");
  }
  return control;
}
