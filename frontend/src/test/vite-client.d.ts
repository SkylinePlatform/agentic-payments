/**
 * `vite/client`'s ambient declarations — `import.meta.glob`, CSS module
 * types, `?raw` and the other asset-import shapes Vite adds to `ImportMeta`
 * — pulled in by a `path` reference to the exact file, not a `types`
 * reference to the package name. `src/main.tsx`'s plain `import
 * "./styles.css"` needs this as much as any test does, so despite living
 * under `src/test/` this is not test-only; see the note on placement below.
 *
 * # Why not `"types": ["vite/client"]`
 *
 * That is the form Vite's own docs recommend, and it was this project's own
 * form until `tsconfig.app.json`'s `typeRoots` was pinned to keep
 * `@types/node` unreachable from `src/**` — see `src/test/types/node/index.d.ts`
 * for why that pin exists. A `"types"` entry is resolved as a *type reference
 * directive*, the identical mechanism `/// <reference types="node" />` uses,
 * and once `typeRoots` is customised that resolution is confined to the
 * listed directories with no fallback to plain `node_modules`. `vite/client`
 * is not an `@types/*` package; it is `vite`'s own shipped declaration file,
 * findable by default only through the fallback that pinning `typeRoots`
 * switches off — confirmed while diagnosing #222: with `typeRoots` pinned and
 * `"types": ["vite/client"]` still in `compilerOptions`, `tsc` fails the whole
 * program with `TS2688: Cannot find type definition file for 'vite/client'`,
 * before a single file under `src/` is even checked.
 *
 * A `path` reference is a different, older mechanism: a literal file
 * location, resolved the way a relative import specifier is, with no
 * `typeRoots` search involved at all. `node_modules/vite/client.d.ts` is a
 * real file at a fixed location relative to this one — it in turn only ever
 * references its own sibling `./types/importMeta.d.ts` by `path`, never by
 * `types` — so pointing at it directly sidesteps the whole question rather
 * than working around it.
 *
 * # Why this lives under `src/test/`
 *
 * Not because it is test-only — it is not — but because this fix's scope is
 * `frontend/tsconfig*.json`, `frontend/vite.config.ts` and
 * `frontend/src/test/**`, and an ambient declaration's effect does not depend
 * on which directory holds it, only on whether `tsconfig.app.json`'s
 * `include` reaches it. `src` does, so it does its job from here exactly as
 * it would from `src/vite-env.d.ts`, the name Vite's own scaffolding uses.
 */
/// <reference path="../../node_modules/vite/client.d.ts" />
