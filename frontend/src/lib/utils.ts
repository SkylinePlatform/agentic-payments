import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * Joins class names and lets the last one win.
 *
 * `clsx` flattens the conditionals; `tailwind-merge` resolves the conflicts, so
 * that a caller passing `className="bg-wash"` to a component whose default is
 * `bg-paper` gets `bg-wash` rather than two classes whose winner depends on the
 * order Tailwind happened to emit them in.
 *
 * It needs no configuring for this palette even though the palette is entirely
 * custom: tailwind-merge treats any unrecognised value after `bg-` as a colour,
 * so `bg-ink bg-paper` collapses the same way `bg-red-500 bg-blue-500` would.
 * Verified rather than assumed — an unconfigured merge that silently kept both
 * classes would be invisible until a component override quietly did nothing.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
