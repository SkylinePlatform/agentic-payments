/**
 * The agent's console, as React state.
 *
 * `/watches` is the Shopping Agent's own bookkeeping and it is proxied to
 * `127.0.0.1:8086` by `vite.config.ts`. It is the only place a browser can get
 * the chains an attempt presented — the collector's event stream carries what
 * happened, never the artefacts, because ADR 0003 makes the event log
 * observability and never evidence.
 */

import { useCallback, useEffect, useState } from "react";

import { inspect } from "./model";
import type { Inspection, Presented } from "./model";

/** One watch, as `GET /watches` lists it. */
export interface Watch {
  readonly id: string;
  readonly correlation_id?: string;
  readonly typed: string;
  readonly item?: string;
  readonly state: string;
  /** How many purchase attempts it has made. Each one presented its own chains. */
  readonly attempts: number;
}

async function readJSON<T>(url: string, what: string): Promise<T> {
  const response = await fetch(url);
  if (!response.ok) {
    // The body first: the console answers `text/plain` for its three different
    // 404s — a watch nobody started, a number that is not one, an attempt that
    // watch never made — and each says which, so a screen that showed only the
    // status would throw away the part worth reading.
    const said = (await response.text()).trim();
    throw new Error(said === "" ? `${what}: ${String(response.status)}` : said);
  }
  return (await response.json()) as T;
}

export interface Console {
  readonly watches: readonly Watch[];
  /** Null until a watch with at least one attempt has been chosen. */
  readonly inspection: Inspection | null;
  readonly selected: { readonly watch: string; readonly attempt: number } | null;
  readonly error: string | null;
  readonly loading: boolean;
  readonly select: (watch: string, attempt: number) => void;
  readonly refresh: () => void;
}

export function useConsole(): Console {
  const [watches, setWatches] = useState<readonly Watch[]>([]);
  const [selected, setSelected] = useState<{ watch: string; attempt: number } | null>(null);
  const [inspection, setInspection] = useState<Inspection | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [round, setRound] = useState(0);

  const refresh = useCallback(() => {
    setRound((n) => n + 1);
  }, []);

  // The list. Re-read on demand rather than polled: an attempt is a thing a
  // reader is looking at, and a list that reordered itself under a screenshot
  // would be worse than one that is a few seconds old.
  useEffect(() => {
    let live = true;
    readJSON<{ watches: Watch[] }>("/watches", "listing the watches")
      .then((body) => {
        if (!live) return;
        setWatches(body.watches);
        setError(null);
      })
      .catch((cause: unknown) => {
        if (live) setError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => {
      live = false;
    };
  }, [round]);

  // The chains, decoded. `inspect` hashes with Web Crypto, so this is async all
  // the way down and the effect has to guard against arriving late.
  useEffect(() => {
    if (selected === null) {
      setInspection(null);
      return;
    }
    let live = true;
    setLoading(true);
    const path = `/watches/${selected.watch}/attempts/${String(selected.attempt)}/presented`;
    readJSON<Presented>(path, "reading what that attempt presented")
      .then(inspect)
      .then((out) => {
        if (!live) return;
        setInspection(out);
        setError(null);
      })
      .catch((cause: unknown) => {
        if (!live) return;
        setInspection(null);
        setError(cause instanceof Error ? cause.message : String(cause));
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [selected]);

  const select = useCallback((watch: string, attempt: number) => {
    setSelected({ watch, attempt });
  }, []);

  return { watches, inspection, selected, error, loading, select, refresh };
}
