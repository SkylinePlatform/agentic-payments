import { Navigate, Route, Routes, useLocation } from "react-router-dom";

import { Shell } from "./layout/Shell";
import { NotFound } from "./routes/NotFound";
import { SURFACES } from "./surfaces";
import { ThemeProvider } from "./theme/ThemeProvider";

/**
 * App turns the surface list into a route table, inside the shell.
 *
 * The list itself lives in surfaces.tsx, which is also what the nav reads, so
 * that "what can this app show" has exactly one answer rather than two that can
 * disagree.
 *
 * The theme store sits above the routes rather than inside the shell, because
 * what it controls is an attribute on `<html>` — outside React entirely — and
 * because a surface rendered on its own in a test should still have one. main.tsx
 * therefore mounts nothing but this.
 */
export function App() {
  return (
    <ThemeProvider>
      <Routes>
        <Route element={<Shell />}>
          {SURFACES.map((surface) =>
            surface.path === "" ? (
              <Route key="index" index element={surface.element} />
            ) : (
              <Route key={surface.path} path={surface.path} element={surface.element} />
            ),
          )}
          {/*
            Where the second screen used to be. It is one screen now — see
            surfaces.tsx — and this keeps every link to `/protocol?run=…` working
            by carrying the query across rather than dropping the reader on the
            newest purchase instead of theirs.
          */}
          <Route path="protocol" element={<ToTheOneScreen />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </ThemeProvider>
  );
}

/** `/protocol?run=…` → `/?run=…`, query intact. */
function ToTheOneScreen() {
  const { search } = useLocation();
  return <Navigate to={{ pathname: "/", search }} replace />;
}
