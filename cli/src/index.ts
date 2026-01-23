#!/usr/bin/env node

import { spawn, spawnSync } from 'node:child_process';
import { closeSync, openSync, writeSync } from 'node:fs';
import { chmod, mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline/promises';
import WebSocket, { RawData } from 'ws';

type ProfileConfig = {
  apiUrl?: string;
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

const defaultApiBase = 'http://localhost:8080';
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

const [, , command, ...rest] = process.argv;

const terminalResetSequence = [
  '\x1b[?2004l', // bracketed paste off
  '\x1b[?2026l', // kitty keyboard protocol off
  '\x1b[?1000l', // mouse tracking off
  '\x1b[?1002l',
  '\x1b[?1003l',
  '\x1b[?1006l',
  '\x1b[?25h', // show cursor
].join('');
let terminalRestored = false;

function withTtyFd(mode: 'r' | 'w', fn: (fd: number) => void) {
  try {
    const fd = openSync('/dev/tty', mode);
    try {
      fn(fd);
    } finally {
      closeSync(fd);
    }
  } catch {
    // ignore
  }
}

function writeToTty(payload: string) {
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
  });
}

function restoreLocalTerminal() {
  if (terminalRestored) return;
  terminalRestored = true;
  writeToTty(terminalResetSequence);
  if (process.platform !== 'win32') {
    try {
      if (process.stdin.isTTY) {
        spawnSync('stty', ['sane'], { stdio: [0, 'ignore', 'ignore'] });
      } else {
        withTtyFd('r', (fd) => {
          spawnSync('stty', ['sane'], { stdio: [fd, 'ignore', 'ignore'] });
        });
      }
    } catch {
      // ignore
    }
  }
}

function usage() {
  console.log(`Spritz CLI

Usage:
  spritz list [--namespace <ns>]
  spritz create <name> --image <image> [--repo <url>] [--branch <branch>] [--ttl <duration>] [--namespace <ns>]
  spritz delete <name> [--namespace <ns>]
  spritz open <name> [--namespace <ns>]
  spritz terminal <name> [--namespace <ns>] [--session <name>] [--transport <ws|ssh>] [--print]
  spritz ssh <name> [--namespace <ns>] [--session <name>] [--transport <ws|ssh>] [--print]
  spritz profile list
  spritz profile current
  spritz profile show [name]
  spritz profile set <name> [--api-url <url>] [--user-id <id>] [--user-email <email>] [--user-teams <csv>] [--namespace <ns>]
  spritz profile use <name>
  spritz profile delete <name>

Alias:
  spz (same commands as spritz)

Environment:
  SPRITZ_API_URL (default: ${process.env.SPRITZ_API_URL || defaultApiBase})
  SPRITZ_USER_ID, SPRITZ_USER_EMAIL, SPRITZ_USER_TEAMS, SPRITZ_OWNER_ID
  SPRITZ_API_HEADER_ID, SPRITZ_API_HEADER_EMAIL, SPRITZ_API_HEADER_TEAMS
  SPRITZ_TERMINAL_TRANSPORT (default: ${terminalTransportDefault})
  SPRITZ_PROFILE, SPRITZ_CONFIG_DIR

Notes:
  When ZMX sessions are enabled, detach with Ctrl+\\ and reconnect later.
`);
}

function argValue(flag: string): string | undefined {
  const idx = rest.indexOf(flag);
  if (idx === -1) return undefined;
  return rest[idx + 1];
}

function argValueInfo(flag: string): { present: boolean; value?: string } {
  const idx = rest.indexOf(flag);
  if (idx === -1) return { present: false };
  return { present: true, value: rest[idx + 1] };
}

function hasFlag(flag: string): boolean {
  return rest.includes(flag);
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
  const headers: Record<string, string> = {};
  const userId = process.env.SPRITZ_USER_ID || profile?.userId || process.env.USER;
  const userEmail = process.env.SPRITZ_USER_EMAIL || profile?.userEmail;
  const userTeams = process.env.SPRITZ_USER_TEAMS || profile?.userTeams;
  if (userId) headers[headerId] = userId;
  if (userEmail) headers[headerEmail] = userEmail;
  if (userTeams) headers[headerTeams] = userTeams;
  return headers;
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
    const message =
      (jsend && (jsend.message || jsend.data?.message || jsend.data?.error)) ||
      text ||
      res.statusText ||
      'Request failed';
    throw new Error(message);
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
    restoreLocalTerminal();
  };
  const onSignal = () => {
    restoreLocalTerminal();
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
    restoreLocalTerminal();
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
  if (!command || command === 'help' || command === '--help') {
    usage();
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
      console.log(`User ID: ${profile.userId || '(unset)'}`);
      console.log(`User Email: ${profile.userEmail || '(unset)'}`);
      console.log(`User Teams: ${profile.userTeams || '(unset)'}`);
      console.log(`Namespace: ${profile.namespace || '(unset)'}`);
      return;
    }

    if (action === 'set') {
      const name = normalizeProfileName(rest[1] || '');
      const apiUrlInfo = argValueInfo('--api-url');
      const userIdInfo = argValueInfo('--user-id');
      const userEmailInfo = argValueInfo('--user-email');
      const userTeamsInfo = argValueInfo('--user-teams');
      const namespaceInfo = argValueInfo('--namespace');

      const anyFlag =
        apiUrlInfo.present ||
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

  if (command === 'create') {
    const name = rest[0];
    if (!name) throw new Error('name is required');
    const image = argValue('--image');
    if (!image) throw new Error('--image is required');

    const repo = argValue('--repo');
    const branch = argValue('--branch');
    const ttl = argValue('--ttl');
    const ns = await resolveNamespace();
    const { profile } = await resolveProfile({ allowFlag: true });
    const ownerId =
      process.env.SPRITZ_OWNER_ID ||
      process.env.SPRITZ_USER_ID ||
      profile?.userId ||
      process.env.USER;
    if (!ownerId) {
      throw new Error('SPRITZ_OWNER_ID, SPRITZ_USER_ID, or USER environment variable must be set');
    }

    const body: any = {
      name,
      namespace: ns,
      spec: {
        image,
        owner: { id: ownerId },
      },
    };

    if (repo) {
      body.spec.repo = { url: repo };
      if (branch) body.spec.repo.branch = branch;
    }
    if (ttl) body.spec.ttl = ttl;

    const data = await request('/spritzes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });

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
