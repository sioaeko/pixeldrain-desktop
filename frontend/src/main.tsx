import React from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./globals.css";

// Keep the webview from navigating to files dropped outside a drop zone.
window.addEventListener("dragover", (e) => e.preventDefault());
window.addEventListener("drop", (e) => e.preventDefault());

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
