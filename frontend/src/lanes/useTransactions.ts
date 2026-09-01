/**
 * The collector's stream, as React state.
 *
 * `src/sse` is a library on purpose — it builds no React and holds no state a
 * component can see — and this is the one place that turns it into some. Keeping
 * that seam is what lets the stream be tested against a fake source with no DOM,
 * and what lets this file be about effects and nothing else.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { connect } from "../sse";

import { DRAIN_MS, atLeast, gapFor } from "./pace";
import type { ConnectionState, EventRecord, Gap, StreamOptions } from "../sse";

import { group } from "./model";
import type { Transaction } from "./model";

/**
 * How many records are kept.
 *
 * The collector's hub holds 512 and replays what it still has, so keeping more
 * than that would be keeping records nothing can re-deliver anyway. Holding
 * fewer than the hub does would mean a reload showed less than the tab that had
 * been open, which reads as data loss.
 */
const KEEP = 512;

/**
 * What the view wants of the stream, plus how fast it may draw it.
 *
 * `pace` is the drain window in milliseconds — everything waiting is on screen
 * within it — and `0` means no pacing at all, which is what a suite about
 * *what* is drawn says to be clear it is not about *when*. See `pace.ts` for
 * the rule and for the two shapes this went through before it worked.
 */
export interface ViewOptions extends StreamOptions {
  readonly pace?: number;
}

export interface StreamView {
  /** Grouped into purchases, newest first. */
  readonly transactions: readonly Transaction[];
  /** Every record the screen is drawing, oldest first, for the log beneath the lanes. */
  readonly records: readonly EventRecord[];
  /** Where the connection stands. */
  readonly state: ConnectionState;
  /**
   * Breaks in the sequence, if any.
   *
   * Surfaced rather than swallowed, because the standard this screen is held to
   * is that every step is visible — and "some steps were dropped" is more
   * honest than a log with a silent hole in it. `collector.Hub` disconnects a
   * subscriber that falls 64 behind, so this is a real case and not a defensive
   * nicety.
   */
  readonly gaps: readonly Gap[];
  /** Opens a fresh connection from the last sequence number seen. */
  readonly reconnect: () => void;
}

export function useTransactions(options: ViewOptions = {}): StreamView {
  const [records, setRecords] = useState<readonly EventRecord[]>([]);
  const [state, setState] = useState<ConnectionState>("connecting");
  const [gaps, setGaps] = useState<readonly Gap[]>([]);
  const [shown, setShown] = useState(0);

  // The stream is held in a ref rather than in state: reconnect() has to reach
  // the live one, and putting it in state would re-run the effect that made it.
  const stream = useRef<ReturnType<typeof connect> | null>(null);

  // `url` and `from` are read once, when the effect opens the connection. The
  // options object is a fresh literal on every render at most call sites, so
  // depending on it directly would tear the connection down and open another on
  // each one — which under StrictMode is already two connections and would
  // become unbounded.
  const { url, from, create, pace = DRAIN_MS } = options;

  useEffect(() => {
    const opened = connect({ url, from, create });
    stream.current = opened;

    const off = [
      opened.onAny((record) => {
        setRecords((held) => {
          const next = [...held, record];
          return next.length > KEEP ? next.slice(next.length - KEEP) : next;
        });
      }),
      opened.onGap((gap) => {
        setGaps((held) => [...held, gap]);
      }),
      opened.onState(setState),
    ];

    // Whatever the stream settled on between `connect()` returning and the
    // listener above being registered. `connect()` opens immediately, so a
    // source that could not be constructed at all is already `failed` by this
    // line and `onState` will never announce it — the component would sit on
    // `connecting` for ever, showing the one message that is certainly wrong.
    setState(opened.state);

    // Both halves matter. Without the unsubscribes a discarded effect's
    // listeners keep writing into state that nothing renders; without close()
    // the connection itself survives, and under StrictMode in development that
    // is two open streams delivering every event twice — which on screen looks
    // exactly like the backend emitting duplicates and sends the reader to the
    // wrong half of the system.
    return () => {
      for (const unsubscribe of off) unsubscribe();
      opened.close();
      stream.current = null;
    };
  }, [url, from, create]);

  const reconnect = useCallback(() => {
    stream.current?.reconnect();
  }, []);

  // When the last step was let through, so a screen that has caught up draws the
  // next arrival at once instead of making it serve a gap nothing is queued
  // behind. A ref rather than state: writing it must not cause the render it is
  // read in.
  const lastRelease = useRef(0);
  // When the queue that is currently draining has to be empty by. Set when a
  // queue forms and cleared when it empties, which is what makes the gap
  // constant across a drain rather than growing as the divisor falls — see
  // `pace.ts` for the arithmetic and for what the obvious version does instead.
  const emptyBy = useRef(0);

  // Everything past the cap is already on screen — a reconnect's replay lands
  // rather than queueing. Derived rather than stored, so the cap can move the
  // screen forward without a render whose only job is to write the number down.
  const drawn = pace <= 0 ? records.length : atLeast(shown, records.length);
  const waiting = records.length - drawn;

  useEffect(() => {
    if (waiting <= 0) {
      emptyBy.current = 0;
      return;
    }
    // `Date.now` and not the injected clock: this is a browser's own animation
    // timing, not a protocol timestamp, and nothing here is signed, compared or
    // shown. Every card and every log row still prints the event's own `at`,
    // which is the emitting party's clock.
    const now = Date.now();
    if (emptyBy.current === 0) emptyBy.current = now + pace;
    const gap = gapFor(emptyBy.current - now, waiting);
    // A screen that has caught up draws the next arrival at once: the gap is
    // there to separate steps from each other, and there is nothing to separate
    // this one from.
    const since = now - lastRelease.current;
    const tick = setTimeout(
      () => {
        lastRelease.current = Date.now();
        setShown(drawn + 1);
      },
      Math.max(0, gap - since),
    );
    return () => {
      clearTimeout(tick);
    };
  }, [drawn, waiting, pace]);

  const paced = useMemo(
    () => (drawn >= records.length ? records : records.slice(0, drawn)),
    [records, drawn],
  );
  const transactions = useMemo(() => group(paced), [paced]);

  return {
    transactions,
    records: paced,
    state,
    gaps,
    reconnect,
  };
}
