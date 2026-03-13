import path from 'node:path';
import { fileURLToPath } from 'node:url';

const uiDir = path.dirname(fileURLToPath(import.meta.url));

/**
 * Resolve a path relative to the ui/ directory.
 */
export function uiPath(...parts) {
  return path.join(uiDir, ...parts);
}

/**
 * Resolve a path relative to the ui/public/ directory.
 */
export function uiSourcePublicPath(...parts) {
  return path.join(uiDir, 'public', ...parts);
}

/**
 * Resolve a path relative to the ui/dist/ directory.
 */
export function uiDistPath(...parts) {
  return path.join(uiDir, 'dist', ...parts);
}
