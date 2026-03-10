import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const dockerfilePath = path.join(__dirname, "Dockerfile");
const entrypointPath = path.join(__dirname, "entrypoint.sh");

test("openclaw image copies the ACP compat generator into /usr/local/bin", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY examples\/openclaw\/generate-openclaw-acp-compat\.mjs \/usr\/local\/bin\/spritz-generate-openclaw-acp-compat/,
  );
});

test("openclaw image generates the stable ACP compat module after installing OpenClaw", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /node \/usr\/local\/bin\/spritz-generate-openclaw-acp-compat \/usr\/local\/lib\/node_modules\/openclaw/,
  );
  assert.match(
    dockerfile,
    /test -f \/usr\/local\/lib\/node_modules\/openclaw\/dist\/spritz-acp-compat\.js/,
  );
});

test("openclaw image copies the ACP server into /usr/local/bin", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/openclaw\/acp-server\.mjs \/usr\/local\/bin\/spritz-openclaw-acp-server/,
  );
});

test("openclaw image copies the ACP wrapper next to the ACP server runtime path", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/openclaw\/acp-wrapper\.mjs \/usr\/local\/bin\/acp-wrapper\.mjs/,
  );
});

test("openclaw entrypoint defaults the ACP server binary", () => {
  const entrypoint = fs.readFileSync(entrypointPath, "utf8");

  assert.match(
    entrypoint,
    /server_bin="\$\{SPRITZ_OPENCLAW_SERVER_BIN:-\/usr\/local\/bin\/spritz-openclaw-acp-server\}"/,
  );
});
