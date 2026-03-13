import { defineConfig } from 'tsdown';

const entries = [
  'acp-client',
  'acp-page',
  'acp-render',
  'app',
  'create-form-request',
  'create-form-state',
  'preset-config',
  'preset-panel',
] as const;

function browserEntry(entry: string, clean: boolean) {
  return defineConfig({
    clean,
    dts: false,
    entry: `src/${entry}.ts`,
    format: 'iife',
    minify: false,
    outDir: 'dist',
    platform: 'browser',
    sourcemap: false,
    target: 'es2023',
  });
}

export default entries.map((entry, index) => browserEntry(entry, index === 0));
