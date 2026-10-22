import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App, { createQueryClient } from "./App";
import { applyTheme, readTheme } from "./lib/theme";
import "./index.css";

applyTheme(readTheme());

const el = document.getElementById("root");
if (!el) throw new Error("#root introuvable");

createRoot(el).render(
  <StrictMode>
    <App client={createQueryClient()} />
  </StrictMode>,
);
