import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

/**
 * The collector's default listen address, matching `-addr` in
 * backend/cmd/collector. Overridable with VITE_COLLECTOR_URL so the demo runner
 * can move it without editing this file.
 */
const DEFAULT_COLLECTOR = "http://127.0.0.1:8085";

/**
 * The Shopping Agent's console API, matching `-addr` on the `agent-watch` entry
 * in deploy/demo.json. Overridable with VITE_AGENT_URL, in VITE_COLLECTOR_URL's
 * shape and for the same reason.
 */
const DEFAULT_AGENT = "http://127.0.0.1:8086";

export default defineConfig(({ mode }) => {
  // "." rather than process.cwd(): this file is type-checked with the app, and
  // reaching for process would pull Node's type definitions into a config that
  // needs nothing else from them.
  const env = loadEnv(mode, ".", "VITE_");
  const collector = env.VITE_COLLECTOR_URL ?? DEFAULT_COLLECTOR;
  const agent = env.VITE_AGENT_URL ?? DEFAULT_AGENT;

  return {
    plugins: [react()],
    server: {
      port: 5173,
      // Fail rather than silently moving to another port. The demo runner
      // prints a URL, and a dev server that quietly picked 5174 makes that
      // URL wrong.
      strictPort: true,
      proxy: {
        // The event stream is served same-origin in development, so nothing
        // downstream has to solve CORS or learn the collector's address. It is
        // here rather than in the view that consumes it because it is dev
        // server configuration, and because the alternative is that whoever
        // builds the three-lane view spends their first hour on it.
        //
        // SSE needs the proxy left alone: no buffering, no timeout on a
        // response that is supposed to stay open.
        "/events": {
          target: collector,
          changeOrigin: true,
          // A stream has no content length and never ends on its own.
          timeout: 0,
          proxyTimeout: 0,
        },

        // The agent's console API, same-origin for the same reason — and here
        // the case against solving it with CORS instead is sharper than
        // convenience. `POST` carrying `Idempotency-Key` is not a simple
        // request, so a browser preflights it; `transport.Idempotency` treats
        // `OPTIONS` as safe and passes it straight to the mux, which has no
        // handler for it and answers 405. CORS is therefore not a header on one
        // route but a change to middleware every role runs — a process holding a
        // signing key, on every state-changing route that spends a user's open
        // mandate — to serve one browser in one dev setup.
        //
        // No `rewrite`, which is why the route is `/watches` rather than
        // something needing a prefix stripped: the path the browser asks for is
        // the path the agent serves.
        "/watches": {
          target: agent,
          changeOrigin: true,
        },
      },
    },
  };
});
