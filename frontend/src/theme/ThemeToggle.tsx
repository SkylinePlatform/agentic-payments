import { SETTING_LABEL, THEME_SETTINGS } from "./theme";
import { useTheme } from "./ThemeProvider";

/**
 * The three-state theme control.
 *
 * Radio inputs rather than buttons with `aria-pressed`, because this is one
 * exclusive choice out of three and that is what a radio group *is*. Two things
 * come from the browser for free and are the reason not to reimplement it on a
 * `<div>`: the three settings are **one tab stop** rather than three, and the
 * arrow keys move between them and apply what they land on.
 *
 * **Neither of those is in the suite, and that is a gap rather than an
 * oversight.** Both are browser behaviour rather than DOM API: jsdom implements
 * no arrow-key navigation for a radio group, and `user-event`'s `{ArrowRight}`
 * throws in this setup rather than doing nothing, so a test for it would assert
 * the test library's model of a browser and not a browser. They were checked in
 * headless Chrome against the dev server — one tab stop, and ArrowRight from
 * *Light* selecting and applying *Dark* — which is evidence that does not
 * re-run. `ThemeProvider.test.tsx` covers what the setting does once it
 * changes, by whatever means.
 *
 * The inputs are visually hidden and the label beside each carries the
 * appearance, which is why the focus ring is on the label — the thing the eye
 * is on — while focus itself is on the input.
 *
 * This component knows which *setting* is chosen, and that is the whole of what
 * it may know. It never learns which theme that resolved to: the resolution is
 * not on the context, the labels come from a table rather than a branch, and no
 * class below depends on any of it. Every colour here is a token that means the
 * same thing whichever theme is on.
 */
export function ThemeToggle() {
  const { setting, choose } = useTheme();

  return (
    <fieldset className="min-w-0 border-0 p-0">
      <legend className="mb-1.5 font-sans text-xs font-semibold tracking-wide text-graphite uppercase">
        Theme
      </legend>
      <div className="flex divide-x divide-graphite overflow-hidden rounded-sm border border-graphite">
        {THEME_SETTINGS.map((option) => (
          <label key={option} className="flex-1">
            <input
              type="radio"
              name="theme"
              value={option}
              checked={setting === option}
              onChange={() => choose(option)}
              className="peer sr-only"
            />
            <span
              className={
                "block cursor-pointer px-2 py-1.5 text-center font-sans text-xs " +
                "text-graphite transition-colors hover:text-ink " +
                "peer-checked:bg-ink peer-checked:text-paper " +
                "peer-focus-visible:outline-2 peer-focus-visible:outline-ink " +
                "peer-focus-visible:outline-offset-2"
              }
            >
              {SETTING_LABEL[option]}
            </span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}
