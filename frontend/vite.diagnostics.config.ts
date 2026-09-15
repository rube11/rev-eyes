import { defineConfig } from "vite"
import { copyFileSync } from "node:fs"
import { resolve } from "node:path"

export default defineConfig({
  base: "./",
  plugins: [{
    name: "diagnostics-default-entry",
    writeBundle(options) {
      // Include the standard launch page as well as the development URL.
      const output = resolve(options.dir ?? "dist-diagnostics")
      copyFileSync(resolve(output, "diagnostics.html"), resolve(output, "index.html"))
    },
  }],
  build: { outDir: "dist-diagnostics", rollupOptions: { input: "diagnostics.html" } },
})
