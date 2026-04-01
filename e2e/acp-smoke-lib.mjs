import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const thisFile = fileURLToPath(import.meta.url);
const rootDir = path.resolve(path.dirname(thisFile), '..');
const cliRequire = createRequire(new URL('../cli/package.json', import.meta.url));

const defaultPromptTemplate = 'Reply with the exact token {{token}} and nothing else.';
const defaultTimeoutSeconds = 300;
const defaultACPPort = 2529;
const defaultACPPath = '/';

function normalizePresetID(value) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

/**
 * Resolve the command used to invoke the local spz CLI.
 */
export function resolveSpzCommand(env = process.env, options = {}) {
  const explicitBin = String(env.SPRITZ_SMOKE_SPZ_BIN || '').trim();
  if (explicitBin) {
    return { command: explicitBin, args: [] };
  }
  return {
    command: 'pnpm',
    args: ['--dir', path.join(rootDir, 'cli'), 'exec', 'tsx', 'src/index.ts'],
  };
}

/**
 * Parse a comma-separated preset list into normalized preset ids.
 */
export function parsePresetList(value) {
  return String(value || '')
    .split(',')
    .map((item) => normalizePresetID(item))
    .filter(Boolean);
}

/**
 * Parse and validate CLI arguments for the ACP smoke runner.
 */
export function parseSmokeArgs(argv, env = process.env) {
  const values = {
    ownerId: env.SPRITZ_SMOKE_OWNER_ID || '',
    namespace: env.SPRITZ_SMOKE_NAMESPACE || env.SPRITZ_NAMESPACE || '',
    apiUrl: env.SPRITZ_SMOKE_API_URL || '',
    bearerToken: env.SPRITZ_SMOKE_BEARER_TOKEN || '',
    presets: parsePresetList(env.SPRITZ_SMOKE_PRESETS || ''),
    timeoutSeconds: defaultTimeoutSeconds,
    keep: false,
    promptTemplate: env.SPRITZ_SMOKE_PROMPT || defaultPromptTemplate,
    idempotencyPrefix: env.SPRITZ_SMOKE_IDEMPOTENCY_PREFIX || `spritz-smoke-${Date.now()}`,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    const next = argv[index + 1];
    if (arg === '--owner-id' && next) {
      values.ownerId = next;
      index += 1;
      continue;
    }
    if (arg === '--namespace' && next) {
      values.namespace = next;
      index += 1;
      continue;
    }
    if (arg === '--presets' && next) {
      values.presets = parsePresetList(next);
      index += 1;
      continue;
    }
    if (arg === '--timeout-seconds' && next) {
      values.timeoutSeconds = Number.parseInt(next, 10);
      index += 1;
      continue;
    }
    if (arg === '--prompt' && next) {
      values.promptTemplate = next;
      index += 1;
      continue;
    }
    if (arg === '--idempotency-prefix' && next) {
      values.idempotencyPrefix = next;
      index += 1;
      continue;
    }
    if (arg === '--keep') {
      values.keep = true;
      continue;
    }
    if (arg === '--help') {
      return { values, help: true };
    }
    throw new Error(`unknown argument: ${arg}`);
  }

  if (!values.ownerId.trim()) {
    throw new Error('--owner-id is required');
  }
  if (!String(values.apiUrl).trim()) {
    throw new Error('SPRITZ_SMOKE_API_URL is required');
  }
  if (!String(values.bearerToken).trim()) {
    throw new Error('SPRITZ_SMOKE_BEARER_TOKEN is required');
  }
  if (!values.presets.length) {
    throw new Error('--presets is required');
  }
  if (!Number.isFinite(values.timeoutSeconds) || values.timeoutSeconds <= 0) {
    throw new Error('--timeout-seconds must be a positive integer');
  }

  return { values, help: false };
}

/**
 * Build an isolated spz environment that cannot inherit ambient auth or profile state.
 */
export function buildSmokeSpzEnvironment(baseEnv = process.env, options = {}) {
  const env = { ...baseEnv };
  const keysToClear = [
    'SPRITZ_API_URL',
    'SPRITZ_BEARER_TOKEN',
    'SPRITZ_PROFILE',
    'SPRITZ_OWNER_ID',
    'SPRITZ_USER_ID',
    'SPRITZ_USER_EMAIL',
    'SPRITZ_USER_TEAMS',
    'SPRITZ_NAMESPACE',
    'SPRITZ_CONFIG_DIR',
  ];
  for (const key of keysToClear) {
    delete env[key];
  }
  const apiUrl = String(options.apiUrl || '').trim();
  const bearerToken = String(options.bearerToken || '').trim();
  if (!apiUrl) {
    throw new Error('smoke environment requires an explicit apiUrl');
  }
  if (!bearerToken) {
    throw new Error('smoke environment requires an explicit bearerToken');
  }
  env.SPRITZ_API_URL = apiUrl;
  env.SPRITZ_BEARER_TOKEN = bearerToken;
  if (String(options.namespace || '').trim()) {
    env.SPRITZ_NAMESPACE = String(options.namespace).trim();
  }
  env.SPRITZ_CONFIG_DIR = String(options.configDir || '').trim()
    || path.join(os.tmpdir(), `spritz-smoke-config-${process.pid}-${Date.now()}`);
  return env;
}

/**
 * Build a create-idempotency token that is stable per preset run.
 */
export function buildIdempotencyKey(prefix, presetId) {
  const safePrefix = String(prefix || 'spritz-smoke')
    .trim()
    .replace(/[^a-zA-Z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '') || 'spritz-smoke';
  const safePreset = String(presetId || '')
    .trim()
    .replace(/[^a-zA-Z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return `${safePrefix}-${safePreset || 'instance'}`;
}

/**
 * Extract readable assistant text from an ACP message/update payload.
 */
export function extractACPText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) {
    return value.map((item) => extractACPText(item)).filter(Boolean).join('\n');
  }
  if (typeof value !== 'object') return String(value);
  if (typeof value.text === 'string' && value.text !== '') return value.text;
  if (value.content !== undefined) return extractACPText(value.content);
  if (value.resource) {
    if (typeof value.resource.text === 'string' && value.resource.text !== '') return value.resource.text;
    if (typeof value.resource.uri === 'string') return value.resource.uri;
  }
  return '';
}

/**
 * Join ACP text chunks without introducing artificial separators between them.
 */
export function joinACPTextChunks(values) {
  return Array.isArray(values)
    ? values
        .map((item) => extractACPText(item))
        .filter(Boolean)
        .join('')
    : extractACPText(values);
}

/**
 * Build a deterministic smoke prompt token for a preset run.
 */
export function buildSmokeToken(presetId) {
  return `spritz-smoke-${normalizePresetID(presetId) || 'instance'}`;
}

/**
 * Resolve the ACP endpoint advertised by a ready spritz, falling back to the reserved defaults.
 */
export function resolveACPEndpoint(spritz) {
  const endpoint = spritz?.status?.acp?.endpoint || {};
  const parsedPort = Number.parseInt(String(endpoint.port ?? ''), 10);
  const port = Number.isFinite(parsedPort) && parsedPort > 0 ? parsedPort : defaultACPPort;
  const rawPath = String(endpoint.path || defaultACPPath).trim();
  const pathValue = rawPath ? (rawPath.startsWith('/') ? rawPath : `/${rawPath}`) : defaultACPPath;
  return { port, path: pathValue };
}

/**
 * Load a Node-compatible WebSocket client constructor from the CLI dependency set.
 */
export function resolveWebSocketConstructor(options = {}) {
  const globalObject = options.globalObject ?? globalThis;
  if (typeof globalObject?.WebSocket === 'function') {
    return globalObject.WebSocket;
  }
  const requireFn = options.requireFn ?? cliRequire;
  const wsModule = requireFn('ws');
  return wsModule.WebSocket || wsModule.default || wsModule;
}

/**
 * Wait for the initial ACP WebSocket handshake to complete within a bounded timeout.
 */
export async function waitForWebSocketOpen(socket, timeoutMs) {
  return new Promise((resolve, reject) => {
    let settled = false;
    let timer = null;

    const cleanup = () => {
      if (timer) clearTimeout(timer);
      socket.removeEventListener?.('open', handleOpen);
      socket.removeEventListener?.('error', handleError);
    };

    const fail = (error) => {
      if (settled) return;
      settled = true;
      cleanup();
      try {
        socket.close?.();
      } catch {}
      try {
        socket.terminate?.();
      } catch {}
      reject(error);
    };

    const handleOpen = () => {
      if (settled) return;
      settled = true;
      cleanup();
      resolve();
    };

    const handleError = (event) => {
      fail(new Error(`ACP websocket failed: ${event?.message || 'unknown error'}`));
    };

    socket.addEventListener('open', handleOpen, { once: true });
    socket.addEventListener('error', handleError, { once: true });
    timer = setTimeout(() => {
      fail(new Error('ACP websocket handshake timed out'));
    }, Math.max(timeoutMs, 1000));
  });
}

/**
 * Validate the service-principal create response contract and return the created instance name.
 */
export function assertSmokeCreateResponse(response, ownerId, presetId) {
  const spritzName = response?.spritz?.metadata?.name;
  if (!spritzName) {
    throw new Error(`create response missing spritz metadata.name: ${JSON.stringify(response, null, 2)}`);
  }
  if (response.ownerId !== ownerId) {
    throw new Error(`expected ownerId ${ownerId}, got ${response.ownerId || '<empty>'}`);
  }
  if (response.actorType !== 'service') {
    throw new Error(`expected actorType service, got ${response.actorType || '<empty>'}`);
  }
  if (!response.chatUrl || !response.instanceUrl || !response.accessUrl) {
    throw new Error(`create response missing canonical URLs: ${JSON.stringify(response, null, 2)}`);
  }
  const expectedPresetID = normalizePresetID(presetId);
  if (response.presetId !== expectedPresetID) {
    throw new Error(`expected presetId ${expectedPresetID || '<empty>'}, got ${response.presetId || '<empty>'}`);
  }
  return spritzName;
}

/**
 * Run a child process and capture stdout/stderr.
 */
export async function runCommand(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    let timedOut = false;
    let killTimer = null;
    child.stdout.on('data', (chunk) => {
      stdout += String(chunk);
    });
    child.stderr.on('data', (chunk) => {
      stderr += String(chunk);
    });
    if (Number.isFinite(options.timeoutMs) && options.timeoutMs > 0) {
      killTimer = setTimeout(() => {
        timedOut = true;
        child.kill('SIGTERM');
        setTimeout(() => {
          if (child.exitCode === null) {
            child.kill('SIGKILL');
          }
        }, 1000).unref?.();
      }, options.timeoutMs);
      killTimer.unref?.();
    }
    child.on('error', reject);
    child.on('close', (code, signal) => {
      if (killTimer) clearTimeout(killTimer);
      resolve({ code: timedOut ? 124 : (code ?? 1), signal, stdout, stderr, timedOut });
    });
  });
}

/**
 * Determine whether a command failure clearly represents an authorization deny.
 */
export function isForbiddenFailure(result) {
  if (!result || result.code === 0) {
    return false;
  }
  const combined = `${result.stderr || ''}\n${result.stdout || ''}`;
  return /forbidden/i.test(combined);
}

/**
 * Find an available localhost port for port-forwarding.
 */
export async function findFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      const port = typeof address === 'object' && address ? address.port : null;
      server.close((error) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(port);
      });
    });
  });
}
