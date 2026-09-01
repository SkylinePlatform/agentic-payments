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
 * What the view wants of the stream.
 *
 * **There was a `pace` here and #344 removed it.** The screen used to hold
 * records back and release one every 750 ms, so that a room could follow a
 * purchase whose eleven steps land within a tenth of a second of each other.
 * The idea was sound and the arithmetic was not: an attempt takes 8.25 seconds
 * to draw at that rate, the merchant produces one every few seconds, and the
 * count of what was still waiting never reached zero. What a viewer actually
 * saw was a permanent notice reading *"10 steps have arrived and are still
 * being drawn"*, which is precisely the thing the ruling's third clause exists
 * to prevent — a screen you cannot tell from a stalled one.
 *
 * Legibility is worth buying but not at that price, and it is bought elsewhere
 * anyway: the card flight between lanes is half a second of movement per step,
 * and `-step`/`-step-max` in `deploy/demo.json` set how often anything happens
 * at all. What this hook does now is show every record the moment it arrives.
 */
export interface ViewOptions extends StreamOptions {
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

  // The stream is held in a ref rather than in state: reconnect() has to reach
  // the live one, and putting it in state would re-run the effect that made it.
  const stream = useRef<ReturnType<typeof connect> | null>(null);

  // `url` and `from` are read once, when the effect opens the connection. The
  // options object is a fresh literal on every render at most call sites, so
  // depending on it directly would tear the connection down and open another on
  // each one — which under StrictMode is already two connections and would
  // become unbounded.
  const { url, from, create } = options;

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

  const transactions = useMemo(() => group(records), [records]);

  return {
    transactions,
    records,
    state,
    gaps,
    reconnect,
  };
}
