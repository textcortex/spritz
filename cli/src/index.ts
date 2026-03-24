#!/usr/bin/env node

import { spawn, spawnSync } from 'node:child_process';
import { closeSync, openSync, readlinkSync, writeFileSync, writeSync } from 'node:fs';
import { chmod, mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline/promises';
import WebSocket, { RawData } from 'ws';
import { terminalHardResetSequence, terminalResetSequence } from './terminal_sequences.js';

type ProfileConfig = {
  apiUrl?: string;
  bearerToken?: string;
  userId?: string;
  userEmail?: string;
  userTeams?: string;
  namespace?: string;
};

type SpritzConfig = {
  currentProfile?: string;
  profiles: Record<string, ProfileConfig>;
};

type TerminalSessionInfo = {
  mode?: string;
  available?: boolean;
  sessions?: string[];
  default_session?: string;
};

type SkillflagModule = typeof import('skillflag');
type Audience = 'human' | 'agent';
type AudienceGuidance = {
  audience: Audience;
  usageNote: string;
  createOwnershipGuidance: string[];
  reportingGuidance: string[];
  missingOwnerInputGuidance: string[];
  unresolvedExternalOwnerGuidance: (provider: string) => string[];
};

type TtyContext = {
  ttyPath: string | null;
  ttyState: string | null;
};

const defaultApiBase = 'http://localhost:8080/api';
const requestTimeoutMs = Number.parseInt(process.env.SPRITZ_REQUEST_TIMEOUT_MS || '10000', 10);
const headerId = process.env.SPRITZ_API_HEADER_ID || 'X-Spritz-User-Id';
const headerEmail = process.env.SPRITZ_API_HEADER_EMAIL || 'X-Spritz-User-Email';
const headerTeams = process.env.SPRITZ_API_HEADER_TEAMS || 'X-Spritz-User-Teams';
const sshKeygenBinary = process.env.SPRITZ_SSH_KEYGEN || 'ssh-keygen';
const sshBinary = process.env.SPRITZ_SSH_BINARY || 'ssh';
const terminalTransportDefault = (process.env.SPRITZ_TERMINAL_TRANSPORT || 'ws').toLowerCase();
const configRoot = process.env.SPRITZ_CONFIG_DIR || path.join(os.homedir(), '.config', 'spritz');
const configPath = path.join(configRoot, 'config.json');
let cachedConfig: SpritzConfig | null = null;
let cachedTtyPath: string | null | undefined;

const [, , command, ...rest] = process.argv;

const watchdogFlag = 'SPRITZ_TTY_WATCHDOG';
const ttyWatchdogIntervalMs = 250;
const ttyRestoreBanner = '\r\n[spz] terminal restored after disconnect\r\n';
const sttyBinary = process.env.SPRITZ_STTY_BINARY || 'stty';
const resetBinary = process.env.SPRITZ_RESET_BINARY || 'reset';
let skillflagModulePromise: Promise<SkillflagModule> | undefined;

class SpritzRequestError extends Error {
  statusCode: number;
  code?: string;
  data?: any;

  constructor(message: string, options: { statusCode: number; code?: string; data?: any }) {
    super(message);
    this.name = 'SpritzRequestError';
    this.statusCode = options.statusCode;
    this.code = options.code;
    this.data = options.data;
  }
}

function loadSkillflagModule(): Promise<SkillflagModule> {
  skillflagModulePromise ??= import('skillflag');
  return skillflagModulePromise;
}

function shouldMaybeHandleSkillflag(argv: string[]): boolean {
  return argv.some((token) => token === '--skill' || token.startsWith('--skill='));
}

/**
 * Build platform-specific stty args that target a specific tty path.
 */
function sttyArgsForPath(path: string, args: string[]): string[] {
  if (process.platform === 'darwin') {
    return ['-f', path, ...args];
  }
  return ['-F', path, ...args];
}

/**
 * Resolve the current terminal device path (e.g. /dev/ttys003) if available.
 */
function resolveTtyPath(): string | null {
  if (cachedTtyPath !== undefined) return cachedTtyPath;
  if (process.platform === 'win32') {
    cachedTtyPath = null;
    return null;
  }
  if (!process.stdin.isTTY && !process.stdout.isTTY) {
    cachedTtyPath = null;
    return null;
  }
  if (process.env.TTY) {
    cachedTtyPath = process.env.TTY;
    return cachedTtyPath;
  }
  const candidates = ['/dev/fd/0', '/proc/self/fd/0', '/dev/fd/1', '/proc/self/fd/1'];
  for (const candidate of candidates) {
    try {
      const target = readlinkSync(candidate);
      if (target && target.startsWith('/dev/')) {
        cachedTtyPath = target;
        return target;
      }
    } catch {
      // ignore
    }
  }
  try {
    const stdin = process.stdin.isTTY ? 0 : 'ignore';
    const result = spawnSync('tty', [], { stdio: [stdin, 'pipe', 'ignore'] });
    const output = result.stdout?.toString().trim();
    if (output && output.startsWith('/dev/')) {
      cachedTtyPath = output;
      return output;
    }
  } catch {
    // ignore
  }
  cachedTtyPath = null;
  return null;
}

function withTtyFd(mode: 'r' | 'w' | 'r+' | 'w+', fn: (fd: number) => void, ttyPath?: string | null) {
  try {
    const path = ttyPath || resolveTtyPath() || '/dev/tty';
    const fd = openSync(path, mode);
    try {
      fn(fd);
    } finally {
      closeSync(fd);
    }
  } catch {
    // ignore
  }
}

function writeToTty(payload: string, ttyPath?: string | null) {
  if (process.stdout.isTTY) {
    try {
      process.stdout.write(payload);
      return;
    } catch {
      // ignore
    }
  }
  withTtyFd('w', (fd) => {
    try {
      writeSync(fd, payload);
    } catch {
      // ignore
    }
  }, ttyPath);
}

/**
 * Capture the terminal's current stty state for later restoration.
 */
function captureTtyState(ttyPath?: string | null): string | null {
  if (process.platform === 'win32') return null;
  let state: string | null = null;
  try {
    if (ttyPath) {
      const result = spawnSync(sttyBinary, sttyArgsForPath(ttyPath, ['-g']), {
        stdio: ['ignore', 'pipe', 'ignore'],
      });
      state = result.stdout?.toString().trim() || null;
    } else if (process.stdin.isTTY) {
      const result = spawnSync(sttyBinary, ['-g'], { stdio: [0, 'pipe', 'ignore'] });
      state = result.stdout?.toString().trim() || null;
    } else {
      withTtyFd(
        'r',
        (fd) => {
          const result = spawnSync(sttyBinary, ['-g'], { stdio: [fd, 'pipe', 'ignore'] });
          state = result.stdout?.toString().trim() || null;
        },
        ttyPath
      );
    }
  } catch {
    return null;
  }
  return state;
}

/**
 * Restore tty modes + keyboard reporting and optionally issue a hard reset.
 */
function restoreLocalTerminal(ttyState?: string | null, ttyPath?: string | null, hard = false) {
  writeToTty(hard ? terminalHardResetSequence : terminalResetSequence, ttyPath);
  if (process.platform !== 'win32') {
    try {
      const args = ttyState ? [ttyState] : ['sane'];
      if (ttyPath) {
        spawnSync(sttyBinary, sttyArgsForPath(ttyPath, args), { stdio: ['ignore', 'ignore', 'ignore'] });
      } else if (process.stdin.isTTY) {
        spawnSync(sttyBinary, args, { stdio: [0, 'ignore', 'ignore'] });
      } else {
        withTtyFd('r', (fd) => {
          spawnSync(sttyBinary, args, { stdio: [fd, 'ignore', 'ignore'] });
        }, ttyPath);
      }
    } catch {
      // ignore
    }
    try {
      if (ttyPath) {
        withTtyFd(
          'r+',
          (fd) => {
            spawnSync(resetBinary, [], { stdio: [fd, fd, 'ignore'] });
          },
          ttyPath
        );
      } else if (process.stdin.isTTY) {
        spawnSync(resetBinary, [], { stdio: [0, 'ignore', 'ignore'] });
      } else {
        withTtyFd('r+', (fd) => {
          spawnSync(resetBinary, [], { stdio: [fd, 'ignore', 'ignore'] });
        }, ttyPath);
      }
    } catch {
      // ignore
    }
  }
}

/**
 * Capture a known-good tty baseline and return a context for later restoration.
 */
function captureTtyContext(): TtyContext {
  const ttyPath = resolveTtyPath();
  const ttyState = captureTtyState(ttyPath);
  if (!ttyState) {
    // Best effort fallback to recover a broken terminal when state capture fails.
    restoreLocalTerminal(undefined, ttyPath);
  }
  return { ttyPath, ttyState };
}

/**
 * Restore the tty based on a previously captured context.
 */
function restoreTtyContext(context: TtyContext, hard = false) {
  restoreLocalTerminal(context.ttyState, context.ttyPath, hard);
}

/**
 * Spawn a watchdog that resets the tty if the parent process is killed.
 */
function startTtyWatchdog(context: TtyContext): (() => void) | null {
  if (process.env[watchdogFlag] === '1') return null;
  if (!process.stdin.isTTY && !process.stdout.isTTY) return null;
  const ttyPath = context.ttyPath;
  const cancelPath = path.join(
    os.tmpdir(),
    `spritz-tty-cancel-${process.pid}-${Date.now()}-${Math.random().toString(16).slice(2)}`
  );
  let inheritedTtyFd: number | null = null;
  try {
    const path = ttyPath || '/dev/tty';
    try {
      inheritedTtyFd = openSync(path, 'r+');
    } catch {
      try {
        inheritedTtyFd = openSync(path, 'w');
      } catch {
        inheritedTtyFd = null;
      }
    }
  } catch {
    inheritedTtyFd = null;
  }
  const payload = JSON.stringify(terminalResetSequence);
  const hardPayload = JSON.stringify(terminalHardResetSequence);
  const state = context.ttyState ? JSON.stringify(context.ttyState) : 'null';
  const script = `
    const { openSync, writeSync, closeSync, existsSync, unlinkSync } = require('fs');
    const { spawnSync } = require('child_process');
    const pid = ${process.pid};
    const payload = ${payload};
    const hardPayload = ${hardPayload};
    const ttyPath = ${ttyPath ? JSON.stringify(ttyPath) : 'null'};
    const inheritedFd = ${inheritedTtyFd !== null ? 3 : 'null'};
    const cancelPath = ${JSON.stringify(cancelPath)};
    const ttyState = ${state};
    const sttyBin = ${JSON.stringify(sttyBinary)};
    const resetBin = ${JSON.stringify(resetBinary)};
    const sttyFlag = ${JSON.stringify(process.platform === 'darwin' ? '-f' : '-F')};
    let fd = null;
    function openFd() {
      if (inheritedFd !== null) return inheritedFd;
      if (fd !== null) return fd;
      const path = ttyPath || '/dev/tty';
      try {
        fd = openSync(path, 'r+');
        return fd;
      } catch {}
      try {
        fd = openSync(path, 'w');
        return fd;
      } catch {}
      try {
        fd = openSync(path, 'r');
        return fd;
      } catch {}
      return null;
    }
    function alive() {
      try { process.kill(pid, 0); return true; } catch { return false; }
    }
    function doReset(hard) {
      const handle = openFd();
      if (handle !== null) {
        try { writeSync(handle, hard ? hardPayload : payload); } catch {}
        try {
          const args = ttyState ? [ttyState] : ['sane'];
          if (ttyPath) {
            spawnSync(sttyBin, [sttyFlag, ttyPath, ...args], { stdio: ['ignore', 'ignore', 'ignore'] });
          } else {
            spawnSync(sttyBin, args, { stdio: [handle, handle, 'ignore'] });
          }
        } catch {}
        try {
          spawnSync(resetBin, [], { stdio: [handle, handle, 'ignore'] });
        } catch {}
      }
    }
    const banner = ${JSON.stringify(ttyRestoreBanner)};
    const interval = setInterval(() => {
      if (!alive()) {
        clearInterval(interval);
        if (existsSync(cancelPath)) {
          try { unlinkSync(cancelPath); } catch {}
          process.exit(0);
        }
        doReset(true);
        const handle = openFd();
        if (handle !== null) {
          try { writeSync(handle, banner); } catch {}
        }
        if (fd !== null && inheritedFd === null) {
          try { closeSync(fd); } catch {}
        }
        process.exit(0);
      }
    }, ${ttyWatchdogIntervalMs});
  `;
  const child = spawn(process.execPath, ['-e', script], {
    detached: true,
    stdio: inheritedTtyFd !== null ? ['ignore', 'ignore', 'ignore', inheritedTtyFd] : 'ignore',
    env: { ...process.env, [watchdogFlag]: '1' },
  });
  child.unref();
  if (inheritedTtyFd !== null) {
    try {
      closeSync(inheritedTtyFd);
    } catch {
      // ignore
    }
  }
  return () => {
    try {
      writeFileSync(cancelPath, '1');
    } catch {
      // ignore
    }
  };
}

function resolveAudience(value = process.env.AUDIENCE): Audience {
  return value?.trim().toLowerCase() === 'agent' ? 'agent' : 'human';
}

const audienceGuidanceByAudience: Record<Audience, AudienceGuidance> = {
  human: {
    audience: 'human',
    usageNote: 'Use `spritz create --help` for detailed owner guidance and examples.',
    createOwnershipGuidance: [
      'Use --owner-provider and --owner-subject when you only know a platform-native user ID such as a Discord, Slack, or Teams user.',
      'Use --owner-id only when you already know the canonical internal Spritz owner ID.',
      'If provider, subject, preset, or tenant context is unclear, clarify it before running the create command.',
    ],
    reportingGuidance: [
      'After create succeeds, use the returned instance name and URLs as the source of truth when reporting the result back to the caller.',
    ],
    missingOwnerInputGuidance: [
      'owner input is required.',
      'Use --owner-provider and --owner-subject when you only know a messaging-platform user ID.',
      'Use --owner-id only when you already know the canonical internal Spritz owner ID.',
    ],
    unresolvedExternalOwnerGuidance: (provider: string) => [
      `The ${provider} account could not be resolved to a Spritz owner.`,
      'Ask the user to connect their account in the product or integration that owns this identity mapping, then retry the create request.',
    ],
  },
  agent: {
    audience: 'agent',
    usageNote: 'If a request originated from a messaging app, prefer --owner-provider and --owner-subject with the platform-native user ID.',
    createOwnershipGuidance: [
      'If the request came from Discord, Slack, Teams, or another messaging platform, use the platform-native user ID with --owner-provider and --owner-subject.',
      'Never pass a messaging-platform user ID through --owner-id.',
      'If provider, subject, preset, or tenant context is unclear, ask for clarification instead of guessing.',
      'If external owner resolution fails, tell the caller the user needs to connect their account, then retry with the same platform user ID.',
    ],
    reportingGuidance: [
      'When replying in a messaging app, tag the person who requested the instance.',
      'Say what was created and how to open it, using the returned instance name and URLs from the create response.',
      'Do not construct instance URLs yourself.',
    ],
    missingOwnerInputGuidance: [
      'owner input is required.',
      'If this request came from a messaging app, use the platform-native user ID with --owner-provider and --owner-subject.',
      'Do not ask for or pass a messaging-platform user ID as --owner-id.',
      'If the provider, subject, preset, or tenant is unclear, ask for clarification before retrying.',
    ],
    unresolvedExternalOwnerGuidance: (provider: string) => [
      `The ${provider} account could not be resolved to a Spritz owner.`,
      'Ask the user to connect their account in the product or integration that owns this identity mapping, then retry the create request.',
      'Keep using the platform-native user ID with --owner-provider and --owner-subject.',
    ],
  },
};

function guidanceForAudience(value = process.env.AUDIENCE): AudienceGuidance {
  return audienceGuidanceByAudience[resolveAudience(value)];
}

function renderBullets(lines: string[]): string {
  return lines.map((line) => `  - ${line}`).join('\n');
}

function createUsage(guidance = guidanceForAudience()) {
  const ownerNotes = `Ownership guidance:\n${renderBullets(guidance.createOwnershipGuidance)}\n`;
  const reportingNotes = `Reporting guidance:\n${renderBullets(guidance.reportingGuidance)}\n`;

  console.log(`Spritz create

Usage:
  spritz create [name] [--preset <id>] [--preset-input <key=value>] [--image <image>] [--repo <url>] [--branch <branch>] [--owner-provider <provider> --owner-subject <subject> [--owner-tenant <tenant>] | --owner-id <id>] [--idle-ttl <duration>] [--ttl <duration>] [--idempotency-key <id>] [--source <source>] [--request-id <id>] [--name-prefix <prefix>] [--namespace <ns>]

Environment:
  AUDIENCE (current: ${guidance.audience})

Examples:
  spritz create --preset claude-code --owner-provider discord --owner-subject 123456789012345678 --source discord --request-id discord-123 --idempotency-key discord-123 --json
  spritz create --preset zeno --preset-input agentId=ag-123 --owner-id user-123 --idempotency-key req-456 --json
  spritz create --preset openclaw --owner-id user-123 --idempotency-key req-123 --json

${ownerNotes}
${reportingNotes}`);
}

function chatSendUsage() {
  console.log(`Spritz chat send

Usage:
  spritz chat send (--instance <name> | --conversation <id>) --message <text> [--owner-id <id>] [--reason <text>] [--cwd <path>] [--title <title>] [--namespace <ns>] [--json]

Environment:
  SPRITZ_API_URL (default: ${process.env.SPRITZ_API_URL || defaultApiBase})
  SPRITZ_INTERNAL_TOKEN
  SPRITZ_OWNER_ID, SPRITZ_USER_ID, SPRITZ_PROFILE

Notes:
  --instance creates a new owner-scoped conversation before sending the prompt.
  --conversation sends into an existing owner-scoped conversation.
  --cwd and --title are only used with --instance.
`);
}

function usage(guidance = guidanceForAudience()) {
  console.log(`Spritz CLI

Usage:
  spritz list [--namespace <ns>]
  spritz create [name] [--preset <id>] [--preset-input <key=value>] [--image <image>] [--repo <url>] [--branch <branch>] [--owner-provider <provider> --owner-subject <subject> [--owner-tenant <tenant>] | --owner-id <id>] [--idle-ttl <duration>] [--ttl <duration>] [--idempotency-key <id>] [--source <source>] [--request-id <id>] [--name-prefix <prefix>] [--namespace <ns>]
  spritz suggest-name [--preset <id>] [--image <image>] [--name-prefix <prefix>] [--namespace <ns>]
  spritz delete <name> [--namespace <ns>]
  spritz open <name> [--namespace <ns>]
  spritz terminal <name> [--namespace <ns>] [--session <name>] [--transport <ws|ssh>] [--print]
  spritz ssh <name> [--namespace <ns>] [--session <name>] [--transport <ws|ssh>] [--print]
  spritz chat send (--instance <name> | --conversation <id>) --message <text> [--owner-id <id>] [--reason <text>] [--cwd <path>] [--title <title>] [--namespace <ns>] [--json]
  spritz profile list
  spritz profile current
  spritz profile show [name]
  spritz profile set <name> [--api-url <url>] [--token <token>] [--user-id <id>] [--user-email <email>] [--user-teams <csv>] [--namespace <ns>]
  spritz profile use <name>
  spritz profile delete <name>
  spritz --skill <list|show|export|install> ...

Alias:
  spz (same commands as spritz)

Environment:
  SPRITZ_API_URL (default: ${process.env.SPRITZ_API_URL || defaultApiBase})
  SPRITZ_BEARER_TOKEN
  SPRITZ_INTERNAL_TOKEN
  SPRITZ_USER_ID, SPRITZ_USER_EMAIL, SPRITZ_USER_TEAMS, SPRITZ_OWNER_ID
  SPRITZ_API_HEADER_ID, SPRITZ_API_HEADER_EMAIL, SPRITZ_API_HEADER_TEAMS
  SPRITZ_TERMINAL_TRANSPORT (default: ${terminalTransportDefault})
  SPRITZ_PROFILE, SPRITZ_CONFIG_DIR
  AUDIENCE (default: human, current: ${guidance.audience})

Notes:
  ${guidance.usageNote}
  When ZMX sessions are enabled, detach with Ctrl+\\ and reconnect later.
`);
}

function missingOwnerInputMessage(guidance = guidanceForAudience()): string {
  return guidance.missingOwnerInputGuidance.join(' ');
}

function unresolvedExternalOwnerMessage(error: SpritzRequestError, guidance = guidanceForAudience()): string {
  const provider = typeof error.data?.identity?.provider === 'string' ? error.data.identity.provider : 'external';
  return guidance.unresolvedExternalOwnerGuidance(provider).join('\n');
}

function argValue(flag: string): string | undefined {
  const idx = rest.indexOf(flag);
  if (idx === -1) return undefined;
  return rest[idx + 1];
}

function argValues(flag: string): string[] {
  const values: string[] = [];
  for (let index = 0; index < rest.length; index += 1) {
    if (rest[index] !== flag) continue;
    const value = rest[index + 1];
    if (value && !value.startsWith('--')) {
      values.push(value);
      index += 1;
    }
  }
  return values;
}

function argValueInfo(flag: string): { present: boolean; value?: string } {
  const idx = rest.indexOf(flag);
  if (idx === -1) return { present: false };
  return { present: true, value: rest[idx + 1] };
}

function hasFlag(flag: string): boolean {
  return rest.includes(flag);
}

function positionalArgs(): string[] {
  const values: string[] = [];
  for (let index = 0; index < rest.length; index += 1) {
    const token = rest[index];
    if (token.startsWith('--')) {
      const next = rest[index + 1];
      if (next && !next.startsWith('--')) {
        index += 1;
      }
      continue;
    }
    values.push(token);
  }
  return values;
}

function parsePresetInputs(values: string[]): Record<string, string> | undefined {
  if (values.length === 0) return undefined;
  const out: Record<string, string> = {};
  for (const item of values) {
    const index = item.indexOf('=');
    if (index <= 0 || index === item.length - 1) {
      throw new Error('--preset-input must use key=value');
    }
    const key = item.slice(0, index).trim();
    const value = item.slice(index + 1).trim();
    if (!key || !value) {
      throw new Error('--preset-input must use key=value');
    }
    out[key] = value;
  }
  return out;
}

function normalizeHeaders(headers?: HeadersInit): Record<string, string> {
  if (!headers) return {};
  if (headers instanceof Headers) {
    const out: Record<string, string> = {};
    headers.forEach((value, key) => {
      out[key] = value;
    });
    return out;
  }
  if (Array.isArray(headers)) {
    return Object.fromEntries(headers);
  }
  return { ...headers };
}

async function runCommand(command: string, args: string[], options: { stdio?: 'ignore' | 'inherit' } = {}) {
  await new Promise<void>((resolve, reject) => {
    const child = spawn(command, args, { stdio: options.stdio || 'ignore' });
    child.on('error', reject);
    child.on('exit', (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`${command} exited with code ${code ?? 'unknown'}`));
      }
    });
  });
}

async function generateSSHKeypair() {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'spritz-ssh-'));
  const privateKeyPath = path.join(dir, 'id_ed25519');
  await runCommand(sshKeygenBinary, ['-t', 'ed25519', '-f', privateKeyPath, '-N', '', '-q']);
  await chmod(privateKeyPath, 0o600);
  const publicKeyPath = `${privateKeyPath}.pub`;
  const publicKey = await readFile(publicKeyPath, 'utf8');
  return { dir, privateKeyPath, publicKey };
}

function normalizeProfileName(value: string): string {
  const name = value.trim();
  if (!name) {
    throw new Error('profile name is required');
  }
  if (!/^[A-Za-z0-9_-]+$/.test(name)) {
    throw new Error('profile name must be alphanumeric and may include - or _');
  }
  return name;
}

function normalizeProfileValue(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const lowered = trimmed.toLowerCase();
  if (lowered === 'none' || lowered === 'null') return undefined;
  return trimmed;
}

/**
 * Load CLI profile configuration from disk (cached per process).
 */
async function loadConfig(): Promise<SpritzConfig> {
  if (cachedConfig) return cachedConfig;
  try {
    const raw = await readFile(configPath, 'utf8');
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object' && parsed.profiles && typeof parsed.profiles === 'object') {
      cachedConfig = {
        currentProfile: typeof parsed.currentProfile === 'string' ? parsed.currentProfile : undefined,
        profiles: parsed.profiles as Record<string, ProfileConfig>,
      };
      return cachedConfig;
    }
  } catch {
    // ignore missing/invalid config
  }
  cachedConfig = { profiles: {} };
  return cachedConfig;
}

/**
 * Persist CLI profile configuration to disk and refresh cache.
 */
async function saveConfig(config: SpritzConfig): Promise<void> {
  await mkdir(configRoot, { recursive: true });
  await writeFile(configPath, `${JSON.stringify(config, null, 2)}\n`, 'utf8');
  cachedConfig = config;
}

/**
 * Resolve the active profile using CLI flags, environment, and config state.
 */
async function resolveProfile(options?: { allowFlag?: boolean }): Promise<{ name?: string; profile?: ProfileConfig; config: SpritzConfig }> {
  const config = await loadConfig();
  const fromFlag = options?.allowFlag ? argValue('--profile') : undefined;
  const name = fromFlag || process.env.SPRITZ_PROFILE || config.currentProfile;
  const profile = name ? config.profiles[name] : undefined;
  return { name, profile, config };
}

/**
 * Resolve the API base URL from env or active profile.
 */
async function resolveApiBase(): Promise<string> {
  if (process.env.SPRITZ_API_URL) return process.env.SPRITZ_API_URL;
  const { profile } = await resolveProfile({ allowFlag: true });
  return profile?.apiUrl || defaultApiBase;
}

function normalizeTransport(value: string | undefined): 'ws' | 'ssh' {
  if (!value) return 'ws';
  const normalized = value.trim().toLowerCase();
  if (['ws', 'websocket', 'terminal'].includes(normalized)) return 'ws';
  if (normalized === 'ssh') return 'ssh';
  throw new Error(`unsupported transport: ${value}`);
}

function resolveTransport(): 'ws' | 'ssh' {
  const flag = argValue('--transport');
  if (flag) return normalizeTransport(flag);
  return normalizeTransport(terminalTransportDefault);
}

function isJSend(payload: any): payload is { status: string; data?: any; message?: string } {
  return payload && typeof payload === 'object' && typeof payload.status === 'string';
}

async function authHeaders(): Promise<Record<string, string>> {
  const { profile } = await resolveProfile({ allowFlag: true });
  const token = argValue('--token') || process.env.SPRITZ_BEARER_TOKEN || profile?.bearerToken;
  if (token?.trim()) {
    return { Authorization: `Bearer ${token.trim()}` };
  }
  const headers: Record<string, string> = {};
  const userId = process.env.SPRITZ_USER_ID || profile?.userId || process.env.USER;
  const userEmail = process.env.SPRITZ_USER_EMAIL || profile?.userEmail;
  const userTeams = process.env.SPRITZ_USER_TEAMS || profile?.userTeams;
  if (userId) headers[headerId] = userId;
  if (userEmail) headers[headerEmail] = userEmail;
  if (userTeams) headers[headerTeams] = userTeams;
  return headers;
}

async function resolveDefaultOwnerId(): Promise<string | undefined> {
  const { profile } = await resolveProfile({ allowFlag: true });
  return (
    process.env.SPRITZ_OWNER_ID ||
    process.env.SPRITZ_USER_ID ||
    profile?.userId ||
    process.env.USER ||
    undefined
  );
}

function resolveInternalToken(): string {
  const token = argValue('--internal-token') || process.env.SPRITZ_INTERNAL_TOKEN;
  if (!token?.trim()) {
    throw new Error('SPRITZ_INTERNAL_TOKEN or --internal-token is required for chat send');
  }
  return token.trim();
}

async function request(path: string, init?: RequestInit) {
  const controller = new AbortController();
  const timeoutMs = Number.isFinite(requestTimeoutMs) ? requestTimeoutMs : 10000;
  const timeout = setTimeout(() => controller.abort(), Math.max(timeoutMs, 1000));
  const mergedHeaders = {
    ...(await authHeaders()),
    ...normalizeHeaders(init?.headers),
  };
  const apiBase = await resolveApiBase();
  const res = await fetch(`${apiBase}${path}`, {
    ...init,
    headers: mergedHeaders,
    signal: controller.signal,
  }).finally(() => clearTimeout(timeout));
  const text = await res.text();
  let data: any = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  const jsend = isJSend(data) ? data : null;
  if (!res.ok || (res.ok && jsend && jsend.status !== 'success')) {
    const errorCode =
      (jsend && typeof jsend.data?.error === 'string' ? jsend.data.error : undefined) ||
      undefined;
    const errorData = jsend?.data;
    const message =
      (jsend && (jsend.message || jsend.data?.message || jsend.data?.error)) ||
      text ||
      res.statusText ||
      'Request failed';
    throw new SpritzRequestError(message, { statusCode: res.status, code: errorCode, data: errorData });
  }
  if (res.status === 204) return null;
  if (jsend) return jsend.data ?? null;
  if (data !== null) return data;
  return text ? text : null;
}

async function internalRequest(path: string, init?: RequestInit) {
  const controller = new AbortController();
  const timeoutMs = Number.isFinite(requestTimeoutMs) ? requestTimeoutMs : 10000;
  const timeout = setTimeout(() => controller.abort(), Math.max(timeoutMs, 1000));
  const mergedHeaders = {
    Authorization: `Bearer ${resolveInternalToken()}`,
    ...normalizeHeaders(init?.headers),
  };
  const apiBase = await resolveApiBase();
  const res = await fetch(`${apiBase}${path}`, {
    ...init,
    headers: mergedHeaders,
    signal: controller.signal,
  }).finally(() => clearTimeout(timeout));
  const text = await res.text();
  let data: any = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  const jsend = isJSend(data) ? data : null;
  if (!res.ok || (res.ok && jsend && jsend.status !== 'success')) {
    const message =
      (jsend && (jsend.message || jsend.data?.message || jsend.data?.error)) ||
      text ||
      res.statusText ||
      'Request failed';
    throw new SpritzRequestError(message, { statusCode: res.status, data: jsend?.data });
  }
  if (res.status === 204) return null;
  if (jsend) return jsend.data ?? null;
  if (data !== null) return data;
  return text ? text : null;
}

function defaultTerminalSessionName(name: string, namespace?: string): string {
  const ns = namespace?.trim() || 'default';
  return `spritz:${ns}:${name}`;
}

async function fetchTerminalSessions(name: string, namespace?: string): Promise<TerminalSessionInfo | null> {
  const qs = namespace ? `?namespace=${encodeURIComponent(namespace)}` : '';
  try {
    const data = await request(`/spritzes/${encodeURIComponent(name)}/terminal/sessions${qs}`);
    return (data || null) as TerminalSessionInfo | null;
  } catch {
    return null;
  }
}

function normalizeSessionList(value: TerminalSessionInfo['sessions']): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  const seen = new Set<string>();
  value.forEach((item) => {
    if (typeof item !== 'string') return;
    const trimmed = item.trim();
    if (!trimmed || seen.has(trimmed)) return;
    seen.add(trimmed);
    out.push(trimmed);
  });
  return out;
}

function pickDefaultSession(sessions: string[], fallback?: string): { session: string; index: number } {
  if (sessions.length === 0 && fallback) return { session: fallback, index: -1 };
  const idx = fallback ? sessions.indexOf(fallback) : -1;
  if (idx >= 0) return { session: sessions[idx], index: idx };
  return { session: sessions[0], index: 0 };
}

async function promptSessionChoice(sessions: string[], fallback?: string, interactive = true): Promise<string> {
  const { session: defaultSession, index: defaultIndex } = pickDefaultSession(sessions, fallback);
  if (!interactive || !process.stdin.isTTY) {
    console.error(`Multiple terminal sessions detected; selecting ${defaultSession}.`);
    return defaultSession;
  }
  console.error('Multiple terminal sessions detected:');
  sessions.forEach((name, idx) => {
    const marker = idx === defaultIndex ? '*' : ' ';
    console.error(` ${idx + 1}) ${name} ${marker}`);
  });
  const rl = readline.createInterface({ input: process.stdin, output: process.stderr });
  try {
    const answer = (await rl.question(`Select session [${defaultIndex + 1}]: `)).trim();
    if (!answer) return defaultSession;
    const idx = Number.parseInt(answer, 10);
    if (!Number.isNaN(idx) && idx >= 1 && idx <= sessions.length) {
      return sessions[idx - 1];
    }
    const exact = sessions.find((session) => session === answer);
    if (exact) return exact;
    console.error(`Invalid selection "${answer}", using ${defaultSession}.`);
    return defaultSession;
  } finally {
    rl.close();
  }
}

async function resolveTerminalSession(
  name: string,
  namespace: string | undefined,
  sessionFlagInfo: { present: boolean; value?: string },
  printOnly: boolean,
): Promise<{ session?: string; useZmx: boolean }> {
  if (sessionFlagInfo.present && !sessionFlagInfo.value) {
    throw new Error('--session requires a value');
  }
  const sessionFlag = sessionFlagInfo.value?.trim();
  const info = await fetchTerminalSessions(name, namespace);
  if (!info) {
    if (sessionFlag) {
      console.error('Unable to verify ZMX sessions; attempting to attach to requested session.');
      return { useZmx: false, session: sessionFlag };
    }
    return { useZmx: false };
  }
  const mode = info?.mode?.toLowerCase();
  const available = Boolean(info?.available);
  if (mode !== 'zmx' || !available) {
    if (sessionFlag) {
      console.error('ZMX sessions unavailable; ignoring --session.');
    }
    return { useZmx: false };
  }

  if (sessionFlag) {
    return { useZmx: true, session: sessionFlag };
  }

  const sessions = normalizeSessionList(info?.sessions);
  const defaultSession = (info?.default_session || defaultTerminalSessionName(name, namespace)).trim();
  if (sessions.length === 0) {
    return { useZmx: true, session: defaultSession };
  }
  if (sessions.length === 1) {
    return { useZmx: true, session: sessions[0] };
  }
  const selected = await promptSessionChoice(sessions, defaultSession, !printOnly);
  return { useZmx: true, session: selected };
}

function terminalWsUrl(apiBase: string, name: string, namespace?: string, session?: string): { url: string; origin: string } {
  const baseUrl = new URL(apiBase);
  const basePath = baseUrl.pathname.replace(/\/$/, '');
  baseUrl.pathname = `${basePath}/spritzes/${encodeURIComponent(name)}/terminal`;
  if (namespace) {
    baseUrl.searchParams.set('namespace', namespace);
  }
  if (session) {
    baseUrl.searchParams.set('session', session);
  }
  const origin = baseUrl.origin;
  baseUrl.protocol = baseUrl.protocol === 'https:' ? 'wss:' : 'ws:';
  return { url: baseUrl.toString(), origin };
}

function terminalResizePayload(): string {
  const cols = process.stdout.columns ?? 80;
  const rows = process.stdout.rows ?? 24;
  return JSON.stringify({ type: 'resize', cols, rows });
}

function writeTerminalOutput(data: RawData) {
  if (typeof data === 'string') {
    process.stdout.write(data);
    return;
  }
  if (Array.isArray(data)) {
    data.forEach((chunk) => process.stdout.write(chunk));
    return;
  }
  if (data instanceof ArrayBuffer) {
    process.stdout.write(Buffer.from(data));
    return;
  }
  process.stdout.write(data);
}

/**
 * Opens a terminal session over WebSocket and wires stdin/stdout.
 */
async function openTerminalWs(name: string, namespace: string | undefined, printOnly: boolean, session?: string) {
  const apiBase = await resolveApiBase();
  const { url, origin } = terminalWsUrl(apiBase, name, namespace, session);
  if (printOnly) {
    console.log(url);
    return;
  }
  const ttyContext = captureTtyContext();
  const cancelWatchdog = startTtyWatchdog(ttyContext);
  const headers: Record<string, string> = {
    ...(await authHeaders()),
    Origin: origin,
  };
  const ws = new WebSocket(url, {
    headers,
    handshakeTimeout: Number.isFinite(requestTimeoutMs) ? requestTimeoutMs : 10000,
  });
  ws.binaryType = 'nodebuffer';

  const stdin = process.stdin;
  const stdout = process.stdout;
  const wasRaw = stdin.isTTY ? stdin.isRaw : false;
  const onData = (chunk: Buffer) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(chunk);
    }
  };
  const onResize = () => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(terminalResizePayload());
    }
  };
  const onExit = () => {
    cancelWatchdog?.();
    restoreTtyContext(ttyContext);
  };
  const onSignal = () => {
    cancelWatchdog?.();
    restoreTtyContext(ttyContext);
    if (ws.readyState === WebSocket.OPEN) {
      ws.close();
    }
  };

  let finished = false;
  const finalize = () => {
    if (finished) return;
    finished = true;
    stdin.off('data', onData);
    if (stdin.isTTY) {
      try {
        stdin.setRawMode(Boolean(wasRaw));
      } catch {
        // ignore
      }
    }
    stdin.pause();
    if (stdout.isTTY) {
      stdout.off('resize', onResize);
    }
    process.off('SIGWINCH', onResize);
    process.off('SIGINT', onSignal);
    process.off('SIGTERM', onSignal);
    process.off('SIGHUP', onSignal);
    process.off('exit', onExit);
    cancelWatchdog?.();
    restoreTtyContext(ttyContext);
  };

  await new Promise<void>((resolve, reject) => {
    ws.on('open', () => {
      process.on('exit', onExit);
      if (stdin.isTTY) {
        stdin.setRawMode(true);
      }
      stdin.resume();
      stdin.on('data', onData);
      if (stdout.isTTY) {
        stdout.on('resize', onResize);
      }
      process.on('SIGWINCH', onResize);
      process.on('SIGINT', onSignal);
      process.on('SIGTERM', onSignal);
      process.on('SIGHUP', onSignal);
      ws.send(terminalResizePayload());
    });

    ws.on('message', (data) => {
      writeTerminalOutput(data);
    });

    ws.on('close', () => {
      finalize();
      resolve();
    });

    ws.on('error', (err) => {
      finalize();
      reject(err instanceof Error ? err : new Error('terminal connection failed'));
    });
  });
}

/**
 * Opens a terminal session via SSH by minting a short-lived cert.
 */
async function openTerminalSSH(name: string, namespace: string | undefined, printOnly: boolean) {
  const keypair = await generateSSHKeypair();
  let keepTemp = false;
  try {
    const data = await request(`/spritzes/${encodeURIComponent(name)}/ssh${namespace ? `?namespace=${encodeURIComponent(namespace)}` : ''}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ public_key: keypair.publicKey }),
    });
    if (!data?.host || !data?.user || !data?.cert) {
      throw new Error('ssh credentials not available');
    }
    const certPath = `${keypair.privateKeyPath}-cert.pub`;
    await writeFile(certPath, data.cert, { mode: 0o600 });
    const args = [
      '-i',
      keypair.privateKeyPath,
      '-o',
      `CertificateFile=${certPath}`,
    ];
    if (data.known_hosts) {
      const knownHostsPath = path.join(keypair.dir, 'known_hosts');
      await writeFile(knownHostsPath, data.known_hosts, { mode: 0o600 });
      args.push('-o', `UserKnownHostsFile=${knownHostsPath}`, '-o', 'StrictHostKeyChecking=yes');
    } else {
      args.push('-o', 'StrictHostKeyChecking=accept-new');
    }
    const port = data.port || 22;
    args.push('-p', String(port), `${data.user}@${data.host}`);
    const commandLine = `${sshBinary} ${args.join(' ')}`;
    if (printOnly) {
      console.log(commandLine);
      keepTemp = true;
      return;
    }
    await runCommand(sshBinary, args, { stdio: 'inherit' });
    return;
  } finally {
    if (!keepTemp) {
      await rm(keypair.dir, { recursive: true, force: true });
    }
  }
}

/**
 * Resolve namespace from CLI flags or active profile.
 */
async function resolveNamespace(): Promise<string | undefined> {
  const nsFlag = argValue('--namespace');
  if (nsFlag) return nsFlag;
  const { profile } = await resolveProfile({ allowFlag: true });
  return profile?.namespace;
}

async function main() {
  const guidance = guidanceForAudience();
  if (shouldMaybeHandleSkillflag(process.argv)) {
    const { findSkillsRoot, maybeHandleSkillflag } = await loadSkillflagModule();
    await maybeHandleSkillflag(process.argv, {
      skillsRoot: findSkillsRoot(import.meta.url),
      includeBundledSkill: false,
    });
    return;
  }

  if (!command || command === 'help' || command === '--help') {
    if (command === 'help' && rest[0] === 'create') {
      createUsage(guidance);
      return;
    }
    if (command === 'help' && rest[0] === 'chat' && (!rest[1] || rest[1] === 'send')) {
      chatSendUsage();
      return;
    }
    usage(guidance);
    return;
  }

  if (command === 'create' && hasFlag('--help')) {
    createUsage(guidance);
    return;
  }

  if (command === 'chat' && hasFlag('--help')) {
    chatSendUsage();
    return;
  }

  if (command === 'profile') {
    const action = rest[0];
    const config = await loadConfig();

    if (!action || action === 'list') {
      const names = Object.keys(config.profiles).sort();
      if (names.length === 0) {
        console.log('no profiles configured');
        return;
      }
      names.forEach((name) => {
        const marker = name === config.currentProfile ? '*' : ' ';
        console.log(`${marker} ${name}`);
      });
      return;
    }

    if (action === 'current') {
      if (!config.currentProfile) {
        console.log('no active profile');
        return;
      }
      console.log(config.currentProfile);
      return;
    }

    if (action === 'show') {
      const name = rest[1] || config.currentProfile;
      if (!name) {
        throw new Error('profile name is required');
      }
      const profile = config.profiles[name];
      if (!profile) {
        throw new Error(`profile not found: ${name}`);
      }
      console.log(`Profile: ${name}`);
      console.log(`API URL: ${profile.apiUrl || '(unset)'}`);
      console.log(`Bearer Token: ${profile.bearerToken ? '(set)' : '(unset)'}`);
      console.log(`User ID: ${profile.userId || '(unset)'}`);
      console.log(`User Email: ${profile.userEmail || '(unset)'}`);
      console.log(`User Teams: ${profile.userTeams || '(unset)'}`);
      console.log(`Namespace: ${profile.namespace || '(unset)'}`);
      return;
    }

    if (action === 'set') {
      const name = normalizeProfileName(rest[1] || '');
      const apiUrlInfo = argValueInfo('--api-url');
      const tokenInfo = argValueInfo('--token');
      const userIdInfo = argValueInfo('--user-id');
      const userEmailInfo = argValueInfo('--user-email');
      const userTeamsInfo = argValueInfo('--user-teams');
      const namespaceInfo = argValueInfo('--namespace');

      const anyFlag =
        apiUrlInfo.present ||
        tokenInfo.present ||
        userIdInfo.present ||
        userEmailInfo.present ||
        userTeamsInfo.present ||
        namespaceInfo.present;

      if (!anyFlag) {
        throw new Error('at least one flag is required (e.g., --api-url, --user-id)');
      }

      const profile: ProfileConfig = { ...(config.profiles[name] || {}) };

      if (apiUrlInfo.present) {
        if (!apiUrlInfo.value) throw new Error('--api-url requires a value');
        profile.apiUrl = normalizeProfileValue(apiUrlInfo.value);
      }
      if (tokenInfo.present) {
        if (!tokenInfo.value) throw new Error('--token requires a value');
        profile.bearerToken = normalizeProfileValue(tokenInfo.value);
      }
      if (userIdInfo.present) {
        if (!userIdInfo.value) throw new Error('--user-id requires a value');
        profile.userId = normalizeProfileValue(userIdInfo.value);
      }
      if (userEmailInfo.present) {
        if (!userEmailInfo.value) throw new Error('--user-email requires a value');
        profile.userEmail = normalizeProfileValue(userEmailInfo.value);
      }
      if (userTeamsInfo.present) {
        if (!userTeamsInfo.value) throw new Error('--user-teams requires a value');
        profile.userTeams = normalizeProfileValue(userTeamsInfo.value);
      }
      if (namespaceInfo.present) {
        if (!namespaceInfo.value) throw new Error('--namespace requires a value');
        profile.namespace = normalizeProfileValue(namespaceInfo.value);
      }

      config.profiles[name] = profile;
      await saveConfig(config);
      console.log(`saved profile ${name}`);
      return;
    }

    if (action === 'use') {
      const name = normalizeProfileName(rest[1] || '');
      if (!config.profiles[name]) {
        throw new Error(`profile not found: ${name}`);
      }
      config.currentProfile = name;
      await saveConfig(config);
      console.log(`active profile set to ${name}`);
      return;
    }

    if (action === 'delete') {
      const name = normalizeProfileName(rest[1] || '');
      if (!config.profiles[name]) {
        throw new Error(`profile not found: ${name}`);
      }
      if (config.currentProfile === name) {
        throw new Error('cannot delete active profile');
      }
      delete config.profiles[name];
      await saveConfig(config);
      console.log(`deleted profile ${name}`);
      return;
    }

    throw new Error(`unknown profile command: ${action}`);
  }

  if (command === 'list') {
    const ns = await resolveNamespace();
    const data = await request(`/spritzes${ns ? `?namespace=${encodeURIComponent(ns)}` : ''}`);
    console.log(JSON.stringify(data, null, 2));
    return;
  }

  if (command === 'suggest-name') {
    const ns = await resolveNamespace();
    const data = await request('/spritzes/suggest-name', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        namespace: ns,
        presetId: argValue('--preset'),
        image: argValue('--image'),
        namePrefix: argValue('--name-prefix'),
      }),
    });
    console.log(JSON.stringify(data, null, 2));
    return;
  }

  if (command === 'create') {
    const name = positionalArgs()[0];
    const presetId = argValue('--preset');
    const presetInputs = parsePresetInputs(argValues('--preset-input'));
    const image = argValue('--image');

    const repo = argValue('--repo');
    const branch = argValue('--branch');
    const token = argValue('--token') || process.env.SPRITZ_BEARER_TOKEN;
    const ownerProvider = argValue('--owner-provider')?.trim().toLowerCase();
    const ownerTenant = argValue('--owner-tenant')?.trim();
    const ownerSubject = argValue('--owner-subject')?.trim();
    const explicitOwnerId = argValue('--owner-id')?.trim();
    const usingExternalOwner = Boolean(ownerProvider || ownerTenant || ownerSubject);
    if (explicitOwnerId && usingExternalOwner) {
      throw new Error('--owner-id and external owner flags are mutually exclusive');
    }
    if (usingExternalOwner) {
      if (!ownerProvider) {
        throw new Error('--owner-provider is required when using external owner flags');
      }
      if (!ownerSubject) {
        throw new Error('--owner-subject is required when using external owner flags');
      }
      if (ownerProvider === 'msteams' && !ownerTenant) {
        throw new Error('--owner-tenant is required for msteams');
      }
    }
    const ownerId = usingExternalOwner
      ? undefined
      : explicitOwnerId || (token?.trim() ? process.env.SPRITZ_OWNER_ID : await resolveDefaultOwnerId());
    if (!usingExternalOwner && !ownerId) {
      throw new Error(missingOwnerInputMessage(guidance));
    }
    const idleTtl = argValue('--idle-ttl');
    const ttl = argValue('--ttl');
    const idempotencyKey = argValue('--idempotency-key');
    const source = argValue('--source');
    const requestId = argValue('--request-id');
    const namePrefix = argValue('--name-prefix');
    const ns = await resolveNamespace();
    const body: any = {
      namespace: ns,
      spec: {},
    };
    if (name) body.name = name;
    if (namePrefix) body.namePrefix = namePrefix;
    if (presetId) body.presetId = presetId;
    if (presetInputs) body.presetInputs = presetInputs;
    if (ownerId) body.ownerId = ownerId;
    if (usingExternalOwner) {
      body.ownerRef = {
        type: 'external',
        provider: ownerProvider,
        subject: ownerSubject,
      };
      if (ownerTenant) body.ownerRef.tenant = ownerTenant;
    }
    if (idleTtl) body.idleTtl = idleTtl;
    if (ttl) body.ttl = ttl;
    if (idempotencyKey) body.idempotencyKey = idempotencyKey;
    if (source) body.source = source;
    if (requestId) body.requestId = requestId;
    if (image) body.spec.image = image;

    if (repo) {
      body.spec.repo = { url: repo };
      if (branch) body.spec.repo.branch = branch;
    }

    let data: any;
    try {
      data = await request('/spritzes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    } catch (error) {
      if (error instanceof SpritzRequestError && error.code === 'external_identity_unresolved') {
        throw new Error(unresolvedExternalOwnerMessage(error, guidance));
      }
      throw error;
    }

    console.log(JSON.stringify(data, null, 2));
    return;
  }

  if (command === 'delete') {
    const name = rest[0];
    if (!name) throw new Error('name is required');
    const ns = await resolveNamespace();
    await request(`/spritzes/${encodeURIComponent(name)}${ns ? `?namespace=${encodeURIComponent(ns)}` : ''}`,
      { method: 'DELETE' },
    );
    console.log('deleted');
    return;
  }

  if (command === 'open') {
    const name = rest[0];
    if (!name) throw new Error('name is required');
    const ns = await resolveNamespace();
    const data = await request(`/spritzes/${encodeURIComponent(name)}${ns ? `?namespace=${encodeURIComponent(ns)}` : ''}`);
    console.log(data?.status?.url || 'no url available');
    return;
  }

  if (command === 'chat') {
    const action = rest[0];
    if (!action || action === 'help' || action === '--help') {
      chatSendUsage();
      return;
    }
    if (action !== 'send') {
      throw new Error(`unknown chat command: ${action}`);
    }
    const instanceName = argValue('--instance')?.trim();
    const conversationId = argValue('--conversation')?.trim();
    if (Boolean(instanceName) === Boolean(conversationId)) {
      throw new Error('exactly one of --instance or --conversation is required');
    }
    if (conversationId && (argValue('--cwd') || argValue('--title'))) {
      throw new Error('--cwd and --title are only supported with --instance');
    }
    const message = argValue('--message')?.trim();
    if (!message) {
      throw new Error('--message is required');
    }
    const ownerId = argValue('--owner-id')?.trim() || (await resolveDefaultOwnerId());
    if (!ownerId) {
      throw new Error('owner id is required; use --owner-id or set SPRITZ_OWNER_ID / SPRITZ_USER_ID');
    }
    const ns = await resolveNamespace();
    const reason = argValue('--reason')?.trim() || 'spz chat send';
    const data = await internalRequest('/internal/v1/debug/chat/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        principal: { id: ownerId },
        target: {
          namespace: ns,
          spritzName: instanceName,
          conversationId,
          cwd: argValue('--cwd'),
          title: argValue('--title'),
        },
        reason,
        message,
      }),
    });
    if (hasFlag('--json') || !data?.assistantText) {
      console.log(JSON.stringify(data, null, 2));
      return;
    }
    console.log(data.assistantText);
    return;
  }

  if (command === 'ssh' || command === 'terminal') {
    const name = rest[0];
    if (!name) throw new Error('name is required');
    const ns = await resolveNamespace();
    const printOnly = hasFlag('--print');
    const sessionFlagInfo = argValueInfo('--session');
    const transport = resolveTransport();
    if (transport === 'ssh') {
      if (sessionFlagInfo.present) {
        console.error('--session is ignored for SSH transport.');
      }
      await openTerminalSSH(name, ns, printOnly);
      return;
    }
    if (command === 'ssh' && !printOnly) {
      console.error('Using websocket terminal. Pass --transport ssh to use legacy SSH.');
    }
    const sessionInfo = await resolveTerminalSession(name, ns, sessionFlagInfo, printOnly);
    if (sessionInfo.useZmx && !printOnly) {
      console.error('ZMX session active. Detach with Ctrl+\\ and reconnect later.');
    }
    await openTerminalWs(name, ns, printOnly, sessionInfo.session);
    return;
  }

  usage();
}

main().catch((err) => {
  console.error(err.message || err);
  process.exit(1);
});
