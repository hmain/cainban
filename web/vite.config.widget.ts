import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

// Dedicated Vite config for the EMBEDDABLE WIDGET build (separate from the SPA's
// vite.config.ts). It produces a single self-contained IIFE bundle at
// web/dist/widget/cainban-widget.js that any external site can drop in with one
// <script> tag — no module loader, no external runtime deps (React is bundled
// in). The widget's CSS is imported `?inline` and injected into a Shadow DOM, so
// there is no separate stylesheet to ship and nothing leaks into the host page.
//
// Build with:  npm run build:widget
export default defineConfig({
  plugins: [react()],
  // `define` so the bundler tree-shakes React's dev-only code paths out of the
  // production IIFE (smaller, no `process is not defined` at runtime on a host
  // page that has no bundler-provided process shim).
  define: {
    "process.env.NODE_ENV": JSON.stringify("production"),
  },
  build: {
    outDir: "dist/widget",
    emptyOutDir: true,
    // Keep source readable-ish but minified for the size budget; no sourcemap in
    // the shipped artifact.
    sourcemap: false,
    minify: "esbuild",
    lib: {
      entry: resolve(__dirname, "src/widget/widget.ts"),
      name: "cainbanWidget",
      formats: ["iife"],
      fileName: () => "cainban-widget.js",
    },
    rollupOptions: {
      output: {
        // Single self-contained file — inline any dynamic chunks so the embedder
        // only ever references one URL.
        inlineDynamicImports: true,
      },
    },
  },
});
