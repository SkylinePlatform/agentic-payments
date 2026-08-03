#!/usr/bin/env bash
#
# One-time setup for SkylinePlatform/agentic-payments.
#
# Prerequisites:
#   gh auth login          (needs 'repo' scope on the SkylinePlatform org)
#   Run from inside this directory.
#
# Safe to re-run: milestone/label creation failures are tolerated.

set -euo pipefail

ORG="SkylinePlatform"
REPO="agentic-payments"
SLUG="$ORG/$REPO"

echo "==> Creating repository $SLUG"
gh repo create "$SLUG" \
  --public \
  --description "Go implementation of AP2 and Visa TAP behind a single, protocol-neutral authorization model" \
  --homepage "https://$ORG.github.io/$REPO" \
  || echo "    (repo already exists, continuing)"

echo "==> Pushing initial commit"
git init -q 2>/dev/null || true
git add -A
git commit -qm "chore: initial scaffolding, license, and contribution rules" || true
git branch -M main
git remote add origin "https://github.com/$SLUG.git" 2>/dev/null || true
git push -u origin main

echo "==> Setting topics"
gh repo edit "$SLUG" --add-topic ap2 \
  --add-topic agentic-commerce --add-topic agentic-payments \
  --add-topic trusted-agent-protocol --add-topic rfc9421 \
  --add-topic golang --add-topic payments --add-topic sd-jwt

echo "==> Creating labels"
mklabel() { gh label create "$1" --color "$2" --description "$3" --repo "$SLUG" --force >/dev/null; }
mklabel "protocol: ap2"    "4285F4" "Google Agent Payments Protocol"
mklabel "protocol: tap"    "1A1F71" "Visa Trusted Agent Protocol"
mklabel "layer: authz"     "0E8A16" "Authorization — mandates, constraints"
mklabel "layer: identity"  "5319E7" "Agent identity and key management"
mklabel "layer: evidence"  "006B75" "Receipts, audit trail, dispute"
mklabel "layer: crypto"    "B60205" "Signing, SD-JWT, key handling"
mklabel "frontend"         "FBCA04" "React / Vite / TypeScript"
mklabel "docs"             "C5DEF5" "Documentation and diagrams"
mklabel "blocked"          "D93F0B" "Blocked on an external dependency"
mklabel "article"          "E99695" "Produces material for the article series"

echo "==> Creating milestones"
mkmilestone() {
  gh api "repos/$SLUG/milestones" -f title="$1" -f description="$2" -f due_on="$3" >/dev/null \
    || echo "    (milestone '$1' already exists)"
}
mkmilestone "Google AP2" \
  "Full AP2 implementation: closed-mandate core, HP and HNP modes, constraint system, mocked trust anchors." \
  "2026-08-14T23:59:59Z"
mkmilestone "Visa TAP" \
  "RFC 9421 agent identity, local agent registry, verifying proxy, integration into core/identity." \
  "2026-08-28T23:59:59Z"

echo "==> Creating issues"
mkissue() {
  local title="$1"; local milestone="$2"; local labels="$3"; local body="$4"
  gh issue create --repo "$SLUG" --title "$title" --milestone "$milestone" \
    --label "$labels" --body "$body" >/dev/null
  echo "    + $title"
}

# ---------- Milestone 1: Google AP2 ----------
M="Google AP2"

mkissue "Go module scaffolding, Makefile, CI" "$M" "layer: authz" \
"Go module, directory layout per README, golangci-lint with depguard enforcing that core/ never imports adapters/, GitHub Actions running test + lint."

mkissue "Contracts: JSON Schema as single source of truth" "$M" "layer: authz" \
"Define the canonical model in contracts/ as JSON Schema and generate both Go and TypeScript types. Start from AP2's published schemas. This is where the real DRY win is — cross-language, not within-language."

mkissue "Signer interface, key store, JWKS resolution" "$M" "layer: crypto" \
"Signer interface with an in-memory implementation. ECDSA for AP2 (the spec forbids deterministic schemes such as Ed25519 for the Checkout JWT). Injected clock so signature expiry is testable."

mkissue "SD-JWT: issuance, disclosures, verification" "$M" "layer: crypto,article" \
"AP2 secures mandates with SD-JWT. Go tooling here is thinner than in Python/JS — expect to build the disclosure layer on top of go-jose. Largest technical unknown in the milestone, and strong article material."

mkissue "Checkout Mandate (closed) — construction and verification" "$M" "layer: authz" \
"Closed Checkout Mandate bound to the merchant-signed Checkout JWT via cryptographic hash. Verify that the hash of the Checkout JWT matches the checkout_hash claim. Honour the vct version suffix — implementations must match the exact string."

mkissue "Payment Mandate (closed) — construction and verification" "$M" "layer: authz" \
"Payment Mandate bound to the same Checkout via checkout_hash. Verified by Credential Provider, Network and Merchant Payment Processor."

mkissue "Receipts: Checkout Receipt and Payment Receipt" "$M" "layer: evidence" \
"Both are mandatory in the protocol, including on rejection — a rejection must return a receipt carrying the error. The reference must match the hash of the corresponding closed mandate."

mkissue "Role-specific verification rules" "$M" "layer: authz" \
"Implement verification per role: Merchant, Credential Provider and Network, Merchant Payment Processor. All verification in deterministic code — no LLM anywhere in this path."

mkissue "Mock roles: Credential Provider, Merchant, MPP, Trusted Surface" "$M" "layer: authz" \
"Local services implementing the five AP2 roles. The Trusted Surface MUST be non-agentic per spec. The Credential Provider mock is precisely where Skyline sits later, so it is not throwaway work."

mkissue "Human Present flow, end to end" "$M" "layer: authz" \
"Wire the closed-mandate core into the direct flow: agent assembles the checkout, Trusted Surface obtains user signature, verification, receipts."

mkissue "Constraint system: type registry, schemas, evaluation" "$M" "layer: authz,article" \
"AP2 treats constraints as an extension point: each needs a unique type, a schema marking selectively-disclosable fields, and an evaluation algorithm. Start with price.max, temporal.window, item.category, merchant.category. Evaluated by the verifier, never by the agent."

mkissue "Open mandates: cnf claim, agent-key signing, TTL" "$M" "layer: authz" \
"Open Checkout and Payment Mandates carrying the agent public key in cnf. Set exp to the smallest value that lets the agent finish the task."

mkissue "Rejection-receipt rule for open mandates" "$M" "layer: authz" \
"An agent must not present a subsequent open mandate before receiving a rejection receipt for the previous one. Prevents approving multiple checkouts against a single open mandate. Easy to miss, security-relevant."

mkissue "Selective disclosure minimisation" "$M" "layer: crypto" \
"Present only those disclosures from the open mandates that the verifier needs to evaluate the closed ones."

mkissue "Human Not Present flow, end to end" "$M" "layer: authz" \
"The primary deliverable. User signs open mandates with constraints; agent later signs closed mandates with its own key; verifier checks the closed against the constraints."

mkissue "IntentInterpreter interface + scripted implementation" "$M" "layer: authz" \
"Natural language to typed constraints, behind an interface. The scripted implementation is what CI uses — no test may depend on a live model."

mkissue "Gemini interpreter with structured output" "$M" "layer: authz" \
"Gemini implementation of IntentInterpreter using structured output constrained to the constraint schema. Provider selection via config so further models drop in without touching call sites."

mkissue "Dispute evidence assembly and verification" "$M" "layer: evidence,article" \
"Bring mandates and receipts together into a non-repudiable picture of the transaction, and implement the dispute-time verification steps. Underexplored publicly and directly relevant to PSPs."

mkissue "Golden test vectors and conformance suite" "$M" "layer: crypto,article" \
"Fixtures for mandate construction and verification. A cross-protocol conformance suite does not currently exist anywhere — worth contributing upstream."

mkissue "Frontend: three-lane view (User / Agent / Merchant)" "$M" "frontend" \
"React + Vite + TypeScript. Three lanes with a live event log between them. Built so screenshots carry the article series."

mkissue "Frontend: Mandate Inspector" "$M" "frontend,article" \
"Decode and display SD-JWT contents, showing which disclosures were revealed and which withheld. Likely the single best screenshot in the demo."

mkissue "Frontend: Trusted Surface consent screen" "$M" "frontend" \
"The user must see and approve the interpreted constraints, not their raw prompt. If the model misread the intent, this is where the user catches it before signing. This is the confirmation gate."

mkissue "Architecture documentation with mermaid diagrams" "$M" "docs" \
"Role diagram, HP and HNP sequence diagrams, the agentic boundary, constraint evaluation flow. Reused directly in the article series."

# ---------- Milestone 2: Visa TAP ----------
M="Visa TAP"

mkissue "RFC 9421 HTTP Message Signatures: signing" "$M" "layer: identity,article" \
"Ed25519 signing per RFC 9421. Covered components include method, target URI, selected headers, timestamp and nonce. Written from the RFC and the published spec — no Visa sample code, see CONTRIBUTING.md."

mkissue "RFC 9421: verification" "$M" "layer: identity" \
"Verification side, including keyId resolution and rejection of malformed or expired signatures."

mkissue "Local agent registry" "$M" "layer: identity" \
"Public key registry with registration and lookup. Visa operates the production directory; this is the local equivalent, which is what makes the POC possible without a Visa account."

mkissue "Nonce store and replay protection" "$M" "layer: identity" \
"Time-bounded single-use signature elements. Shared mechanism with AP2 — a genuine candidate for extraction into platform/."

mkissue "Signature binding to domain and operation" "$M" "layer: identity" \
"Signatures are bound to the merchant domain and to the specific operation, distinguishing browsing from payment, so authorisation cannot be reused elsewhere."

mkissue "Consumer and payment identifier passing" "$M" "layer: identity" \
"Verifiable consumer identifiers and Payment Account References passed to the merchant, subject to consumer consent."

mkissue "Verifying proxy in front of the mock merchant" "$M" "layer: identity" \
"TAP verification happens at the merchant edge — Visa's own reference architecture puts a CDN proxy in front of the merchant. Mirror that shape."

mkissue "Integrate TAP into core/identity" "$M" "layer: identity" \
"Populate the identity axis of the canonical model. core/ must remain free of any import from adapters/."

mkissue "Frontend: TAP handshake visualisation" "$M" "frontend" \
"Show signature construction, verification at the proxy, and accept/reject outcome."

mkissue "TAP architecture documentation" "$M" "docs" \
"Diagrams plus an explicit written comparison of where TAP sits relative to AP2. Note the contrast: TAP uses Ed25519, while AP2 forbids deterministic signatures for the Checkout JWT."

echo "==> Configuring branch protection on main"
gh api -X PUT "repos/$SLUG/branches/main/protection" \
  -H "Accept: application/vnd.github+json" \
  --input - <<'JSON' >/dev/null || echo "    (protection not applied — set it in Settings > Rules)"
{
  "required_status_checks": null,
  "enforce_admins": false,
  "required_pull_request_reviews": {
    "required_approving_review_count": 0,
    "dismiss_stale_reviews": false,
    "require_code_owner_reviews": false
  },
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
JSON

echo "==> Configuring merge settings"
gh repo edit "$SLUG" \
  --enable-squash-merge --enable-merge-commit=false --enable-rebase-merge=false \
  --delete-branch-on-merge

echo
echo "Done: https://github.com/$SLUG"
echo "Issues: https://github.com/$SLUG/issues"
