import { spawn } from 'node:child_process';
import { createRequire } from 'node:module';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const thisFile = fileURLToPath(import.meta.url);
const rootDir = path.resolve(path.dirname(thisFile), '..');
const cliRequire = createRequire(new URL('../cli/package.json', import.meta.url));

const defaultPromptTemplate = 'Reply with the exact token {{token}} and nothing else.';
const defaultTimeoutSeconds = 300;

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
  if (options.hasSpzOnPath) {
    return { command: 'spz', args: [] };
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
    namespace: env.SPRITZ_NAMESPACE || env.SPRITZ_SMOKE_NAMESPACE || '',
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
  if (!values.presets.length) {
    throw new Error('--presets is required');
  }
  if (!Number.isFinite(values.timeoutSeconds) || values.timeoutSeconds <= 0) {
    throw new Error('--timeout-seconds must be a positive integer');
  }

  return { values, help: false };
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
  return `${safePrefix}-${safePreset || 'workspace'}`;
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
  if (typeof value.text === 'string') return value.text;
  if (value.content !== undefined) return extractACPText(value.content);
  if (value.resource) {
    if (typeof value.resource.text === 'string') return value.resource.text;
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
  return `spritz-smoke-${normalizePresetID(presetId) || 'workspace'}`;
}

/**
 * Load a Node-compatible WebSocket client constructor from the CLI dependency set.
 */
export function resolveWebSocketConstructor() {
  const wsModule = cliRequire('ws');
  return wsModule.WebSocket || wsModule.default || wsModule;
}

/**
 * Validate the service-principal create response contract and return the created workspace name.
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
  if (!response.chatUrl || !response.workspaceUrl || !response.accessUrl) {
    throw new Error(`create response missing canonical URLs: ${JSON.stringify(response, null, 2)}`);
  }
  const expectedPresetID = normalizePresetID(presetId);
  if (response.presetId !== expectedPresetID) {
    throw new Error(`expected presetId ${expectedPresetID || '<empty>'}, got ${response.presetId || '<empty>'}`);
  }
  return spritzName;
}

/**
 * Produce a human-readable stage summary from Spritz and pod state.
 */
export function summarizeWorkspaceFailure({ spritz, podList }) {
  const status = spritz?.status || {};
  const pod = Array.isArray(podList?.items) ? podList.items[0] : null;
  if (!pod) {
    return {
      stage: 'create',
      message: status.message || 'workspace pod not created',
    };
  }

  const initStatuses = Array.isArray(pod.status?.initContainerStatuses) ? pod.status.initContainerStatuses : [];
  for (const container of initStatuses) {
    const waiting = container?.state?.waiting;
    if (!waiting) continue;
    if (container.name === 'shared-mounts-init') {
      return {
        stage: 'shared-mount-init',
        message: waiting.message || waiting.reason || 'shared mount init is blocked',
      };
    }
    return {
      stage: 'init',
      message: waiting.message || waiting.reason || `${container.name} init container is blocked`,
    };
  }

  const statuses = Array.isArray(pod.status?.containerStatuses) ? pod.status.containerStatuses : [];
  for (const container of statuses) {
    const waiting = container?.state?.waiting;
    if (!waiting) continue;
    if (waiting.reason === 'ImagePullBackOff' || waiting.reason === 'ErrImagePull') {
      return {
        stage: 'image-pull',
        message: waiting.message || waiting.reason,
      };
    }
    return {
      stage: 'startup',
      message: waiting.message || waiting.reason || `${container.name} is waiting`,
    };
  }

  const terminated = statuses.find((container) => container?.state?.terminated);
  if (terminated?.state?.terminated) {
    return {
      stage: 'startup',
      message:
        terminated.state.terminated.message ||
        terminated.state.terminated.reason ||
        `${terminated.name} terminated unexpectedly`,
    };
  }

  if (status.phase && status.phase !== 'Ready') {
    return {
      stage: 'readiness',
      message: status.message || `workspace phase is ${status.phase}`,
    };
  }

  if (status.acp?.state && status.acp.state !== 'ready') {
    return {
      stage: 'acp',
      message: status.message || `ACP state is ${status.acp.state}`,
    };
  }

  return {
    stage: 'unknown',
    message: status.message || 'workspace did not become ready',
  };
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
    child.stdout.on('data', (chunk) => {
      stdout += String(chunk);
    });
    child.stderr.on('data', (chunk) => {
      stderr += String(chunk);
    });
    child.on('error', reject);
    child.on('close', (code, signal) => {
      resolve({ code: code ?? 1, signal, stdout, stderr });
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
