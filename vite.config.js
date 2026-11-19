import { fileURLToPath } from "node:url";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vite";

const appRoots = { installer: "installer/ui", panel: "jarvisd/ui" };
const fromRepo = (path) => fileURLToPath(new URL(path, import.meta.url));

export default defineConfig(({ mode }) => {
  const root = appRoots[mode];
  if (!root) throw new Error("Use npm run build:installer or npm run build:panel");

  return {
    root: fromRepo(root),
    base: "./",
    plugins: [svelte()],
    build: {
      outDir: fromRepo(`build/ui/${mode}`),
      emptyOutDir: true,
      rolldownOptions: {
        // The installer serves this module with the token for the current process.
        external: mode === "installer" ? ["/token.js"] : [],
      },
    },
  };
});
