/**
 * The one Node function the vector test needs, declared here rather than by
 * adding `@types/node`.
 *
 * `render.test.ts` reads `contracts/testdata/render_vectors.json`, which lives
 * outside this package. A bundler import would be the shorter route and is the
 * wrong one: `import.meta.glob` and a plain JSON import both make the fixture a
 * module Vite resolves, so the production build would be reaching outside its
 * own root for a test file. `src/test/palette.ts` reaches for `?raw` instead,
 * and that is right for what it does — `styles.css` is *in* `src/` and is a
 * source file the app ships. A fixture in `contracts/` is a different case, and
 * reading it from disk is what keeps it out of the module graph entirely.
 *
 * What that would normally cost is `@types/node`, and its globals are the thing
 * `src/test/palette.ts` was written to avoid: adding them puts `process.env`
 * within reach of every component in the app, where nothing should have one.
 * Declaring the single function used costs nothing and adds no globals — an
 * ambient module declaration introduces the module, not the environment.
 *
 * `URL` is accepted because that is how the path arrives: `new URL(..., import.meta.url)`
 * resolves the fixture against this file rather than against a working
 * directory, and Node takes a `file:` URL wherever it takes a path — which
 * spares this declaration `node:url` as well.
 */
declare module "node:fs" {
  export function readFileSync(path: URL | string, encoding: "utf8"): string;
}
