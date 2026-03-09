// Global test setup for Vitest + jsdom
import { config } from '@vue/test-utils'

// Stub CSS imports (Tailwind etc.) that jsdom can't handle
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})
