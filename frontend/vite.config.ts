import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
// Tailwind v4 has no tailwind.config.js and no PostCSS step. The theme lives in
// src/styles.css, and this plugin is the whole of the build integration.
import tailwindcss from "@tailwindcss/vite";

/**
 * The collector's default listen address, matching `-addr` in
 * backend/cmd/collector. Overridable with VITE_COLLECTOR_URL so the demo runner
 * can move it without editing this file.
 */
const DEFAULT_COLLECTOR = "http://127.0.0.1:8085";

/**
 * The Shopping Agent's console API, matching `-addr` on the `agent-watch` entry
 * in deploy/demo.json. Overridable with VITE_AGENT_URL, in VITE_COLLECTOR_URL's
 * shape and for the same reason.
 */
const DEFAULT_AGENT = "http://127.0.0.1:8086";

/**
 * defineConfig comes from vitest/config rather than vite so that the `test`
 * block below type-checks; loadEnv still comes from vite, which is the only
 * one of the two that exports it. Vitest reads this file, so the tests run
 * against the same resolution and plugins as the app — one config, not two
 * that can disagree about what a `../protocol` import means.
 */
export default defineConfig(({ mode }) => {
  // "." rather than process.cwd(): this file is type-checked with the app, and
  // reaching for process would pull Node's type definitions into a config that
  // needs nothing else from them.
  //
  // Vitest resolves it the same way, against the working directory. `npm test`
  // and `make frontend-test` both run from frontend/, so it lands here, and
  // the mode is "test" rather than "development" — so .env and .env.test are
  // what it reads. Verified with a temporary .env.test rather than assumed: a
  // config that threw under the test runner would have made every test
  // unrunnable for a reason nothing about a failing test would point at.
  const env = loadEnv(mode, ".", "VITE_");
  const collector = env.VITE_COLLECTOR_URL ?? DEFAULT_COLLECTOR;
  const agent = env.VITE_AGENT_URL ?? DEFAULT_AGENT;

  return {
    plugins: [react(), tailwindcss()],
    server: {
      port: 5173,
      // Fail rather than silently moving to another port. The demo runner
      // prints a URL, and a dev server that quietly picked 5174 makes that
      // URL wrong.
      strictPort: true,

      // Vite refuses to load a module from outside the workspace root, and
      // this package *is* the workspace root — `package-lock.json` lives here,
      // so nothing above `frontend/` is reachable by default. Vitest goes
      // through the same transform pipeline, so the restriction applies to
      // fixtures as much as to source.
      //
      // One directory is added, and only one: the SD-JWT conformance vectors,
      // which `src/sdjwt/golden.test.ts` imports with `?raw`. RFC 9901
      // publishes its own disclosures, digests and processed payloads, and
      // those files are where the Go implementation already reads them from —
      // so a copy under `frontend/` would be a second source of truth for a
      // vector, which goes on passing after the original moves. The header of
      // that test file carries the full argument.
      //
      // Deliberately not `".."`. The list governs what the *dev server* will
      // serve to a page, so widening it to the repository would put every file
      // in this checkout one request away from anything running in a browser
      // on this machine.
      fs: { allow: [".", "../backend/pkg/sdjwt/testdata"] },
      proxy: {
        // The event stream is served same-origin in development, so nothing
        // downstream has to solve CORS or learn the collector's address. It is
        // here rather than in the view that consumes it because it is dev
        // server configuration, and because the alternative is that whoever
        // builds the three-lane view spends their first hour on it.
        //
        // SSE needs the proxy left alone: no buffering, no timeout on a
        // response that is supposed to stay open.
        "/events": {
          target: collector,
          changeOrigin: true,
          // A stream has no content length and never ends on its own.
          timeout: 0,
          proxyTimeout: 0,
        },

        // The agent's console API, same-origin for the same reason — and here
        // the case against solving it with CORS instead is sharper than
        // convenience. `POST` carrying `Idempotency-Key` is not a simple
        // request, so a browser preflights it; `transport.Idempotency` treats
        // `OPTIONS` as safe and passes it straight to the mux, which has no
        // handler for it and answers 405. CORS is therefore not a header on one
        // route but a change to middleware every role runs — a process holding a
        // signing key, on every state-changing route that spends a user's open
        // mandate — to serve one browser in one dev setup.
        //
        // No `rewrite`, which is why the route is `/watches` rather than
        // something needing a prefix stripped: the path the browser asks for is
        // the path the agent serves.
        "/watches": {
          target: agent,
          changeOrigin: true,
        },
      },
    },

    test: {
      // These are React components, and there is no DOM to render them into
      // under Vitest's default `node` environment.
      environment: "jsdom",

      // Testing Library registers its own cleanup only when Vitest runs with
      // `globals: true`. It does not here, so setup.ts registers it — and it
      // is also where the note about jsdom's missing EventSource lives, next
      // to the polyfill somebody would otherwise reach for.
      setupFiles: ["./src/test/setup.ts"],

      // Tests live beside what they test, under src/. Stated rather than left
      // to the default, which sweeps the whole package: scripts/ is a build
      // tool run by npm and has no business being collected by the runner.
      include: ["src/**/*.test.{ts,tsx}"],

      // An empty suite is a broken suite. Deleting every test, breaking the
      // glob above or moving src/ must not produce a green tick that asserts
      // nothing, because green is exactly what nobody looks into.
      //
      // This is Vitest 4's default as well — measured, not assumed: with the
      // line removed, a run matching no files still exits 1. It is written
      // down because the property is a decision and a default is not, and
      // because the two ways of losing the suite fail differently and it is
      // worth knowing both do: no matching file at all is "No test files
      // found, exiting with code 1", and a .test.ts left in place with its
      // tests removed fails as a file with no suite in it.
      //
      // What it does not do is lock anything. `vitest run --passWithNoTests`
      // still exits 0, because the CLI wins over the config — so the thing
      // actually keeping this true is that neither package.json nor
      // .github/workflows/ci.yml passes that flag.
      passWithNoTests: false,

      // Vitest stubs every CSS import as an empty string by default, and it
      // does so by matching the file extension *including the query* — so
      // `import styles from "./styles.css?raw"` comes back empty rather than
      // as the file. That is normally what you want; here it silently disarms
      // the two tests that read the palette out of the stylesheet, which are
      // the only mechanical check that the palette is closed.
      //
      // Narrowed to the one file rather than switched on wholesale: `css: true`
      // would put Tailwind's compiler on the path of any test that imports a
      // stylesheet, for no benefit — nothing here asserts on rendered CSS, and
      // jsdom computes none anyway.
      css: { include: [/styles\.css/] },
    },
  };
});
