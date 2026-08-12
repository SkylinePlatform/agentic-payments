/**
 * @vitest-environment node
 *
 * This file imports `vite.config.ts`, and jsdom is what stops that working:
 * esbuild asserts at load time that `new TextEncoder().encode("") instanceof
 * Uint8Array`, which is false under jsdom because the encoder and the array come
 * from different realms. `src/topology.test.ts` carries the full argument; the
 * pragma has to sit in the first comment block, which is why it is above the
 * description rather than below it.
 */
import { describe, expect, it } from "vitest";

import { refuseUnsupportedNode } from "../../vite.config";

/**
 * The Node floor `vite.config.ts` refuses below, driven over the versions that
 * decide it.
 *
 * The check exists because `execArgv` passes `--no-experimental-webstorage`, and
 * a Node that never had `--experimental-webstorage` to negate treats it as a bad
 * option: every forked worker exits before loading a test and Vitest reports
 * `Worker exited unexpectedly` once per file, naming no version and no option.
 * `#269` bought that check; this is what makes it provable by breaking it, which
 * a comment claiming a prevention is required to be.
 *
 * **22.3.0 is the row that matters.** `--experimental-webstorage` arrived in
 * 22.4.0, so a check on the major alone lets four releases of Node 22 through
 * into exactly the failure the check is for — measured on v22.3.0 before this
 * was a pair comparison, and `.nvmrc` naming the line rather than a patch is
 * what makes it reachable: `nvm use` takes the newest 22.x installed, not the
 * newest that exists.
 *
 * Both directions are here on purpose. A guard that refused everything would
 * pass a table of refusals, so the accepted versions are asserted beside them —
 * the floor exactly, `.nvmrc`'s own line, the two majors above it, and a host
 * with no `process` at all, which must be let through rather than refused on a
 * version it does not have.
 */
describe("the Node floor vite.config.ts enforces", () => {
  it.each([
    ["20.19.5", "end of life on 2026-04-30, and has no flag to negate"],
    ["22.0.0", "Node 22 before Web Storage existed at all"],
    [
      "22.3.0",
      "the last release before --experimental-webstorage, and a major-only check's blind spot",
    ],
    ["22.12.0", "above the flag but below what jsdom's own engines range accepts"],
  ])("refuses v%s — %s", (version) => {
    expect(
      () => refuseUnsupportedNode(version),
      "an unsupported Node has to be named here, where the reason can be stated, " +
        "rather than at the first thing downstream that happens to notice",
    ).toThrow(/needs Node 22\.13 or newer/);
  });

  it.each([
    ["22.13.0", "the floor exactly, which is `engines`' floor and jsdom's"],
    ["22.23.1", "what .nvmrc's `22` resolved to in CI"],
    ["24.16.0", "the next LTS line"],
    ["26.7.0", "what `node-version: current` resolved to in CI, and where #269 was reproduced"],
  ])("accepts v%s — %s", (version) => {
    expect(
      () => refuseUnsupportedNode(version),
      "a guard that refused a supported version would be found by whoever it " +
        "stopped, and only after they had lost the time",
    ).not.toThrow();
  });

  it("lets a host with no process through rather than refusing it", () => {
    // The reason the version is read structurally off `globalThis`: this file is
    // never bundled into a browser, but the check must not be the thing that
    // decides that. Somewhere without a `process` yields `undefined`, and the
    // one safe direction for a version guard with no version is to allow.
    expect(
      () => refuseUnsupportedNode(undefined),
      "a missing version is not an old version, and refusing on it would refuse " +
        "every host that is not Node",
    ).not.toThrow();
  });

  it("lets a version it cannot parse through rather than refusing it", () => {
    // Same direction, one step along: every comparison against NaN is false, so
    // an unrecognised version string is allowed rather than refused. Asserted
    // rather than left to the reader, because it is a property of `<` and not of
    // anything written here — a rewrite to `!(major >= 22)` would invert it.
    expect(
      () => refuseUnsupportedNode("not-a-version"),
      "the failure mode of a version guard should be letting an unknown through, " +
        "not stopping a working install on a string it did not recognise",
    ).not.toThrow();
  });
});
