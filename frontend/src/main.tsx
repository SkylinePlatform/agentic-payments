import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";

import { App } from "./App";
import "./styles.css";

const root = document.getElementById("root");
if (!root) {
  // index.html owns this element. If it is missing, something removed it, and
  // a blank page with nothing in the console is the worst way to find out.
  throw new Error("index.html is missing its #root element");
}

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
