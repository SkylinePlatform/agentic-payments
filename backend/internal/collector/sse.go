package collector

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// EventsPath is where the collector listens, for both halves of its job: POST
// puts events in, GET streams them out. One path routed by method rather than
// two paths, because they are two views of one resource and the frontend and
// the roles' sinks should not have to agree on two strings.
const EventsPath = "/events"

// Stream serves the event log as Server-Sent Events.
//
// SSE rather than a WebSocket because it is a long-lived HTTP response and
// nothing more — ADR 0001 settled on HTTP and ADR 0003 was explicit that this
// must not reopen that, so a second protocol on a second connection type would
// be a decision nobody made. The browser side is EventSource, which reconnects
// on its own.
//
// A new stream replays the recent history and then continues live, with no gap
// and no duplicate between the two. That property comes from Hub.Subscribe
// doing both under one lock; this handler only has to write what it is given,
// in the order it is given.
func Stream(h *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			// Without flushing, every event would sit in a buffer until the
			// response ended — which for a stream is never. Better to say so
			// than to serve a stream that silently shows nothing.
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		// Named for the proxy that would otherwise buffer the stream into
		// uselessness. This costs nothing when no proxy is present.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		history, sub := h.Subscribe()
		defer sub.Unsubscribe()

		for _, rec := range history {
			if err := writeRecord(w, rec); err != nil {
				return
			}
		}
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				// The client went away. Unsubscribing is deferred, so the hub
				// stops trying to feed a socket nobody is reading.
				return
			case rec, open := <-sub.C:
				if !open {
					// The hub ended it: shutdown, or this reader fell behind.
					// Either way the stream is over and EventSource will
					// reconnect if the server is still there.
					return
				}
				if err := writeRecord(w, rec); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

// writeRecord emits one SSE frame.
//
// The framing is deliberately plain: an id line so a reconnecting client can
// say where it got to, an event line naming the kind so a listener can filter
// without parsing the payload, and one data line. The payload is JSON on a
// single line, which is what makes the single data line safe — a newline inside
// it would end the frame early, and json.Marshal never emits one unescaped.
//
// The correlation ID inside the payload has already been through
// obs.ValidCorrelationID by the time it reaches here, so the one field an
// attacker could otherwise use to forge a frame boundary cannot contain a
// newline either.
func writeRecord(w http.ResponseWriter, rec Record) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("collector: marshal record: %w", err)
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", rec.Seq, rec.Event.Kind, payload)
	return err
}
