import { Route, Routes } from "react-router-dom";

import { Shell } from "./layout/Shell";
import { MandateInspector } from "./routes/MandateInspector";
import { NotFound } from "./routes/NotFound";
import { ThreeLanes } from "./routes/ThreeLanes";
import { TrustedSurface } from "./routes/TrustedSurface";

/**
 * App is the route table and nothing else.
 *
 * Three surfaces, each owned by its own issue, each rendered inside the shell.
 * Keeping the table here — rather than letting each surface register itself —
 * means one file answers "what can this app show", which is the question
 * somebody arriving at the codebase asks first.
 */
export function App() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route index element={<ThreeLanes />} />
        <Route path="inspector" element={<MandateInspector />} />
        <Route path="consent" element={<TrustedSurface />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
