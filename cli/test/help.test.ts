import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import test from 'node:test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const cliPath = path.join(__dirname, '..', 'src', 'index.ts');

async function runCli(args: string[], env: NodeJS.ProcessEnv = process.env) {
  const child = spawn(process.execPath, ['--import', 'tsx', cliPath, ...args], {
    env,
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

test('create help defaults to human audience', async () => {
  const result = await runCli(['create', '--help'], { ...process.env, AUDIENCE: '' });
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, /AUDIENCE \(current: human\)/);
  assert.match(result.stdout, /Use --owner-provider and --owner-subject when you only know a platform-native\s+user ID/i);
});

test('create help for agent audience prefers external owner guidance', async () => {
  const result = await runCli(['create', '--help'], { ...process.env, AUDIENCE: 'agent' });
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, /AUDIENCE \(current: agent\)/);
  assert.match(result.stdout, /--preset-input <key=value>/);
  assert.match(result.stdout, /use the platform-native user ID with --owner-provider and --owner-subject/i);
  assert.match(result.stdout, /Never pass a messaging-platform user ID through --owner-id/i);
  assert.match(result.stdout, /connect their account/i);
  assert.match(result.stdout, /ask for\s+clarification instead of guessing/i);
  assert.match(result.stdout, /tag the person who requested the instance/i);
  assert.match(result.stdout, /what was created and how to open it/i);
});

test('top-level help lists port-forward command', async () => {
  const result = await runCli(['--help']);
  assert.equal(result.code, 0, result.stderr);
  assert.match(
    result.stdout,
    /spritz port-forward <name> \[--namespace <ns>\] --local <port> --remote <port> \[--print\]/,
  );
});
