/// <reference types="vite/client" />

// The libheif build for browsers, with its WebAssembly inside.
declare module 'libheif-js/libheif-wasm/libheif-bundle.mjs' {
  const factory: () => unknown;
  export default factory;
}
