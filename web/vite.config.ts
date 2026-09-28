import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Minimal Vite config for the cainban connect-page SPA. Build output goes to
// dist/, which amplify.yml publishes as the Hosting artifact.
export default defineConfig({
  plugins: [react()],
});
