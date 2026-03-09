import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const dockerfilePath = path.join(__dirname, "Dockerfile");
const entrypointPath = path.join(__dirname, "entrypoint.sh");

test("openclaw image copies the ACP wrapper into /usr/local/bin", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/openclaw\/acp-wrapper\.mjs \/usr\/local\/bin\/spritz-openclaw-acp-wrapper/,
  );
});

test("openclaw entrypoint defaults the ACP command to the wrapper", () => {
  const entrypoint = fs.readFileSync(entrypointPath, "utf8");

  assert.match(
    entrypoint,
    /export SPRITZ_OPENCLAW_ACP_COMMAND="\$\{SPRITZ_OPENCLAW_ACP_COMMAND:-\/usr\/local\/bin\/spritz-openclaw-acp-wrapper\}"/,
  );
});
