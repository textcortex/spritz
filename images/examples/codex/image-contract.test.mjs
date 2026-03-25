import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

test("Dockerfile marks the Codex entrypoints executable", () => {
  const dockerfile = fs.readFileSync(new URL("./Dockerfile", import.meta.url), "utf8");
  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/base\/entrypoint\.sh \/usr\/local\/bin\/spritz-entrypoint/,
  );
  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/codex\/entrypoint\.sh \/usr\/local\/bin\/spritz-codex-entrypoint/,
  );
  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/shared\/spritz-acp-server\.mjs \/usr\/local\/shared\/spritz-acp-server\.mjs/,
  );
  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/codex\/acp-server\.mjs \/usr\/local\/bin\/spritz-codex-acp-server/,
  );
});
