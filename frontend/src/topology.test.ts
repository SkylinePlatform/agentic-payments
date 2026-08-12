/**
 * @vitest-environment node
 *
 * The one file in the package that opts out of jsdom, and it has to.
 *
 * This suite imports `vite.config.ts`, which pulls in Vite and therefore
 * esbuild, and esbuild asserts at load time that
 * `new TextEncoder().encode("") instanceof Uint8Array`. Under jsdom that is
 * **false** — jsdom supplies its own `TextEncoder` from a different realm than
 * the `Uint8Array` esbuild compares against — so the import fails the whole
 * file with "your JavaScript environment is broken", three layers from anything
 * this test is about. Nothing here renders a component, so there is no DOM to
 * give up.
 *
 * The pragma has to sit in the first comment block in the file, which is why
 * this one is above the imports and the description of the subject is below
 * them.
 */
import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import config from "../vite.config";

/**
 * Where the dev server listens, against where `make demo` looks for it.
 *
 * The address is stated in two files — `vite.config.ts` binds it and
 * `deploy/demo.json` probes it — and issue #267 is what happens when they
 * disagree: Vite's default `server.host` is the *name* `localhost`, Node ≥ 17
 * resolves names with `verbatim: true`, and on a machine where `localhost`
 * resolves to ::1 first the dev server bound `[::1]:5173` while the runner
 * probed `http://127.0.0.1:5173/`. Nothing was listening there, so `make demo`
 * reported the frontend failed for thirty seconds while the page it was serving
 * worked in a browser.
 *
 * That failure depended on the resolver of whoever ran the command, which is
 * the class this repository keeps closing: a demo reporting two results on two
 * machines running the identical command is a screenshot nobody can attribute.
 * The port has always been stated in both files with a note that it "has to
 * move in both"; this is the same property for the host, mechanised rather than
 * left to that note.
 *
 * It lives at the top of `src/` beside `palette.test.ts` because it governs the
 * package rather than a module in it, and it is not in `architecture.test.ts`
 * because that file is the frontend's depguard — import arrows and the closed
 * palette — and this is neither.
 */

/**
 * The demo topology, as `internal/demo`'s runner reads it.
 *
 * Read from disk rather than imported, on `src/test/node-fs.d.ts`'s argument:
 * a module outside the package root is reachable only through the dev server's
 * `/@fs/` escape hatch, which has to be opened by widening `server.fs.allow` —
 * and that list governs what the dev server serves to a *page*, so importing
 * this would buy HTTP surface area on every developer's machine to solve a
 * problem that never leaves Node.
 *
 * The path is bound to a constant first rather than written inline, and that is
 * load-bearing rather than style: Vite's asset transform only fires on a string
 * *literal*, so an identifier it cannot analyse statically is left alone and
 * `new URL` evaluates to the plain `file:` URL `readFileSync` accepts. Passed
 * inline it would come back as `http://localhost:3000/@fs/…`, which
 * `readFileSync` refuses.
 */
const MANIFEST = "../../deploy/demo.json";

/** The fields of a manifest entry this file reads. The runner reads more. */
interface Entry {
  name: string;
  health?: string;
  url?: string;
}

const manifest = JSON.parse(readFileSync(new URL(MANIFEST, import.meta.url), "utf8")) as {
  processes: Entry[];
};

const entry = manifest.processes.find((p) => p.name === "frontend");

/**
 * The config as `npm run dev` resolves it.
 *
 * `development` rather than the `test` mode Vitest itself runs this file under,
 * because the dev server `make demo` starts is the subject: the mode selects
 * which `.env` files `loadEnv` reads, and reading the ones the demo reads is
 * what makes the answer here the demo's answer.
 */
const resolved = await config({ command: "serve", mode: "development" });
const server = resolved.server;

describe("the dev server and the demo manifest", () => {
  // The guard the rules below need. Every assertion after this one is derived
  // from `entry`, and a manifest whose frontend entry was renamed would leave
  // them comparing `undefined` against `undefined` — green, having checked
  // nothing. This is the one that turns that into a failure.
  it("are both being read — the manifest has a frontend entry with an address in it", () => {
    expect(entry, `deploy/demo.json has no process named "frontend", so nothing below is checking anything`).toBeDefined();
    expect(entry?.health, "the entry states no health URL, so the runner has nothing to probe").toBeTruthy();
    expect(entry?.url, "the entry states no URL, so the banner has nothing to print").toBeTruthy();
    expect(server?.host, "vite.config.ts leaves server.host at Vite's default, which is the name `localhost` and resolver-dependent — this is issue #267 exactly").toBeTruthy();
  });

  // Note for whoever moves either side to an IPv6 literal: `URL.hostname`
  // brackets one, so `http://[::1]:5173/` reads back as `[::1]` and this
  // comparison would need to strip them. It is left un-normalised deliberately
  // — both sides are IPv4 today, and a comparison that quietly accepted a form
  // nothing here produces is how the port grew a second spelling.
  it("agree on the address the runner probes", () => {
    const health = new URL(entry?.health ?? "");
    expect(health.hostname, "the runner probes an address the dev server does not bind, which is a thirty-second failure for a server that works").toBe(server?.host);
    expect(Number(health.port), "the runner probes a port the dev server does not listen on").toBe(server?.port);
  });

  // `url` is held to less than `health` is, and the difference between them is
  // the whole subject of this file. The runner probes `health` itself, as an IP
  // literal with no fallback, so that one has to name the bind address exactly.
  // `url` is only ever printed for a person to open — see `Process.URL`'s use in
  // internal/demo/banner.go — and a browser handed a *name* walks the address
  // list, so `localhost` reaches a server bound to 127.0.0.1 alone. Both
  // READMEs tell readers to open exactly that form. Demanding the literal here
  // would fail a value that works, under a message claiming the link was dead.
  //
  // What still has to hold: the port, which no resolver fallback covers, and a
  // host that reaches a loopback bind at all rather than one that does not.
  it("print a URL that reaches the dev server", () => {
    const printed = new URL(entry?.url ?? "");
    expect(Number(printed.port), "the banner prints a port the dev server does not listen on, and no fallback covers a wrong port").toBe(server?.port);
    expect(
      ["127.0.0.1", "localhost"],
      "the banner has to print a host that reaches a loopback bind — the literal itself, or a name whose address list contains it",
    ).toContain(printed.hostname);
  });

  // The regression this one exists for is a *fix* rather than a mistake:
  // `host: true` also makes the probe pass, and it does it by binding 0.0.0.0.
  // That would put the dev server, its proxy to the Trusted Surface and its
  // proxy to a console that spends open mandates on every interface. Vite's
  // default is loopback-only, and the fix for #267 must not be the change that
  // quietly ends that.
  it("bind a loopback address only", () => {
    expect(
      server?.host,
      "`true` and `0.0.0.0` both satisfy the runner's probe by exposing the dev server, and its proxies, to the network",
    ).toBe("127.0.0.1");
  });

  it("keep the port refusing to move on its own", () => {
    expect(
      server?.strictPort,
      "without strictPort a busy 5173 moves the server to 5174 and the address above is a lie again, for the other half of the same reason",
    ).toBe(true);
  });
});
