import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
// Tailwind v4 has no tailwind.config.js and no PostCSS step. The theme lives in
// src/styles.css, and this plugin is the whole of the build integration.
import tailwindcss from "@tailwindcss/vite";

/**
 * The oldest Node this package runs on, refused here rather than downstream.
 *
 * `engines` in package.json is the declaration and `.npmrc`'s `engine-strict`
 * makes npm act on it — but only on **install**. npm does not check `engines` on
 * `npm run`, which was measured rather than assumed: with the range temporarily
 * set to `^99.0.0`, `npm run typecheck` ran to completion and exited 0. So a tree
 * installed under a supported Node and then run under an older one gets no
 * warning from npm at any point.
 *
 * That gap matters because of `execArgv` in the `test` block below.
 * `--no-experimental-webstorage` is a *bad option* on a Node that never had
 * `--experimental-webstorage` to negate, so every forked worker exits before it
 * loads a test and Vitest reports `Worker exited unexpectedly` once per test
 * file. Measured on Node 20.19.5: **360 seconds, 44 errors, no tests run**, and
 * nothing in that output names a Node version or an option. It is the worst error
 * message in this package, and `nvm use 20 && npm test` over an existing
 * `node_modules` is all it takes to reach it.
 *
 * Two lines instead, in the file the runner loads first. This is the argument
 * `backend/internal/core/generated/doc.go` makes one language across: fail in the
 * place that can say why, rather than at the first thing downstream that happens
 * to notice.
 *
 * # The floor is a minor version, and comparing majors alone would miss the case
 *
 * `--experimental-webstorage` arrived in Node **22.4.0**, so the option this file
 * passes is a bad option on 22.0 through 22.3 exactly as it is on Node 20 —
 * measured on v22.3.0, which produces the same `Worker exited unexpectedly` with
 * no version and no option named. A check on the major alone reads as covering
 * "Node 22" and covers the four releases of it that cannot run this suite, which
 * `.nvmrc` makes reachable rather than theoretical: it names the **line**, so
 * `nvm use` takes the newest 22.x already installed, not the newest that exists.
 * The floor here is therefore `engines`' own — 22.13, jsdom's — and the two are
 * compared as a pair.
 *
 * # Why the version is read off `globalThis` rather than from `process` directly
 *
 * Because `src/topology.test.ts` imports this file, and that puts it in
 * `tsconfig.app.json`'s program as well as `tsconfig.node.json`'s. The app
 * program is the one with `types: []` and a `node` stand-in ahead of
 * `@types/node` in `typeRoots`, so a bare `process` here is `TS2591: Cannot find
 * name 'process'` in `npm run typecheck` — measured, and the first shape this
 * check was written in. Adding the types back is not the fix: keeping `process`
 * out of every component is what #222 built and what
 * `src/test/no-node-globals.ts` fails the build to protect.
 *
 * So the global is asked for structurally, which needs no Node types in either
 * program and reads as what it is — a host that may or may not have a `process`.
 * A host without one is let through rather than refused on a version it does not
 * have, and so is a version string that does not parse: every comparison against
 * `NaN` is false, which points the one direction a guard can fail safely. This
 * file is never bundled into a browser; that is the reason the cast says `?`
 * rather than a reason it can be dropped.
 *
 * Exported because a claimed prevention has to be provable by breaking it, and
 * the thing to break here is a comparison rather than the module: re-importing
 * this file under a stubbed `process` would re-evaluate Vite and esbuild with it.
 * `src/test/node-floor.test.ts` drives the function over the versions that
 * matter, 22.3.0 among them. Vite reads the default export and ignores this one.
 */
const OLDEST_NODE = [22, 13] as const;

export function refuseUnsupportedNode(version: string | undefined): void {
  if (version === undefined) return;
  const [major, minor] = version.split(".").map(Number);
  const tooOld = major < OLDEST_NODE[0] || (major === OLDEST_NODE[0] && minor < OLDEST_NODE[1]);
  if (!tooOld) return;
  throw new Error(
    `This package needs Node ${OLDEST_NODE[0]}.${OLDEST_NODE[1]} or newer and is running on ` +
      `v${version}. See .nvmrc and \`engines\` in frontend/package.json. Node 20 reached end of ` +
      "life on 2026-04-30 and was dropped in #269: the test worker is started with " +
      "--no-experimental-webstorage, which a Node before 22.4 refuses as a bad option before " +
      "running a single test.",
  );
}

const runningOn = (globalThis as { process?: { versions?: { node?: string } } }).process?.versions
  ?.node;
refuseUnsupportedNode(runningOn);

/**
 * The collector's default listen address, matching `-addr` in
 * backend/cmd/collector. Overridable with VITE_COLLECTOR_URL so the demo runner
 * can move it without editing this file.
 */
const DEFAULT_COLLECTOR = "http://127.0.0.1:8085";

/**
 * The Shopping Agent's console API, matching `-addr` on the `agent` entry
 * in deploy/demo.json. Overridable with VITE_AGENT_URL, in VITE_COLLECTOR_URL's
 * shape and for the same reason.
 */
const DEFAULT_AGENT = "http://127.0.0.1:8086";

/**
 * The Trusted Surface, matching `-addr` on the `surface` entry in
 * deploy/demo.json. Overridable with VITE_SURFACE_URL.
 */
const DEFAULT_SURFACE = "http://127.0.0.1:8084";

/**
 * defineConfig comes from vitest/config rather than vite so that the `test`
 * block below type-checks; loadEnv still comes from vite, which is the only
 * one of the two that exports it. Vitest reads this file, so the tests run
 * against the same resolution and plugins as the app — one config, not two
 * that can disagree about what a `../protocol` import means.
 */
export default defineConfig(({ mode }) => {
  // "." rather than process.cwd(): tsconfig.node.json, which is what
  // type-checks this file, does grant Node's globals deliberately (see #222),
  // but reaching for process here would still be reaching for something this
  // file does not otherwise need, for no benefit over the relative path Vite
  // already resolves against its own working directory.
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
  const surface = env.VITE_SURFACE_URL ?? DEFAULT_SURFACE;

  return {
    plugins: [react(), tailwindcss()],
    server: {
      port: 5173,
      // Fail rather than silently moving to another port. The demo runner
      // prints a URL, and a dev server that quietly picked 5174 makes that
      // URL wrong.
      strictPort: true,

      // And bind the address that URL names, for the same reason one line up.
      //
      // Vite's default is `localhost`, which is a *name* — and Node ≥ 17
      // resolves with `verbatim: true`, so it no longer reorders results to put
      // IPv4 first. `server.listen(5173, "localhost")` binds whichever address
      // the resolver returns first and only that one, so on a machine where
      // `localhost` resolves to ::1 first the dev server is IPv6-only.
      // `deploy/demo.json` probes `http://127.0.0.1:5173/`, nothing is
      // listening there, and `make demo` reports the frontend failed for 30
      // seconds while the page it is serving works in a browser: issue #267.
      // The proxy targets below have always been explicit `127.0.0.1`; this is
      // the same address stated for what this server *listens on* rather than
      // only for what it connects to.
      //
      // `host: true` would satisfy the probe too, and must not be what closes
      // this: it binds 0.0.0.0 — every interface — so the dev server, its proxy
      // to the Trusted Surface and its proxy to a console that spends open
      // mandates would all become reachable from the network. Vite's default is
      // loopback-only and that stays true. `127.0.0.1` is the one value that is
      // deterministic and loopback-only both, and src/topology.test.ts refuses
      // anything that is not.
      host: "127.0.0.1",

      // `server.fs.allow` is deliberately left at its default, which confines
      // the dev server to this package. The SD-JWT conformance vectors live in
      // `backend/pkg/sdjwt/testdata` and `src/sdjwt/golden.test.ts` compares
      // against them, but it reads them from disk in Node rather than importing
      // them — so nothing here needs widening. A fixture that only a test reads
      // must not buy itself HTTP surface area on every developer's machine;
      // `src/test/node-fs.d.ts` carries the full argument.
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

        // The Trusted Surface's authorisation routes. Same-origin for the
        // reason /watches is: `Idempotency-Key` is not a simple request, so the
        // browser preflights, and `transport.Idempotency` treats OPTIONS as
        // safe and hands it to a mux with no handler for it, which answers 405.
        // CORS would therefore not be a header on one route but a change to
        // middleware every role runs — including a process holding the user's
        // signing key — to serve one browser in one dev setup.
        //
        // The prefix covers /authorise, /authorise/preview and
        // /authorise/refused. No rewrite: the path the browser asks for is the
        // path the surface serves.
        "/authorise": {
          target: surface,
          changeOrigin: true,
        },

        // The agent's discovery and menu routes, alongside /watches above and
        // for the same reason.
        //
        // `/interpret` and `/candidates` are the two halves `/proposals` became
        // for a browser — issue #299 — and all three are listed because all three
        // are served: `cmd/agent`'s `-buy` and the watch path still call the
        // single one, so it stays reachable here rather than being replaced.
        //
        // **A missing entry here is why the agent answers a stale reading with
        // `410` rather than `404`.** Vite answers a request it has no proxy rule
        // for from the app itself, and the browser has to be able to tell *this
        // reading has expired* from *this path was never wired up*. See
        // `READING_GONE` in `src/consent/client.ts`.
        "/proposals": { target: agent, changeOrigin: true },
        "/interpret": { target: agent, changeOrigin: true },
        "/candidates": { target: agent, changeOrigin: true },
        "/examples": { target: agent, changeOrigin: true },
      },
    },

    test: {
      // These are React components, and there is no DOM to render them into
      // under Vitest's default `node` environment.
      environment: "jsdom",

      // Node's own Web Storage, off, so that jsdom's is what the suite gets.
      //
      // Node 26 is the first release to put `localStorage` and `sessionStorage`
      // on the global object without a flag, and Vitest will not overwrite a
      // global the host already defines unless the name is on its allow-list of
      // WebIDL interfaces — `getWindowKeys`, `if (k in global) return
      // keysArray.includes(k)`. `Storage` is on that list. `localStorage` and
      // `sessionStorage` are not; the string does not appear anywhere in
      // node_modules/vitest/dist. So on Node 26 jsdom's two storages are built
      // and then dropped, and the tests get Node's: `sessionStorage` in-memory
      // and working, `localStorage` file-backed and `undefined` without
      // `--localstorage-file`. That is how #269 read — fourteen theme tests
      // failing in `afterEach` on `localStorage.clear()` rather than on anything
      // they were asserting, with `tsc` silent throughout because the DOM lib
      // types `localStorage` as a `Storage` that always exists.
      //
      // With Node's implementation off, `'localStorage' in globalThis` is false
      // when Vitest looks, the filter falls through, and jsdom's own is
      // installed — the same path Node 22 and 24 already take, where this flag is
      // accepted and does nothing. Nothing here wants Node's Web Storage: the app
      // runs in a browser and the tests run in jsdom.
      //
      // **Not `--localstorage-file`**, which would make Node's `localStorage`
      // work and leave the two halves in different realms. That state is
      // observable on Node 26 today: the global `Storage` is jsdom's, so
      // `sessionStorage instanceof Storage` is already false. src/theme/
      // noflash.test.ts spies on `Storage.prototype.getItem` to prove the
      // no-flash script survives a storage that throws, and a spy on a prototype
      // the object under test does not use passes without ever making storage
      // throw. src/test/webstorage.test.ts is what holds that line, and deleting
      // this option turns it red on Node 26 before the theme suite gets there.
      //
      // It is also why `engines` in package.json starts at 22.13: Node 20 has no
      // `--experimental-webstorage` to negate and refuses to start at all — `bad
      // option`, no tests run. Node 20 reached end of life on 2026-04-30.
      execArgv: ["--no-experimental-webstorage"],

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
