import { describe, expect, it } from "vitest";

import { UNREADABLE } from "../status/model";
import { RUN_STATE_META, RUN_STATES, runStatus } from "./model";

describe("run status", () => {
  it.each(RUN_STATES)("recognises %s", (state) => {
    expect(runStatus(state)).toEqual(RUN_STATE_META[state]);
  });

  it("never renders an unknown run state as blank", () => {
    // The state this console tells is runState.String() from
    // internal/agent/console/run.go — "watching", "bought", "exhausted",
    // "expired", "stopped", "failed" — and nothing stops a future seventh
    // state shipping on the agent before this frontend has been rebuilt
    // against it. A lookup with a silent fallback would draw that row with
    // nothing on it; this one has to say, visibly, that it does not
    // recognise the word.
    const status = runStatus("awaiting_something_new");
    expect(status.raw, "the raw word travels, so the row is debuggable rather than mute").toBe(
      "awaiting_something_new",
    );
    expect(status.label, "and a sentence beside it, because a bare wire value is not an answer").toBe(
      UNREADABLE,
    );
  });

  it("draws a run state it cannot read as a gap in the reader, never as a verdict", () => {
    // #191's third row. `?` in `broken` said a verifier refused, and nothing
    // refused anything: an unrecognised status is this build not knowing a
    // word. Drawing it as a refusal converts "I cannot read this" into "this
    // was rejected", on a purchase that may well have succeeded — the same
    // failure AGENTS.md describes for a constraint nobody understands, one
    // layer up.
    const status = runStatus("hibernating");
    expect(
      status.ending,
      "no ending mark, because an ending says how something closed and this build " +
        "does not know that it did",
    ).toBeNull();
    expect(status.pip, "and no pip, because a pip says how far along and this build cannot tell").toBeNull();
  });

  it("is genuinely exhaustive — every declared state has a table entry, not just the ones somebody remembered", () => {
    expect(Object.keys(RUN_STATE_META).sort()).toEqual([...RUN_STATES].sort());
  });
});
