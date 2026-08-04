import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

/**
 * The collector's default listen address, matching `-addr` in
 * backend/cmd/collector. Overridable with VITE_COLLECTOR_URL so the demo runner
 * can move it without editing this file.
 */
const DEFAULT_COLLECTOR = "http://127.0.0.1:8085";

export default defineConfig(({ mode }) => {
  // "." rather than process.cwd(): this file is type-checked with the app, and
  // reaching for process would pull Node's type definitions into a config that
  // needs nothing else from them.
  const env = loadEnv(mode, ".", "VITE_");
  const collector = env.VITE_COLLECTOR_URL ?? DEFAULT_COLLECTOR;

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
      },
    },
  };
});
