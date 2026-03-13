import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const uiDir = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const sourceDir = path.join(uiDir, 'public');
const distDir = path.join(uiDir, 'dist');
const staticFiles = ['config.js', 'index.html', 'styles.css'];
const staticDirectories = ['vendor'];
const builtEntries = [
  'acp-client',
  'acp-page',
  'acp-render',
  'app',
  'create-form-request',
  'create-form-state',
  'preset-config',
  'preset-panel',
];

fs.mkdirSync(distDir, { recursive: true });
for (const name of staticFiles) {
  fs.copyFileSync(path.join(sourceDir, name), path.join(distDir, name));
}

for (const name of staticDirectories) {
  const sourcePath = path.join(sourceDir, name);
  const targetPath = path.join(distDir, name);
  fs.rmSync(targetPath, { recursive: true, force: true });
  fs.cpSync(sourcePath, targetPath, { recursive: true });
}

for (const entry of builtEntries) {
  const iifePath = path.join(distDir, `${entry}.iife.js`);
  const normalizedPath = path.join(distDir, `${entry}.js`);
  if (fs.existsSync(iifePath)) {
    fs.renameSync(iifePath, normalizedPath);
  }
}
