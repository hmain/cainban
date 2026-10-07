import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { configureAmplify } from "./amplify";
import { App } from "./App";
import "./styles.css";

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
