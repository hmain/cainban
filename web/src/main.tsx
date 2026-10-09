import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router";
import { configureAmplify } from "./amplify";
import { App } from "./App";
import { initTheme } from "./theme";
import "./styles.css";

// Apply the stored theme preference before React mounts, so there is no flash of
// the wrong scheme on first paint.
initTheme();

// Configure Amplify against the existing CDK-managed pool + SPA client before
// the app renders, so the OAuth session is available on first paint.
configureAmplify();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
);
