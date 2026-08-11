import { describe, expect, it } from "vitest";

import {
  MANDATE_STATE_META,
  MANDATE_STATES,
  mandateStatus,
  RUN_STATE_META,
  RUN_STATES,
  runStatus,
} from "./model";

describe("run status", () => {
  it.each(RUN_STATES)("recognises %s", (state) => {
    expect(runStatus(state)).toEqual(RUN_STATE_META[state]);
  });

  it("never renders an unknown run state as blank", () => {
    // The state this console tells is runState.String() from
    // internal/agent/console/run.go — "watching", "bought", "exhausted",
    // "stopped", "failed" — and nothing stops a future sixth state shipping on
    // the agent before this frontend has been rebuilt against it. A lookup
    // with a silent fallback would draw that row with nothing on it; this one
    // has to say, visibly, that it does not recognise the word.
    const status = runStatus("awaiting_something_new");
    expect(status.label, "the raw word travels, so the row is debuggable rather than mute").toContain(
      "awaiting_something_new",
    );
    expect(status.icon, "an icon is still required — see the totality rule this is the runtime half of").not.toBe("");
    expect(
      status.tone,
      "a state nobody told this build about is drawn as loud as a real failure, never as though nothing happened",
    ).toBe("negative");
  });

  it("is genuinely exhaustive — every declared state has a table entry, not just the ones somebody remembered", () => {
    expect(Object.keys(RUN_STATE_META).sort()).toEqual([...RUN_STATES].sort());
  });
});

describe("mandate status", () => {
  it.each(MANDATE_STATES)("recognises %s, spelled exactly as authz.MandateState.String() spells it", (state) => {
    expect(mandateStatus(state)).toEqual(MANDATE_STATE_META[state]);
  });

  it("never renders an unknown mandate state as blank", () => {
    const status = mandateStatus("revoked");
    expect(status.label).toContain("revoked");
    expect(status.tone).toBe("negative");
  });

  it("is genuinely exhaustive", () => {
    expect(Object.keys(MANDATE_STATE_META).sort()).toEqual([...MANDATE_STATES].sort());
  });
});
