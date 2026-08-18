import { defineConfig, mergeConfig } from 'vitest/config'

import viteConfig from './vite.config'

// Merged with vite.config.ts so test-time module resolution (aliases,
// plugins) matches the app build exactly -- see tasks/plan.md Task 1.
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
    },
  }),
)
