# Frontend

React + Vite + TypeScript. Three lanes — User, Agent, Merchant — with a live
event log between them, plus the Mandate Inspector and the Trusted Surface
consent screen.

Not scaffolded yet. See issues #20, #21 and #22.

Protocol types are generated from `contracts/` into `src/protocol/generated`
and are not hand-edited. That generation is the only reason `package.json`
exists today: it pins `json-schema-to-typescript` and nothing else. The React,
Vite and TypeScript toolchain arrives with #20.

```bash
make generate     # from the repository root
```
