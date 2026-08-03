## What

<!-- What does this change do? -->

## Why

<!-- Reasoning and alternatives considered. This is the part worth writing well —
     it is what makes the PR usable later as article material. -->

## Protocol reference

<!-- Link the spec section this implements, e.g. AP2 §Mandates / RFC 9421 §3.1 -->

## Checklist

- [ ] `core/` does not import from `adapters/`
- [ ] No LLM call in a signing or verification path
- [ ] No code copied or derived from Visa's TAP sample repository
- [ ] Tests do not depend on a live model or network
- [ ] Golden vectors added or updated if mandate construction changed

Closes #
