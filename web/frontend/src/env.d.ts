/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Set to any truthy value to enable cloud mode (S3 uploads, presigned URLs). */
  readonly VITE_CLOUD_MODE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
