import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import path from 'node:path'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    include: ['src/**/*.{test,spec}.ts'],
    coverage: {
      // Restrict scope to the code our unit tests actually target:
      // - src/api/* (axios + sse clients)
      // - src/stores/* (Pinia stores)
      // Everything else (views, components, router, composables, type-only
      // files, config files at the package root) is covered by Playwright
      // E2E or is non-runtime code.
      provider: 'v8',
      reporter: ['text', 'html', 'lcov'],
      include: [
        'src/api/client.ts',
        'src/stores/filters.ts',
        'src/stores/slots.ts',
      ],
      thresholds: {
        // Wave 4 baseline 50 % on the included slice — raise as the
        // suite grows. Branches set lower because some axios paths only
        // fire on rare network errors.
        lines: 50,
        functions: 50,
        statements: 50,
        branches: 40,
      },
    },
  },
})
