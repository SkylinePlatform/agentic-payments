/**
 * The compile-time lock on the rule `src/test/node-fs.d.ts` records: `process`
 * does not exist in this program, so a component reaching for `process.env`
 * fails to compile. This file fails the build the moment that stops being true.
 *
 * # Why the configuration alone is not enough
 *
 * `tsconfig.app.json` keeps Node's globals out of `src/**` with two settings
 * that have to agree — `types: []`, and a `typeRoots` whose *first* entry is the
 * empty `node` stand-in in `src/test/types/`. Neither is self-checking, and the
 * ordering is the sharp one. `@types/node` is a real devDependency of this
 * package — `tsconfig.node.json`'s program is granted it deliberately — so the
 * real package sits in the second `typeRoots` entry, one position behind the
 * stand-in. Swapping the two, the kind of edit somebody makes to tidy an array,
 * resolves `vite`'s own `/// <reference types="node" />` to the real package
 * instead: every Node global becomes visible to every component, `tsc` stays
 * silent, `make frontend-check` and CI both stay green, and the rule goes on
 * reading as enforced while enforcing nothing.
 *
 * That is measured rather than feared. With the two entries swapped and nothing
 * else changed, a `process.env` probe in `src/lanes/` compiles.
 *
 * # How it fires
 *
 * `@ts-expect-error` is itself an error when the line beneath it has *no* error.
 * So the moment anything makes `process` resolve here, the build fails with
 * `TS2578: Unused '@ts-expect-error' directive`, pointing at this file — in
 * `npm run typecheck`, in `npm run build`, and in CI's `Contracts` job, which
 * runs the second.
 *
 * **If that error is what brought you here, the directive is not the bug.**
 * Something has put `@types/node` — or another package declaring Node's globals
 * — into `tsconfig.app.json`'s program, and deleting this line hides it again
 * rather than fixing it. #222 is the whole story; `src/test/types/node/index.d.ts`
 * is the mechanism it turns on.
 *
 * The property is asserted rather than the mechanism, which is what makes this
 * worth more than a test that reads `tsconfig.app.json` back. A future `vite`
 * whose declarations reference some *other* `@types` package — one the stand-in
 * does not intercept, because interception is by name — would reopen the same
 * escape with the configuration untouched and every existing check green. This
 * asks only whether `process` is reachable, so it fails for that too.
 *
 * One global rather than five, because there is one realistic way any of them
 * arrives: `@types/node` reaching this program, and that package declares
 * `process` alongside `Buffer`, `require`, `global` and `__dirname`.
 *
 * Being a `.ts` and not a `.d.ts` is load-bearing. `skipLibCheck` is on, so
 * TypeScript does not check declaration files at all — the identical canary in a
 * `.d.ts` passes whether the escape is open or shut. Also measured, and the
 * first shape this was written in.
 */

// @ts-expect-error `process` must not resolve in this program — see above.
export type ProcessIsNotAGlobalHere = typeof process;
