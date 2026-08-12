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
import { PACE_MS, nextRelease, releasedNow } from "./pace";

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
 * `pace` is milliseconds between steps and `0` means none — see `pace.ts` for
 * the ruling. It is an option rather than a constant so that a suite about
 * *what* is drawn can say it is not about *when*, and so that the route can
 * hold one number in one place rather than a component guessing at it.
 */
export interface ViewOptions extends StreamOptions {
  readonly pace?: number;
}

export interface StreamView {
  /** Grouped into purchases, newest first. */
  readonly transactions: readonly Transaction[];
  /** Every record the screen is drawing, oldest first, for the log beneath the lanes. */
  readonly records: readonly EventRecord[];
  /**
   * Records that have arrived and are not on screen yet.
   *
   * Zero except while the pacing is behind. The route says this out loud, which
   * is the third clause of the ruling in `pace.ts`: a screen may be slower than
   * the events were, and it may not be quietly slower.
   */
  readonly behind: number;
  /** Draws everything that has arrived, now. */
  readonly showEverything: () => void;
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
  const { url, from, create, pace = PACE_MS } = options;

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

  // Derived rather than stored, so the cap can move the screen forward without
  // a render whose only job is to write the number down. `shown` is the count
  // the ticks have released and this is what a reader may actually see, which
  // differs whenever a reconnect has just replayed more than the cap allows.
  const drawn = releasedNow(shown, records.length, pace);

  useEffect(() => {
    if (drawn >= records.length) return;
    const tick = setTimeout(() => {
      setShown(nextRelease(drawn, records.length, pace));
    }, pace);
    return () => {
      clearTimeout(tick);
    };
  }, [drawn, records.length, pace]);

  // Catches up, and does not turn the pacing off: whatever arrives next is
  // paced again. Somebody who clicked it wanted the purchase on screen for a
  // screenshot, not a different screen for the rest of the demonstration.
  const total = records.length;
  const showEverything = useCallback(() => {
    setShown(total);
  }, [total]);

  const paced = useMemo(
    () => (drawn >= records.length ? records : records.slice(0, drawn)),
    [records, drawn],
  );
  const transactions = useMemo(() => group(paced), [paced]);

  return {
    transactions,
    records: paced,
    behind: records.length - drawn,
    showEverything,
    state,
    gaps,
    reconnect,
  };
}
