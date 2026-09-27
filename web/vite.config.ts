import { writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

// Vite empties dist/ on every build, which deletes the tracked dist/.gitkeep
// that lets `go:embed all:dist` (web/embed.go) compile without a frontend
// build. Recreate it so a build never leaves that deletion in the worktree.
const keepDistPlaceholder: Plugin = {
  name: 'keep-dist-placeholder',
  apply: 'build',
  closeBundle() {
    writeFileSync(fileURLToPath(new URL('./dist/.gitkeep', import.meta.url)), '')
  },
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), keepDistPlaceholder],
})
