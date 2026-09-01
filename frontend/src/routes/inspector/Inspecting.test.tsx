import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Inspecting } from "./Inspecting";

/**
 * What this file is for, and what it deliberately leaves alone.
 *
 * The decoding, the three disclosure tables and the sentence about holding no
 * verifier's key belong to `inspector/` and `../protocol/Disclosure.tsx`, and
 * their own suites drive them. What is new in #344 is the *address*: this screen
 * exists because an attempt in the lanes links to it, so the property worth
 * pinning is that both halves of the question survive the trip — which purchase,
 * and which attempt of it.
 *
 * `GET /watches` is what turns a correlation id into the watch id the console's
 * detail route wants, so a stub of it is enough to see which attempt was asked
 * for, without a single mandate being decoded.
 */

/** Every `GET /watches/{id}/attempts/{n}/presented` this render made. */
let asked: string[] = [];

function servingOneWatch(correlationId: string) {
  asked = [];
  vi.stubGlobal("fetch", (url: string) => {
    if (url === "/watches") {
      return Promise.resolve(
        new Response(JSON.stringify({ watches: [{ id: "w1", correlation_id: correlationId }] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    }
    if (url.startsWith("/watches/")) {
      asked.push(url);
      return Promise.resolve(new Response("console: nothing to decode", { status: 404 }));
    }
    return Promise.resolve(new Response(`not stubbed: ${url}`, { status: 404 }));
  });
}

function at(search: string) {
  return render(
    <MemoryRouter initialEntries={[`/inspector${search}`]}>
      <Inspecting />
    </MemoryRouter>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the Mandate Inspector's address", () => {
  it("asks the console for the attempt the link named, not for the first", async () => {
    // The mutation this is written against, and the one an eye cannot catch: a
    // screen that ignored `?attempt=` and always asked for 1 draws a full,
    // convincing set of disclosure tables — for the wrong attempt. The digest it
    // prints is the only thing that would say so, and only to a reader who went
    // back to compare it against the spine head they came from.
    servingOneWatch("c-abc");
    at("?run=c-abc&attempt=2");

    await expect.poll(() => asked).toEqual(["/watches/w1/attempts/2/presented"]);
  });

  it("carries the purchase from the address, so a link is enough to arrive", async () => {
    // The other half. A correlation id that did not survive the trip means the
    // console is asked about a watch nobody named, and the screen says it has no
    // record of it — which reads like a restarted agent rather than like a bug
    // here.
    servingOneWatch("c-abc");
    at("?run=c-abc&attempt=1");

    await expect.poll(() => asked).toEqual(["/watches/w1/attempts/1/presented"]);
  });

  it("says what is missing when the address names no purchase, and points at one", () => {
    // Not a 404 and not an empty set of tables. Arriving here without a purchase
    // named is arriving at a question with no subject.
    servingOneWatch("c-abc");
    at("");

    expect(screen.getByRole("link", { name: /start from a purchase/i })).toBeTruthy();
    expect(
      screen.queryByTestId("disclosure"),
      "nothing is asked of the console, so there is nothing to draw tables about",
    ).toBeNull();
  });

  it("offers the way back to the lanes for this purchase, not to the newest", () => {
    // The counterpart to the way *in*. A reader who followed a link from an
    // attempt has to be able to get back to the attempt, and a bare `/` would
    // drop them on the shop with the run they were reading forgotten.
    servingOneWatch("c-abc");
    at("?run=c-abc&attempt=2");

    expect(screen.getByRole("link", { name: /back to the three lanes/i }).getAttribute("href")).toBe(
      "/?run=c-abc",
    );
  });

  it("falls back to the first attempt when the address names one no agent has", async () => {
    // `?attempt=0` and `?attempt=nonsense` are the same case: the console counts
    // from 1, so both would ask for an attempt that cannot exist. The first is
    // the honest fallback — it is what a panel opened with nothing selected
    // showed — and the digest on screen is what tells a reader which attempt
    // they are actually looking at.
    servingOneWatch("c-abc");
    at("?run=c-abc&attempt=0");

    await expect.poll(() => asked).toEqual(["/watches/w1/attempts/1/presented"]);
  });
});
