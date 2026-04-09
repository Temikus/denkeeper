import { defineConfig } from 'vitest/config'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

export default defineConfig({
  plugins: [svelte()],
  resolve: {
    // Ensure Svelte resolves to client-side (browser) bundle, not server.
    conditions: ['browser'],
  },
  test: {
    exclude: ['test/e2e/**', '**/node_modules/**', '**/dist/**'],
    environment: 'jsdom',
    globals: true,
    // polyfill-storage must run first to fix Node 25+ bare localStorage
    setupFiles: ['./src/test/polyfill-storage.js', './src/test/setup.js'],
    pool: 'forks',
    // Give Node 25+ a valid path for its built-in localStorage backing file.
    // In vitest 4, execArgv moved from poolOptions.forks.execArgv to top-level.
    execArgv: [`--localstorage-file=${join(tmpdir(), 'vitest-localstorage')}`],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{js,svelte}'],
      exclude: ['src/test/**', 'src/main.js'],
      thresholds: {
        lines: 58,
        branches: 43,
      },
    },
  },
})
