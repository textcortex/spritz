import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";

test("Dockerfile marks the Claude Code entrypoints executable", () => {
  const dockerfile = fs.readFileSync(new URL("./Dockerfile", import.meta.url), "utf8");
  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/base\/entrypoint\.sh \/usr\/local\/bin\/spritz-entrypoint/,
  );
  assert.match(
    dockerfile,
    /COPY --chown=dev:dev --chmod=0755 examples\/claude-code\/entrypoint\.sh \/usr\/local\/bin\/spritz-claude-code-entrypoint/,
  );
});
