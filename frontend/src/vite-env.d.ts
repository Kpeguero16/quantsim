/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL for every API call -- the gateway. See SPEC.md 2.3. */
  readonly VITE_API_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
