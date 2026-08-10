/**
 * The one Node function the golden-vector suites need, declared here rather
 * than by adding `@types/node`.
 *
 * # Why a fixture outside this package is read, never imported
 *
 * Two suites compare against vectors that live outside `frontend/`:
 * `contracts/testdata/render_vectors.json`, which Go generates and owns, and
 * `backend/pkg/sdjwt/testdata/*.sdjwt`, which are RFC 9901's own examples and
 * the delegate chains pinned beside them. Neither may be copied under
 * `frontend/` — a copy of a published vector still looks exactly like a vector
 * after the original moves, and goes on passing while the two languages agree
 * with each other and with nothing else.
 *
 * That leaves how to reach them, and the bundler route is the shorter one and
 * the wrong one. `import.meta.glob`, a plain `?raw` import and even
 * `new URL("literal", import.meta.url)` all make the fixture a module Vite
 * resolves, and a module outside the package root is reachable only through the
 * dev server's `/@fs/` escape hatch — which has to be opened by widening
 * `server.fs.allow`. That list governs what the **dev server serves to a page**,
 * so a test fixture would be buying itself HTTP surface area on every
 * developer's machine to solve a problem that never leaves Node.
 *
 * The `new URL` half is not a theory: the form was tried, and the expression
 * came back as `http://localhost:3000/@fs/...`, which `readFileSync` refuses
 * with "The URL must be of scheme file". Vite's asset transform only fires on a
 * string *literal*, so binding the path to a constant first is load-bearing
 * rather than style — an identifier it cannot analyse statically is left alone,
 * and the expression evaluates to the plain `file:` URL it reads as. Both
 * suites do it that way and say so beside the line.
 *
 * Reading the file from disk keeps it a fixture rather than a module, and it
 * fails loudly — `readFileSync` throws on a path that moved — rather than by
 * quietly resolving to an empty string, which is what a `?raw` import of an
 * unreachable file does.
 *
 * `src/test/palette.ts` reaches for `?raw` instead, and that is right for what
 * it does: `styles.css` is *in* `src/`, is a source file the app ships, and
 * wants the same resolution the app builds with. A fixture in another tree is
 * the other case.
 *
 * # Why not `@types/node`
 *
 * Its globals are the thing `src/test/palette.ts` was written to avoid: adding
 * them puts `process.env` within reach of every component in the app, where
 * nothing should have one. Declaring the single function used costs nothing and
 * adds no globals — an ambient module declaration introduces the module, not the
 * environment.
 *
 * It lives under `src/test/` because it is a test-only concern with more than
 * one consumer, and because every architecture rule in this package excludes
 * that directory: a `.d.ts` beside a module would otherwise be scanned as one of
 * that module's sources.
 *
 * `URL` is accepted because that is how the path arrives: `new URL(..., import.meta.url)`
 * resolves the fixture against the reading file rather than against a working
 * directory, and Node takes a `file:` URL wherever it takes a path — which
 * spares this declaration `node:url` as well.
 */
declare module "node:fs" {
  export function readFileSync(path: URL | string, encoding: "utf8"): string;
}
