#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export function resolveOpenclawAcpxExtensionDir(packageRoot) {
  const candidates = [
    path.join(packageRoot, "dist", "extensions", "acpx"),
    path.join(packageRoot, "extensions", "acpx"),
  ];

  for (const extensionDir of candidates) {
    if (fs.existsSync(extensionDir)) {
      return extensionDir;
    }
  }

  throw new Error(`OpenClaw acpx extension directory not found under ${packageRoot}`);
}

function main() {
  const packageRoot = process.argv[2]?.trim();
  if (!packageRoot) {
    throw new Error("Usage: resolve-openclaw-acpx-extension-dir <openclaw-package-root>");
  }
  process.stdout.write(`${resolveOpenclawAcpxExtensionDir(path.resolve(packageRoot))}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(String(error));
    process.exit(1);
  }
}
