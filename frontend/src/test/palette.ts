/**
 * Reads the palette back out of `src/styles.css`.
 *
 * One source, both themes. The stylesheet is the only place a colour is
 * written down, so a test that wants to know what `seal` is has to parse it —
 * a second copy in TypeScript would be a second palette, which is the thing
 * this whole design is arranged to avoid.
 *
 * `?raw` rather than `node:fs`: Vitest resolves it through the same Vite
 * pipeline the app builds with, and the package gets to keep `@types/node` out
 * of its `tsconfig` — which matters, because adding it would put `process.env`
 * within reach of every component in the app.
 *
 * Not collected as a suite: `vite.config.ts` includes only `src/**\/*.test.*`.
 */

import stylesheet from "../styles.css?raw";
import { THEME_ATTRIBUTE, THEMES, type Theme } from "../theme/theme";

/**
 * The six, in the order `docs/specs/2026-08-06-three-lane-view-design.md`
 * tabulates them.
 */
export const TOKENS = ["ink", "paper", "graphite", "seal", "broken", "wash"] as const;

export type Token = (typeof TOKENS)[number];

/**
 * The two themes, from the module that names them.
 *
 * Re-exported rather than declared again: `src/theme/theme.ts` is the one file
 * allowed to name a theme, and a second copy of the pair here would be a second
 * place to rename.
 */
export { THEMES, type Theme };

/**
 * Where each theme's values are declared in `styles.css`.
 *
 * The dark selector is *built* from `THEME_ATTRIBUTE` rather than written out,
 * which closes the third spelling of that name. It appears in TypeScript, in
 * the script in `index.html` and in this stylesheet, and `blockOf` throws when
 * the selector it is handed is not in the file — so renaming the attribute
 * without renaming it in `styles.css` fails here, loudly, instead of producing
 * an app whose dark theme silently stops existing.
 */
export const BLOCK_OF: Record<Theme, string> = {
  light: "@theme",
  dark: `:root[${THEME_ATTRIBUTE}="dark"]`,
};

export const STYLESHEET = stylesheet;

/**
 * The text between the braces of the block introduced by `selector`.
 *
 * Brace-matched rather than read to the first `}`, so that a nested rule added
 * later — a `@media` inside `@theme`, say — truncates nothing silently.
 */
export function blockOf(selector: string): string {
  const at = stylesheet.indexOf(selector);
  if (at < 0) {
    throw new Error(`styles.css declares no \`${selector}\` block`);
  }
  const open = stylesheet.indexOf("{", at);
  if (open < 0) {
    throw new Error(`\`${selector}\` in styles.css is not followed by a block`);
  }
  let depth = 0;
  for (let i = open; i < stylesheet.length; i++) {
    if (stylesheet[i] === "{") depth++;
    else if (stylesheet[i] === "}") {
      depth--;
      if (depth === 0) return stylesheet.slice(open + 1, i);
    }
  }
  throw new Error(`\`${selector}\` in styles.css has an unclosed block`);
}

/**
 * The `--color-<name>: <value>` declarations in a block, in source order.
 *
 * `--color-*: initial` is not one of these: the name pattern below does not
 * match `*`, which is deliberate — the reset is a separate fact and
 * `architecture.test.ts` asserts it separately, including that it comes first.
 */
export function colourDeclarations(block: string): Map<string, string> {
  const found = new Map<string, string>();
  for (const [, name, value] of block.matchAll(/--color-([a-z][a-z0-9-]*)\s*:\s*([^;]+);/g)) {
    found.set(name, value.trim());
  }
  return found;
}

/**
 * Every token's value in every theme, as a `#rrggbb` string.
 *
 * Throws rather than returning a partial palette: a theme missing a token is a
 * component rendering an inherited colour, which is exactly the failure the
 * closed palette exists to make impossible, and it should not reach an
 * assertion as `undefined`.
 */
export function palette(): Record<Theme, Record<Token, string>> {
  const out = {} as Record<Theme, Record<Token, string>>;
  for (const theme of THEMES) {
    const declared = colourDeclarations(blockOf(BLOCK_OF[theme]));
    const values = {} as Record<Token, string>;
    for (const token of TOKENS) {
      const value = declared.get(token);
      if (value === undefined) {
        throw new Error(`the ${theme} theme declares no --color-${token}`);
      }
      if (!/^#[0-9a-f]{6}$/i.test(value)) {
        throw new Error(
          `--color-${token} is \`${value}\` in the ${theme} theme; the contrast ` +
            `test can only read a #rrggbb literal, which is why styles.css uses ` +
            `one rather than oklch() or color-mix()`,
        );
      }
      values[token] = value.toLowerCase();
    }
    out[theme] = values;
  }
  return out;
}
