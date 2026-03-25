import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { resolveOpenclawAcpxExtensionDir } from "./resolve-openclaw-acpx-extension-dir.js";

function withTempDir(run) {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "openclaw-acpx-"));
  try {
    return run(tempDir);
  } finally {
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
}

test("resolveOpenclawAcpxExtensionDir supports the legacy extensions/acpx layout", () => {
  withTempDir((tempDir) => {
    const extensionDir = path.join(tempDir, "extensions", "acpx");
    fs.mkdirSync(extensionDir, { recursive: true });

    assert.equal(resolveOpenclawAcpxExtensionDir(tempDir), extensionDir);
  });
});

test("resolveOpenclawAcpxExtensionDir supports the current dist/extensions/acpx layout", () => {
  withTempDir((tempDir) => {
    const extensionDir = path.join(tempDir, "dist", "extensions", "acpx");
    fs.mkdirSync(extensionDir, { recursive: true });

    assert.equal(resolveOpenclawAcpxExtensionDir(tempDir), extensionDir);
  });
});

test("resolveOpenclawAcpxExtensionDir prefers dist/extensions/acpx when both layouts exist", () => {
  withTempDir((tempDir) => {
    const legacyDir = path.join(tempDir, "extensions", "acpx");
    const distDir = path.join(tempDir, "dist", "extensions", "acpx");
    fs.mkdirSync(legacyDir, { recursive: true });
    fs.mkdirSync(distDir, { recursive: true });

    assert.equal(resolveOpenclawAcpxExtensionDir(tempDir), distDir);
  });
});

test("resolveOpenclawAcpxExtensionDir throws when no acpx extension directory exists", () => {
  withTempDir((tempDir) => {
    assert.throws(
      () => resolveOpenclawAcpxExtensionDir(tempDir),
      /OpenClaw acpx extension directory not found/,
    );
  });
});
