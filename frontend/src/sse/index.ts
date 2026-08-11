/**
 * The collector's event stream, typed.
 *
 * `connect()` and nothing else is what a surface needs: it opens `/events`,
 * registers a listener for each of the six kinds — the collector names its
 * events, and a named SSE event never reaches `onmessage` — and hands back a
 * subscription per kind, a report of anything the stream skipped, and a
 * connection state.
 *
 * This is a library. It builds no React and holds no state a component can see;
 * wiring it into an effect is #20's job, and `stream.ts`'s own comment gives
 * the cleanup that makes `StrictMode` behave.
 *
 * The two names that carry the seam — the source interface and its factory —
 * are deliberately missing from this barrel. The app never writes either of
 * them down, because passing nothing gets the browser's, and
 * `src/architecture.test.ts` allows that identifier in `./stream.ts` alone; a
 * test injecting a fake imports them from there.
 */

export {
  EVENT_KINDS,
  MANDATE_STATES,
  MANDATE_TYPES,
  PROTOCOL_EVENT_FIELDS,
  isEventKind,
  parseRecord,
} from "./events";
export type {
  EventKind,
  EventRecord,
  MandateRef,
  MandateState,
  MandateType,
  ParsedRecord,
  ProtocolEvent,
  ProtocolEventField,
} from "./events";

export { connect } from "./stream";
export type {
  ConnectionState,
  EventStream,
  Gap,
  MalformedFrame,
  RecordListener,
  StreamOptions,
  Unsubscribe,
} from "./stream";
