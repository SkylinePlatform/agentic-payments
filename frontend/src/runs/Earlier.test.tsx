import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Earlier } from "./Earlier";
import type { RunSummary } from "./model";

function aRun(over: Partial<RunSummary> = {}): RunSummary {
  return {
    id: "w1",
    correlation_id: "c-1",
    typed: "buy a ladder under 200",
    item: "ladder",
    title: "Telescopic ladder",
    quantity: 1,
    expires_at: "2026-09-01T10:00:00Z",
    state: "watching",
    attempts: 3,
    ...over,
  };
}

function serving(watches: readonly RunSummary[] | unknown) {
  vi.stubGlobal("fetch", (url: string) => {
    if (url !== "/watches") {
      return Promise.resolve(new Response(`not stubbed: ${url}`, { status: 404 }));
    }
    return Promise.resolve(
      new Response(JSON.stringify({ watches }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("earlier purchases", () => {
  it("draws nothing at all until there are two runs", async () => {
    // The one run is the purchase the person has just finished watching, and a
    // heading reading "Earlier purchases" over it is the clutter this component
    // exists to have removed. The mandate tracker it replaced drew a heading
    // over an empty table, which is the same failure one row down.
    serving([aRun()]);
    render(<Earlier onOpen={() => {}} />);

    // `findBy` rather than `queryBy`, and that is the whole of what makes this
    // test mean anything. The fetch resolves on a later tick, so *nothing* is on
    // screen at the moment of the first render — a synchronous assertion, and a
    // poll that stops the instant it is satisfied, both pass against a component
    // that goes on to draw the list a millisecond later. This one waits for the
    // element and requires that the wait time out. Written against the mutation
    // `runs.length < 1`, which the two forms above pass.
    await expect(
      screen.findByTestId("earlier"),
      "one run is the purchase just finished, and a heading over it is the " +
        "clutter this component exists to have removed",
    ).rejects.toThrow();
  });

  it("lists every run once there are two, newest first", async () => {
    serving([
      aRun({ id: "w1", correlation_id: "c-1", title: "Telescopic ladder" }),
      aRun({ id: "w2", correlation_id: "c-2", title: "Vitesse Urbain 7", state: "bought" }),
    ]);
    render(<Earlier onOpen={() => {}} />);

    const rows = await screen.findAllByRole("button");
    expect(
      rows.map((row) => row.textContent),
      "the run a person is most likely to want back is the one they just left, " +
        "which is the order the attempts inside one run are drawn in too",
    ).toEqual([expect.stringContaining("Vitesse Urbain 7"), expect.stringContaining("Telescopic ladder")]);
  });

  it("opens the run whose row was pressed, not the newest", async () => {
    // The mutation this is written against: `onOpen(newestFirst[0])` reads
    // correctly, passes every count and every ordering assertion above, and
    // sends a reader to somebody else's purchase.
    const opened: string[] = [];
    serving([
      aRun({ id: "w1", correlation_id: "c-older", title: "Telescopic ladder" }),
      aRun({ id: "w2", correlation_id: "c-newer", title: "Vitesse Urbain 7" }),
    ]);
    render(
      <Earlier
        onOpen={(run) => {
          opened.push(run.correlation_id);
        }}
      />,
    );

    await userEvent.click(await screen.findByRole("button", { name: /Telescopic ladder/ }));
    expect(opened, "the correlation id of the row that was pressed").toEqual(["c-older"]);
  });

  it("shows the phrase that was searched for when the merchant named nothing", async () => {
    // #242's rule one screen along: `title` is the merchant's own word and
    // `item` is what the constraint carries. Where there is no title, the
    // search is shown *as* a search — quoted — rather than dressed up as a
    // product this shop sells.
    serving([
      aRun({ id: "w1", correlation_id: "c-1", title: "", item: "ladder" }),
      aRun({ id: "w2", correlation_id: "c-2", title: "Vitesse Urbain 7" }),
    ]);
    render(<Earlier onOpen={() => {}} />);

    expect(
      await screen.findByText("“ladder”"),
      "quoted, because this is the phrase somebody typed and not a name anybody gave",
    ).toBeTruthy();
  });

  it("says nothing when the console cannot be reached", async () => {
    // A way back to something reachable anyway — by buying again, or by the
    // address the lanes wrote. An error banner here would report a console
    // being down on a screen whose own console call has its own place to say so.
    vi.stubGlobal("fetch", () => Promise.reject(new Error("connection refused")));
    render(<Earlier onOpen={() => {}} />);

    await expect(screen.findByTestId("earlier")).rejects.toThrow();
  });
});
