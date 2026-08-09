// The one test in this package that runs a build, so it needs Node's
// environment rather than jsdom. Vitest reads that from this docblock and
// applies it to the whole file, which is why it is a file of its own.
//
// @vitest-environment node
import { build } from "vite";
import { describe, expect, it } from "vitest";

import { STORAGE_KEY } from "./theme";

/**
 * Where the theme script ends up relative to the stylesheet, in the document a
 * browser is actually served.
 *
 * `noflash.test.ts` asserts two facts about `index.html` — the script is in
 * `<head>`, and the file declares no stylesheet of its own — and then reasons
 * from a third: that Vite appends the built `<link rel="stylesheet">` to the end
 * of `<head>`. That third fact is somebody else's behaviour. It is true of Vite
 * 6 and it is not promised anywhere, so reasoning from it is exactly the kind of
 * assumption that rots quietly: nothing about the app would fail, the first
 * paint would simply be in the wrong theme again.
 *
 * So this runs the build and looks. `write: false`, so it emits nothing to disk;
 * it costs about a second and a half, which is the price of not having to write
 * "verified by hand at the time" in a comment nobody can re-run.
 */
describe("the built document", () => {
  it("serves the theme script ahead of the stylesheet", async () => {
    const result = await build({ logLevel: "silent", build: { write: false } });
    const outputs = Array.isArray(result) ? result : [result];
    const assets = outputs.flatMap(
      (output) => (output as { output: { fileName: string; source?: unknown }[] }).output,
    );

    const document = assets.find((asset) => asset.fileName === "index.html")?.source;
    expect(typeof document, "the build emits an index.html").toBe("string");

    // Comments removed first, and the same reason as in `noflash.test.ts` —
    // which is worth stating twice because it has now caught this twice. The
    // comment above the script in index.html *describes* the link Vite adds and
    // spells the tag out, the build copies comments through verbatim, and a
    // scan of the raw document therefore finds a stylesheet several hundred
    // characters before the one that exists.
    const html = (document as string).replace(/<!--[\s\S]*?-->/g, "");

    // Found by the key rather than by `<script>`, so this cannot be satisfied
    // by some other inline script the build happened to add.
    const script = html.indexOf(JSON.stringify(STORAGE_KEY));
    const stylesheet = html.indexOf('rel="stylesheet"');

    expect(script, "the theme script survives the build").toBeGreaterThanOrEqual(0);
    expect(
      stylesheet,
      "the build emits a stylesheet link — without one this assertion would " +
        "compare against -1 and pass while proving nothing",
    ).toBeGreaterThanOrEqual(0);
    expect(
      script,
      "a stylesheet applied before this script runs means the page paints in " +
        "one theme and flips to the other, which is the whole of what the " +
        "script is for. index.html cannot show this on its own: the link is " +
        "the build's, and where the build puts it is Vite's decision rather " +
        "than ours",
    ).toBeLessThan(stylesheet);

    // The build inlines nothing and transforms nothing here, so the tag stays
    // classic. A bundler that decided to hoist it into the entry module would
    // make it deferred without changing a line of this repository.
    const tag = html.lastIndexOf("<script", script);
    expect(
      html.slice(tag, script),
      "a `type` on the tag the build emitted would make it deferred, however " +
        "it got there",
    ).not.toMatch(/\btype=/);
  }, 60_000);
});
