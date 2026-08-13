# Diagram Index

Every diagram has exactly one home: the document that explains it. This file
is an index, not a store — it does not hold diagrams itself, it points at
where each one lives and what it renders to.

`make diagrams` produces the exported column. Those files are gitignored:
they are a build artefact of the documents in the second column, and letting
them drift into version control would add a class of file that can silently
go stale against the source it came from. Run `make diagrams` to regenerate
them locally.

The exported name is the document's path under `docs/` with the separators
flattened, rather than its file name. Two documents called `README.md` in
different directories would otherwise export to one file and the second would
silently overwrite the first, with nothing in the run saying so — which is what
the root `README.md` did to `docs/architecture/README.md`'s three diagrams the
day it gained a mermaid block. `make diagrams` now refuses a collision instead
of taking it.

The root `README.md` is the one name that is not derived, and it is spelled
`root-README`. Stripping a leading `docs/` would leave it as `README`, which is
also what `docs/README.md` — the documentation index — flattens to, so the two
would collide the moment either gained a diagram. The guard would catch it, and
catching it is worse than not being able to write it: the failure would arrive
for whoever next edited an unrelated document, naming two files neither of them
had touched.

| Diagram | Owning document | Exported file |
|---|---|---|
| What breaks when an agent buys | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-1.svg` |
| The mandate lifecycle, and the refusal edge | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-2.svg` |
| Two mandates, and who verifies each | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-3.svg` |
| Open and closed | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-4.svg` |
| Who sees what | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-5.svg` |
| The TAP handshake at the merchant edge | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-6.svg` |
| Unknown agent against unverified agent | [README.md](https://github.com/SkylinePlatform/agentic-payments/blob/main/README.md) | `docs/diagrams/root-README-7.svg` |
| The three-layer model | [docs/architecture/README.md](../architecture/README.md) | `docs/diagrams/architecture-README-1.svg` |
| Module dependencies | [docs/architecture/README.md](../architecture/README.md) | `docs/diagrams/architecture-README-2.svg` |
| The agentic boundary | [docs/architecture/README.md](../architecture/README.md) | `docs/diagrams/architecture-README-3.svg` |
| What breaks today | [docs/business/problem-statement.md](../business/problem-statement.md) | `docs/diagrams/business-problem-statement-1.svg` |
| The built scenario | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/business-use-cases-1.svg` |
| Human Present retail purchase | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/business-use-cases-2.svg` |
| Subscription | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/business-use-cases-3.svg` |
| B2B procurement | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/business-use-cases-4.svg` |
| The boundary | [docs/business/what-this-proves.md](../business/what-this-proves.md) | `docs/diagrams/business-what-this-proves-1.svg` |
| The five roles | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/protocols-ap2-1.svg` |
| Human Present | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/protocols-ap2-2.svg` |
| Human Not Present | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/protocols-ap2-3.svg` |
| Binding: `checkout_hash` | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/protocols-ap2-4.svg` |
| Constraints | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/protocols-ap2-5.svg` |
| Selective disclosure | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/protocols-ap2-6.svg` |
| The handshake | [docs/protocols/tap.md](../protocols/tap.md) | `docs/diagrams/protocols-tap-1.svg` |
| Registry resolution | [docs/protocols/tap.md](../protocols/tap.md) | `docs/diagrams/protocols-tap-2.svg` |
| Signature base construction | [docs/protocols/tap.md](../protocols/tap.md) | `docs/diagrams/protocols-tap-3.svg` |

Twenty-five diagrams, seven owning documents. The exported filename is
`<document-path-under-docs-with-dashes>-<n>.svg`, numbered in the order the
mermaid blocks appear in that document — see the `diagrams` target in the
`Makefile`.

**The numbering is positional**, which matters for the root README more than for
the others: it is the document the article series and any deck are built from,
so a diagram inserted above another renumbers everything below it and silently
breaks whatever already pointed at the old numbers. That is why the Visa TAP
section is last in that document and not in protocol order — a chapter that
grows when the milestone lands appends rather than renumbers.
