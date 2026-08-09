import { SETTING_LABEL, THEME_SETTINGS } from "./theme";
import { useTheme } from "./ThemeProvider";

/**
 * The three-state theme control.
 *
 * Radio inputs rather than buttons with `aria-pressed`, because this is one
 * exclusive choice out of three and that is what a radio group *is*: the
 * grouping, the arrow-key navigation and the announcement of "2 of 3" all come
 * from the browser, and none of it is worth reimplementing on a `<div>`. The
 * inputs are visually hidden and the label beside each carries the appearance,
 * which is why the focus ring is on the label — the thing the eye is on — while
 * focus itself is on the input.
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
