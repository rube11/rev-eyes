import { defineConfig } from "vite"

export default defineConfig({
  base: "./",
  build: { outDir: "dist-diagnostics", rollupOptions: { input: "diagnostics.html" } },
})
