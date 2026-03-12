import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const cliPath = path.join(__dirname, '..', 'src', 'index.ts');

async function runCli(args: string[]) {
  const child = spawn(process.execPath, ['--import', 'tsx', cliPath, ...args], {
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  let stdout = '';
  let stderr = '';

  child.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
  });
  child.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  const code = await new Promise<number | null>((resolve) => child.on('exit', resolve));
  return { code, stdout, stderr };
}

test('skillflag list exposes the bundled spz skill', async () => {
  const result = await runCli(['--skill', 'list']);
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, /(^|\n)spz(\t|$)/);
});

test('skillflag show returns the bundled spz skill body', async () => {
  const result = await runCli(['--skill', 'show', 'spz']);
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, /# spz/);
  assert.match(result.stdout, /service-principal create flow/i);
});
