/**
 * A repo-owned, empty stand-in for `@types/node`, reachable only from
 * `tsconfig.app.json`'s `typeRoots` and listed ahead of `./node_modules/@types`
 * in it. It is never installed by npm and never real — see #222.
 *
 * # The reference this exists to satisfy
 *
 * `src/theme/built.test.ts` imports `{ build } from "vite"` to run a real
 * build and inspect its output; it is the only file under `src/` that imports
 * `vite` itself rather than `vite/client`. `vite`'s own `dist/node/index.d.ts`
 * opens with `/// <reference types="node" />`, and that reference is written
 * *inside a node_modules package*, not in our own source. TypeScript resolves
 * a reference in that position as one package's own dependency on another,
 * the same way it would resolve a value import — by walking every ancestor
 * `node_modules` outward from `vite`'s own location, independently of this
 * project's `typeRoots`.
 *
 * That is the part worth stating plainly: pinning `typeRoots` does **not**
 * stop this particular walk. It stops a bare `/// <reference types="node" />`
 * written in *our* code, and it stops an unqualified `"types"` entry in
 * `compilerOptions` — both checked directly against #222's reproduction — but
 * a reference sitting inside a dependency's own declaration file is resolved
 * as that dependency's problem to solve, not ours, and keeps climbing past
 * this repository regardless of what `typeRoots` says. Left alone, that climb
 * is exactly the bug #222 reports: on a machine with a stray `@types/node`
 * above the repository, the walk finds it, and `process` — along with
 * everything else `@types/node` declares — becomes a program-wide global for
 * every file `tsconfig.app.json` compiles, including every component.
 * `src/test/node-fs.d.ts` is the record of why that must never happen.
 *
 * # Why an empty package here fixes it
 *
 * TypeScript checks each entry in the `typeRoots` list, in order, before it
 * ever starts that ancestor walk, and stops as soon as one of them answers.
 * A package literally named `node` sitting in the *first* `typeRoots` entry
 * therefore intercepts the reference before the walk that would otherwise
 * escape the repository ever begins — verified with `tsc --traceResolution`
 * while diagnosing #222, both with and without a stray `@types/node` present
 * above the repository: the outcome no longer depends on it either way.
 *
 * What keeps that from being a configuration nobody re-checks is
 * `src/test/no-node-globals.ts`, which asserts the *property* this file is one
 * half of the mechanism for: it fails the build if `process` ever resolves in
 * this program, whether because this stand-in was removed, because the
 * `typeRoots` entries were reordered, or because a future `vite` reached for
 * Node's globals by a name this interception does not cover.
 *
 * This file exports nothing and declares nothing global, so the reference is
 * satisfied without granting a single Node symbol. `skipLibCheck` means
 * `vite`'s own `.d.ts` is never checked for internal consistency against
 * whatever `node` resolved to — only resolved — so an empty stand-in is
 * enough to let `built.test.ts` compile. `process`, `Buffer` and the rest
 * stay exactly as unreachable from `src/**` as `node-fs.d.ts` requires.
 *
 * `vite.config.ts` is not covered by this file at all: it sits in
 * `tsconfig.node.json`, a separate TypeScript program with its own
 * `typeRoots` and a real `@types/node`, granted deliberately and confined to
 * that one file. Ambient globals never cross between two separate tsconfig
 * programs the way they cross between files compiled together in one, so
 * that grant cannot leak here.
 */
export {};
