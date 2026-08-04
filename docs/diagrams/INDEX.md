# Diagram Index

Every diagram has exactly one home: the document that explains it. This file
is an index, not a store — it does not hold diagrams itself, it points at
where each one lives and what it renders to.

`make diagrams` produces the exported column. Those files are gitignored:
they are a build artefact of the documents in the second column, and letting
them drift into version control would add a class of file that can silently
go stale against the source it came from. Run `make diagrams` to regenerate
them locally.

| Diagram | Owning document | Exported file |
|---|---|---|
| The three-layer model | [docs/architecture/README.md](../architecture/README.md) | `docs/diagrams/README-1.svg` |
| Module dependencies | [docs/architecture/README.md](../architecture/README.md) | `docs/diagrams/README-2.svg` |
| The agentic boundary | [docs/architecture/README.md](../architecture/README.md) | `docs/diagrams/README-3.svg` |
| What breaks today | [docs/business/problem-statement.md](../business/problem-statement.md) | `docs/diagrams/problem-statement-1.svg` |
| The built scenario | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/use-cases-1.svg` |
| Human Present retail purchase | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/use-cases-2.svg` |
| Subscription | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/use-cases-3.svg` |
| B2B procurement | [docs/business/use-cases.md](../business/use-cases.md) | `docs/diagrams/use-cases-4.svg` |
| The boundary | [docs/business/what-this-proves.md](../business/what-this-proves.md) | `docs/diagrams/what-this-proves-1.svg` |
| The five roles | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/ap2-1.svg` |
| Human Present | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/ap2-2.svg` |
| Human Not Present | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/ap2-3.svg` |
| Binding: `checkout_hash` | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/ap2-4.svg` |
| Constraints | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/ap2-5.svg` |
| Selective disclosure | [docs/protocols/ap2.md](../protocols/ap2.md) | `docs/diagrams/ap2-6.svg` |
| The handshake | [docs/protocols/tap.md](../protocols/tap.md) | `docs/diagrams/tap-1.svg` |
| Registry resolution | [docs/protocols/tap.md](../protocols/tap.md) | `docs/diagrams/tap-2.svg` |
| Signature base construction | [docs/protocols/tap.md](../protocols/tap.md) | `docs/diagrams/tap-3.svg` |

Eighteen diagrams, six owning documents. The exported filename is
`<document-basename>-<n>.svg`, numbered in the order the mermaid blocks
appear in that document — see the `diagrams` target in the `Makefile`.
