import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const sourceRoot = path.resolve(__dirname, "..", "..", "openclaw");
const dockerfilePath = path.join(sourceRoot, "Dockerfile");
const entrypointPath = path.join(sourceRoot, "entrypoint.sh");

test("openclaw image copies the ACP compat generator from the TypeScript build stage", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --from=openclaw-ts-build --chmod=0755 \/build\/examples\/dist\/openclaw\/generate-openclaw-acp-compat\.js \/usr\/local\/bin\/spritz-generate-openclaw-acp-compat/,
  );
});

test("openclaw image resolves the bundled acpx extension directory before installing acpx", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --from=openclaw-ts-build --chmod=0755 \/build\/examples\/dist\/openclaw\/resolve-openclaw-acpx-extension-dir\.js \/usr\/local\/bin\/spritz-resolve-openclaw-acpx-extension-dir/,
  );
  assert.match(
    dockerfile,
    /extension_dir="\$\(node \/usr\/local\/bin\/spritz-resolve-openclaw-acpx-extension-dir "\$\{openclaw_dir\}"\)"/,
  );
});

test("openclaw image generates the stable ACP compat module after installing OpenClaw", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /node \/usr\/local\/bin\/spritz-generate-openclaw-acp-compat "\$\{openclaw_dir\}"/,
  );
  assert.match(dockerfile, /test -f "\$\{openclaw_dir\}\/dist\/spritz-acp-compat\.js"/);
});

test("openclaw image copies the compiled ACP server runtime into \/usr\/local\/bin", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --from=openclaw-ts-build --chown=dev:dev --chmod=0755 \/build\/examples\/dist\/openclaw\/acp-server\.js \/usr\/local\/bin\/spritz-openclaw-acp-server/,
  );
});

test("openclaw image copies the compiled shared ACP harness next to the runtime import path", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --from=openclaw-ts-build --chown=dev:dev --chmod=0755 \/build\/examples\/dist\/shared\/spritz-acp-server\.js \/usr\/local\/shared\/spritz-acp-server\.js/,
  );
});

test("openclaw image copies the compiled ACP wrapper and gateway proxy into \/usr\/local\/bin", () => {
  const dockerfile = fs.readFileSync(dockerfilePath, "utf8");

  assert.match(
    dockerfile,
    /COPY --from=openclaw-ts-build --chown=dev:dev --chmod=0755 \/build\/examples\/dist\/openclaw\/acp-wrapper\.js \/usr\/local\/bin\/acp-wrapper\.js/,
  );
  assert.match(
    dockerfile,
    /COPY --from=openclaw-ts-build --chown=dev:dev --chmod=0755 \/build\/examples\/dist\/openclaw\/gateway-proxy\.js \/usr\/local\/bin\/gateway-proxy\.js/,
  );
});

test("openclaw entrypoint defaults the ACP server binary", () => {
  const entrypoint = fs.readFileSync(entrypointPath, "utf8");

  assert.match(
    entrypoint,
    /server_bin="\$\{SPRITZ_OPENCLAW_SERVER_BIN:-\/usr\/local\/bin\/spritz-openclaw-acp-server\}"/,
  );
});
