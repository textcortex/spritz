import { spawn } from 'node:child_process';
import net from 'node:net';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const thisFile = fileURLToPath(import.meta.url);
const rootDir = path.resolve(path.dirname(thisFile), '..');

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
    .map((item) => item.trim())
    .filter(Boolean);
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
  return `spritz-smoke-${String(presetId || 'workspace').replace(/[^a-zA-Z0-9-]+/g, '-')}`;
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
