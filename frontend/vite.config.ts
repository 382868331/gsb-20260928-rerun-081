import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 构建产物直接输出到 Go 的 embed 目录 cmd/server/webdist
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080"
    }
  },
  build: {
    outDir: "../cmd/server/webdist",
    emptyOutDir: true
  }
});
