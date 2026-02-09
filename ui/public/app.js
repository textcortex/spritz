const config = window.SPRITZ_CONFIG || { apiBaseUrl: '' };
const apiBaseUrl = config.apiBaseUrl || '';
const basePath = (config.basePath || '').replace(/\/$/, '');
const authConfig = config.auth || {};
const authMode = (authConfig.mode || '').toLowerCase();
const authTokenStorage = (authConfig.tokenStorage || 'localStorage').toLowerCase();
const authTokenStorageKeys = parseStorageKeys(authConfig.tokenStorageKeys);
const authBearerTokenParam = (authConfig.bearerTokenParam || 'token').trim() || 'token';
const authLoginUrl = (authConfig.loginUrl || '').trim();
const authReturnToMode = (authConfig.returnToMode || 'auto').toLowerCase();
const authReturnToParam = (authConfig.returnToParam || '').trim();
const authRedirectOnUnauthorized = parseBoolean(authConfig.redirectOnUnauthorized, true);
const authRefreshConfig = authConfig.refresh || {};
const authRefreshEnabled = parseBoolean(authRefreshConfig.enabled, false);
const authRefreshUrl = String(authRefreshConfig.url || '').trim();
const authRefreshMethod = String(authRefreshConfig.method || 'POST').toUpperCase();
const authRefreshCredentials = String(authRefreshConfig.credentials || 'include');
const authRefreshTimeoutMs = parseNumber(authRefreshConfig.timeoutMs, 5000);
const authRefreshCooldownMs = parseNumber(authRefreshConfig.cooldownMs, 30000);
const authRefreshHeadersRaw = String(authRefreshConfig.headers || '').trim();
const authRefreshTokenStorageKeys = parseStorageKeys(authRefreshConfig.tokenStorageKeys);
const repoDefaults = config.repoDefaults || {};
const defaultRepoUrl = String(repoDefaults.url || '').trim();
const defaultRepoDir = String(repoDefaults.dir || '').trim();
const defaultRepoBranch = String(repoDefaults.branch || '').trim();
const hideRepoInputs = parseBoolean(repoDefaults.hideInputs, false);
const authReturnToPlaceholder = '__SPRITZ_RETURN_TO__';
const noticeEl = document.getElementById('notice');
const listEl = document.getElementById('list');
const refreshBtn = document.getElementById('refresh');
const form = document.getElementById('create-form');
const shellEl = document.querySelector('.shell');
const createSection = form?.closest('section');
const listSection = listEl?.closest('section');
let activeTerminalSession = null;
let activeTerminalName = '';
let presetsInitialized = false;
let activePresetEnv = null;
const authRedirectStorageKey = 'spritz-auth-redirected';
let authRefreshInFlight = null;
let authRefreshAttemptId = 0;
let lastAuthRefreshAt = 0;
let activeTerminalPoll = null;
const presetPlaceholder = '__SPRITZ_UI_PRESETS__';
const defaultPresets = [
  {
    name: 'Starter Devbox',
    image: 'spritz-starter:latest',
    description: 'Minimal starter image built from images/examples/base.',
    repoUrl: '',
    branch: '',
    ttl: '',
  },
];

function parseBoolean(value, fallback) {
  if (value === undefined || value === null || value === '') return fallback;
  if (typeof value === 'boolean') return value;
  const normalized = String(value).trim().toLowerCase();
  if (['true', '1', 'yes', 'y', 'on'].includes(normalized)) return true;
  if (['false', '0', 'no', 'n', 'off'].includes(normalized)) return false;
  return fallback;
}

function parseNumber(value, fallback) {
  if (value === undefined || value === null || value === '') return fallback;
  const parsed = Number(value);
  return Number.isNaN(parsed) ? fallback : parsed;
}

function parseYamlScalar(value) {
  if (value === undefined || value === null) return '';
  const trimmed = String(value).trim();
  if (!trimmed) return '';
  if ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) {
    return trimmed.slice(1, -1);
  }
  const lowered = trimmed.toLowerCase();
  if (lowered === 'true') return true;
  if (lowered === 'false') return false;
  if (lowered === 'null') return null;
  const numeric = Number(trimmed);
  if (!Number.isNaN(numeric) && trimmed !== '') return numeric;
  return trimmed;
}

function parseYamlKeyValue(line) {
  const idx = line.indexOf(':');
  if (idx === -1) return null;
  const key = line.slice(0, idx).trim();
  if (!key) return null;
  const value = line.slice(idx + 1).trim();
  return { key, value: parseYamlScalar(value) };
}

function normalizeSharedMountsPayload(payload) {
  if (Array.isArray(payload)) return payload;
  if (payload && typeof payload === 'object' && Array.isArray(payload.sharedMounts)) {
    return payload.sharedMounts;
  }
  throw new Error('Shared mounts must be a YAML list or JSON array.');
}

function parseSharedMountsYaml(raw) {
  const lines = raw
    .split(/\r?\n/)
    .map((line) => {
      const hashIndex = line.indexOf('#');
      const sanitized = hashIndex >= 0 ? line.slice(0, hashIndex) : line;
      return sanitized.replace(/\t/g, '  ').trimEnd();
    })
    .filter((line) => line.trim() !== '');

  if (lines.length === 0) return null;
  let index = 0;
  if (/^sharedMounts\s*:\s*$/.test(lines[0].trim())) {
    index = 1;
  }
  const mounts = [];
  let current = null;
  for (; index < lines.length; index += 1) {
    const line = lines[index];
    if (/^-\s*/.test(line.trim())) {
      if (current) mounts.push(current);
      current = {};
      const rest = line.trim().replace(/^-\s*/, '').trim();
      if (rest) {
        const kv = parseYamlKeyValue(rest);
        if (!kv) {
          throw new Error(`Invalid shared mounts YAML entry: ${rest}`);
        }
        current[kv.key] = kv.value;
      }
      continue;
    }
    if (!current) {
      throw new Error('Shared mounts YAML must be a list (start each item with "-").');
    }
    const kv = parseYamlKeyValue(line.trim());
    if (!kv) {
      throw new Error(`Invalid shared mounts YAML line: ${line.trim()}`);
    }
    current[kv.key] = kv.value;
  }
  if (current) mounts.push(current);
  if (mounts.length === 0) {
    throw new Error('Shared mounts YAML must contain at least one item.');
  }
  return mounts;
}

function parseSharedMountsInput(value) {
  const raw = String(value || '').trim();
  if (!raw) return null;
  if (raw.startsWith('{') || raw.startsWith('[')) {
    const parsed = JSON.parse(raw);
    return normalizeSharedMountsPayload(parsed);
  }
  return parseSharedMountsYaml(raw);
}

function parseStorageKeys(value) {
  if (!value) return [];
  if (Array.isArray(value)) {
    return value.map((item) => String(item).trim()).filter(Boolean);
  }
  return String(value)
    .split(/[;,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function parsePresets(raw) {
  if (Array.isArray(raw)) return raw;
  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (!trimmed || trimmed === presetPlaceholder) return null;
    try {
      const parsed = JSON.parse(trimmed);
      if (Array.isArray(parsed)) return parsed;
    } catch {
      return null;
    }
  }
  return null;
}

const presets = parsePresets(config.presets) ?? defaultPresets;
const defaultSharedMountsYaml = `- name: config
  mountPath: /home/dev/.config
  scope: owner
  mode: snapshot
  syncMode: manual`;

function normalizePresetEnv(env) {
  if (!env) return null;
  if (Array.isArray(env)) {
    return env
      .map((item) => {
        if (!item || typeof item !== 'object') return null;
        const name = String(item.name || '').trim();
        if (!name) return null;
        const value = item.value === undefined ? '' : String(item.value);
        return { name, value };
      })
      .filter(Boolean);
  }
  if (typeof env === 'object') {
    return Object.entries(env)
      .map(([name, value]) => ({
        name,
        value: value === undefined ? '' : String(value),
      }))
      .filter((item) => item.name.trim() !== '');
  }
  return null;
}

function applySharedMountsDefaults() {
  if (!form) return;
  const textarea = form.querySelector('textarea[name="shared_mounts"]');
  if (!textarea) return;
  if (!textarea.value.trim()) {
    textarea.value = defaultSharedMountsYaml;
  }
}

function applyRepoDefaults() {
  if (!form) return;
  const repoInput = form.querySelector('input[name="repo"]');
  const branchInput = form.querySelector('input[name="branch"]');
  if (!repoInput || !branchInput) return;

  if (hideRepoInputs) {
    const repoLabel = repoInput.closest('label');
    const branchLabel = branchInput.closest('label');
    if (repoLabel) repoLabel.hidden = true;
    if (branchLabel) branchLabel.hidden = true;
    repoInput.disabled = true;
    branchInput.disabled = true;
  }

  if (defaultRepoUrl && !repoInput.value) {
    repoInput.value = defaultRepoUrl;
  }
  if (defaultRepoBranch && !branchInput.value) {
    branchInput.value = defaultRepoBranch;
  }
}

function isJSend(payload) {
  return payload && typeof payload === 'object' && typeof payload.status === 'string';
}

function showNotice(message, kind = 'error') {
  if (!noticeEl) return;
  if (!message) {
    noticeEl.hidden = true;
    noticeEl.textContent = '';
    noticeEl.dataset.kind = '';
    return;
  }
  noticeEl.hidden = false;
  noticeEl.textContent = message;
  noticeEl.dataset.kind = kind;
}

function getStorage() {
  if (authTokenStorage === 'sessionstorage' && window.sessionStorage) {
    return window.sessionStorage;
  }
  return window.localStorage || null;
}

function readTokenFromStorage(keys) {
  if (!keys.length) return '';
  const storage = getStorage();
  if (!storage) return '';
  for (const key of keys) {
    const raw = storage.getItem(key);
    if (!raw) continue;
    if (raw.startsWith('"') && raw.endsWith('"')) {
      return raw.slice(1, -1);
    }
    return raw;
  }
  return '';
}

function writeTokenToStorage(keys, token) {
  if (!keys.length) return false;
  const storage = getStorage();
  if (!storage) return false;
  let updated = false;
  for (const key of keys) {
    try {
      storage.setItem(key, token);
      updated = true;
    } catch {
      // ignore storage errors
    }
  }
  return updated;
}

function authModeAllowsBearer() {
  if (!authMode) return false;
  if (authMode === 'bearer' || authMode === 'auto') return true;
  const parts = authMode.split(/[,+]/).map((part) => part.trim());
  return parts.includes('bearer');
}

function getAuthToken() {
  if (!authModeAllowsBearer()) return '';
  return readTokenFromStorage(authTokenStorageKeys);
}

function getAuthRefreshToken() {
  return readTokenFromStorage(authRefreshTokenStorageKeys);
}

function clearAuthRedirectFlag() {
  try {
    if (window.sessionStorage) {
      window.sessionStorage.removeItem(authRedirectStorageKey);
    }
  } catch {
    // ignore storage errors
  }
}

function shouldRedirectOnUnauthorized() {
  return Boolean(authLoginUrl) && authRedirectOnUnauthorized;
}

function buildReturnToUrl() {
  const path = window.location.pathname || basePath || '/';
  const search = window.location.search || '';
  const hash = window.location.hash || '';
  const returnPath = `${path}${search}${hash}`;
  const mode = authReturnToMode === 'auto'
    ? (isLoginUrlAbsolute(authLoginUrl) ? 'absolute' : 'path')
    : authReturnToMode;
  if (mode === 'absolute') {
    return `${window.location.origin}${returnPath}`;
  }
  return returnPath;
}

function isLoginUrlAbsolute(value) {
  if (!value) return false;
  return /^https?:\/\//i.test(value) || value.startsWith('//');
}

function resolveLoginUrl() {
  if (!authLoginUrl) return '';
  const returnTo = buildReturnToUrl();
  if (authLoginUrl.includes(authReturnToPlaceholder)) {
    return authLoginUrl.split(authReturnToPlaceholder).join(encodeURIComponent(returnTo));
  }
  try {
    const url = new URL(authLoginUrl, window.location.href);
    if (authReturnToParam) {
      if (!url.searchParams.has(authReturnToParam)) {
        url.searchParams.set(authReturnToParam, returnTo);
      }
    } else if (!url.searchParams.has('next') && !url.searchParams.has('returnTo')) {
      url.searchParams.set('next', returnTo);
    }
    return url.href;
  } catch {
    return authLoginUrl;
  }
}

function redirectToLogin(message) {
  if (!shouldRedirectOnUnauthorized()) return false;
  const loginHref = resolveLoginUrl();
  if (!loginHref) return false;
  if (loginHref === window.location.href) return false;
  try {
    if (window.sessionStorage?.getItem(authRedirectStorageKey) === '1') {
      return false;
    }
    window.sessionStorage?.setItem(authRedirectStorageKey, '1');
  } catch {
    // ignore storage errors
  }
  showNotice(message || 'Redirecting to sign in...', 'info');
  window.location.assign(loginHref);
  return true;
}

function hasAuthorizationHeader(headers) {
  if (!headers || typeof headers !== 'object') return false;
  return Object.keys(headers).some((key) => key.toLowerCase() === 'authorization');
}

function buildRefreshHeaders() {
  let headers = {};
  if (authRefreshHeadersRaw) {
    try {
      const parsed = JSON.parse(authRefreshHeadersRaw);
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        headers = { ...parsed };
      }
    } catch {
      // ignore invalid JSON
    }
  }
  const refreshToken = getAuthRefreshToken();
  if (refreshToken && !hasAuthorizationHeader(headers)) {
    headers = { ...headers, Authorization: `Bearer ${refreshToken}` };
  }
  return headers;
}

function normalizeRefreshCredentials(value) {
  if (value === 'omit' || value === 'same-origin' || value === 'include') {
    return value;
  }
  return 'include';
}

function shouldAttemptAuthRefresh() {
  if (!authRefreshEnabled) return false;
  if (!authRefreshUrl) return false;
  if (authModeAllowsBearer() && authTokenStorageKeys.length > 0 && !getAuthToken()) {
    return false;
  }
  if (authRefreshTokenStorageKeys.length > 0 && !getAuthRefreshToken()) {
    return false;
  }
  if (authRefreshCooldownMs <= 0) return true;
  return Date.now() - lastAuthRefreshAt > authRefreshCooldownMs;
}

function readTokenField(payload, key) {
  if (!payload || typeof payload !== 'object') return '';
  const direct = payload[key];
  if (typeof direct === 'string' && direct) return direct;
  if (direct) return String(direct);
  const nested = payload.data && typeof payload.data === 'object' ? payload.data[key] : null;
  if (typeof nested === 'string' && nested) return nested;
  if (nested) return String(nested);
  return '';
}

function extractAuthTokens(payload) {
  const accessToken =
    readTokenField(payload, 'access_token') ||
    readTokenField(payload, 'accessToken') ||
    readTokenField(payload, 'token');
  const refreshToken =
    readTokenField(payload, 'refresh_token') ||
    readTokenField(payload, 'refreshToken');
  return { accessToken, refreshToken };
}

function updateAuthTokensFromPayload(payload) {
  const { accessToken, refreshToken } = extractAuthTokens(payload);
  const accessUpdated = accessToken
    ? writeTokenToStorage(authTokenStorageKeys, accessToken)
    : false;
  const refreshUpdated = refreshToken
    ? writeTokenToStorage(authRefreshTokenStorageKeys, refreshToken)
    : false;
  return { accessUpdated, refreshUpdated };
}

async function runAuthRefresh() {
  if (authRefreshInFlight) return authRefreshInFlight;
  if (!shouldAttemptAuthRefresh()) {
    return { ok: false, attemptId: authRefreshAttemptId };
  }
  authRefreshAttemptId += 1;
  const attemptId = authRefreshAttemptId;
  authRefreshInFlight = (async () => {
    lastAuthRefreshAt = Date.now();
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), authRefreshTimeoutMs);
    try {
      const headers = buildRefreshHeaders();
      const res = await fetch(authRefreshUrl, {
        method: authRefreshMethod,
        credentials: normalizeRefreshCredentials(authRefreshCredentials),
        headers,
        signal: controller.signal,
      });
      if (!res.ok) {
        return { ok: false, attemptId };
      }
      const { data } = await readResponse(res);
      const updates = updateAuthTokensFromPayload(data);
      const requiresAccessToken = authModeAllowsBearer() && authTokenStorageKeys.length > 0;
      const ok = !requiresAccessToken || updates.accessUpdated;
      return { ok, attemptId };
    } catch {
      return { ok: false, attemptId };
    } finally {
      clearTimeout(timeout);
      authRefreshInFlight = null;
    }
  })();
  return authRefreshInFlight;
}

async function readResponse(res) {
  const text = await res.text();
  if (!text) {
    return { text: '', data: null };
  }
  try {
    return { text, data: JSON.parse(text) };
  } catch {
    return { text, data: null };
  }
}

async function request(path, options = {}) {
  const headers = new Headers(options.headers || {});
  const token = getAuthToken();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  const res = await fetch(`${apiBaseUrl}${path}`, {
    credentials: 'include',
    ...options,
    headers,
  });
  const { text, data } = await readResponse(res);
  const jsend = isJSend(data) ? data : null;
  if (res.ok && jsend && jsend.status !== 'success') {
    const message =
      jsend.message ||
      jsend.data?.message ||
      jsend.data?.error ||
      text ||
      res.statusText ||
      'Request failed';
    const err = new Error(message);
    err.status = res.status;
    err.payload = jsend;
    throw err;
  }
  if (!res.ok) {
    const message =
      (jsend && (jsend.message || jsend.data?.message || jsend.data?.error)) ||
      data?.error ||
      data?.message ||
      text ||
      res.statusText ||
      'Request failed';
    if ((res.status === 401 || res.status === 403) && options.__authRefreshAttemptId !== authRefreshAttemptId) {
      const refreshResult = await runAuthRefresh();
      if (refreshResult.ok) {
        return request(path, { ...options, __authRefreshAttemptId: refreshResult.attemptId });
      }
    }
    if ((res.status === 401 || res.status === 403) && redirectToLogin(message)) {
      throw new Error(message);
    }
    const err = new Error(message);
    err.status = res.status;
    err.payload = jsend || data;
    throw err;
  }
  clearAuthRedirectFlag();
  if (res.status === 204) return null;
  if (jsend) return jsend.data ?? null;
  if (data !== null) return data;
  return text ? text : null;
}

async function fetchSpritzes() {
  try {
    const data = await request('/spritzes');
    renderList(data.items || []);
    showNotice('');
  } catch (err) {
    listEl.innerHTML = `<p>Failed to load spritzes.</p>`;
    if (err.status === 401 || err.status === 403) {
      if (!shouldRedirectOnUnauthorized()) {
        showNotice(err.message || 'Sign in required.');
      }
      return;
    }
    showNotice(err.message || 'Failed to load spritzes.');
  }
}

function renderList(items) {
  if (!items.length) {
    listEl.innerHTML = '<p>No spritzes yet.</p>';
    return;
  }
  listEl.innerHTML = '';
  for (const spritz of items) {
    const item = document.createElement('div');
    item.className = 'spritz-item';
    const info = document.createElement('div');
    const spritzName = spritz.metadata?.name;
    const spritzNamespace = spritz.metadata?.namespace;
    const nameEl = document.createElement('strong');
    nameEl.textContent = spritzName || 'unknown';
    const metaEl = document.createElement('small');
    const phase = spritz.status?.phase || 'unknown';
    const image = spritz.spec?.image || '';
    const message = spritz.status?.message || '';
    metaEl.textContent = message ? `${phase} · ${image} · ${message}` : `${phase} · ${image}`;
    info.append(nameEl, metaEl);

    const actions = document.createElement('div');
    actions.className = 'actions';

    const openBtn = document.createElement('button');
    openBtn.textContent = 'Open';
    openBtn.onclick = () => {
      const url = spritz.status?.url;
      if (url) window.open(url, '_blank');
    };

    const terminalBtn = document.createElement('button');
    terminalBtn.textContent = 'Terminal';
    const terminalReady = phase === 'Ready';
    terminalBtn.disabled = !terminalReady;
    if (!terminalReady) {
      terminalBtn.title = 'Terminal is available once provisioning completes.';
    }
    terminalBtn.onclick = () => {
      if (!terminalReady) return;
      const name = spritz.metadata?.name;
      if (!name) return;
      window.location.assign(terminalPagePath(name));
    };

    const sshMode = spritz.spec?.ssh?.mode;
    const sshInfo = spritz.status?.ssh;
    if (sshMode === 'gateway' && spritzName) {
      const sshBtn = document.createElement('button');
      sshBtn.textContent = 'SSH';
      sshBtn.onclick = async () => {
        const cmdParts = ['spz', 'ssh', spritzName];
        if (spritzNamespace) {
          cmdParts.push('--namespace', spritzNamespace);
        }
        const cmd = cmdParts.join(' ');
        try {
          if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(cmd);
            showNotice('spz SSH command copied to clipboard.', 'info');
          } else {
            window.prompt('spz SSH command', cmd);
          }
        } catch (err) {
          showNotice(err.message || 'Failed to copy SSH command.');
        }
      };
      actions.append(sshBtn);
    } else if (sshInfo?.host && sshInfo?.user) {
      const sshBtn = document.createElement('button');
      sshBtn.textContent = 'SSH';
      sshBtn.onclick = async () => {
        const port = sshInfo?.port || 22;
        const cmd = `ssh ${sshInfo.user}@${sshInfo.host} -p ${port}`;
        try {
          if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(cmd);
            showNotice('SSH command copied to clipboard.', 'info');
          } else {
            window.prompt('SSH command', cmd);
          }
        } catch (err) {
          showNotice(err.message || 'Failed to copy SSH command.');
        }
      };
      actions.append(sshBtn);
    }

    const deleteBtn = document.createElement('button');
    deleteBtn.textContent = 'Delete';
    deleteBtn.onclick = async () => {
      try {
        await request(`/spritzes/${spritz.metadata?.name}`, { method: 'DELETE' });
        await fetchSpritzes();
      } catch (err) {
        showNotice(err.message || 'Failed to delete spritz.');
      }
    };

    actions.append(openBtn, terminalBtn, deleteBtn);
    item.append(info, actions);
    listEl.appendChild(item);
  }
}

function terminalPagePath(name) {
  const prefix = basePath || '';
  return `${prefix}#terminal/${encodeURIComponent(name)}`;
}

function terminalNameFromPath() {
  const hash = window.location.hash || '';
  const prefix = '#terminal/';
  if (!hash.startsWith(prefix)) return '';
  const remainder = hash.slice(prefix.length);
  return decodeURIComponent(remainder.split('/')[0] || '');
}

function renderTerminalPage(name) {
  if (!shellEl) return;
  cleanupTerminal();
  activeTerminalName = name;
  if (createSection) createSection.hidden = true;
  if (listSection) listSection.hidden = true;

  const header = shellEl.querySelector('header');
  if (header) {
    const title = header.querySelector('h1');
    if (title) title.textContent = `Spritz · ${name}`;
    const subtitle = header.querySelector('p');
    if (subtitle) subtitle.textContent = 'Terminal session via gateway.';
  }

  const card = document.createElement('section');
  card.className = 'card terminal-card';

  const bar = document.createElement('div');
  bar.className = 'terminal-bar';

  const back = document.createElement('a');
  back.className = 'terminal-back';
  back.href = basePath || '/';
  back.textContent = 'Back to spritzes';

  const status = document.createElement('span');
  status.className = 'terminal-status';
  status.textContent = 'Waiting for spritz...';

  bar.append(back, status);

  const container = document.createElement('div');
  container.id = 'terminal';

  card.append(bar, container);
  shellEl.append(card);

  loadTerminalAssets()
    .then(() => waitForTerminalReady(name, container, status))
    .catch((err) => {
      status.textContent = 'Failed to load terminal.';
      showNotice(err.message || 'Failed to load terminal assets.');
    });
}

function waitForTerminalReady(name, container, statusEl) {
  if (activeTerminalPoll) {
    activeTerminalPoll.cancelled = true;
  }
  const poll = { cancelled: false };
  activeTerminalPoll = poll;

  const check = async () => {
    if (poll.cancelled) return;
    try {
      const data = await request(`/spritzes/${encodeURIComponent(name)}`);
      const spritz = data?.item || data;
      const phase = spritz?.status?.phase || 'unknown';
      const message = spritz?.status?.message || '';
      if (phase === 'Ready') {
        poll.cancelled = true;
        statusEl.textContent = 'Connecting...';
        startTerminalSession(name, container, statusEl);
        return;
      }
      statusEl.textContent = message ? `${phase} · ${message}` : `${phase}...`;
    } catch (err) {
      if (err && (err.status === 401 || err.status === 403 || err.status === 404)) {
        poll.cancelled = true;
        statusEl.textContent = err.message || 'Terminal unavailable.';
        showNotice(err.message || 'Terminal unavailable.');
        return;
      }
      statusEl.textContent = 'Provisioning...';
    }
    if (!poll.cancelled) {
      setTimeout(check, 3000);
    }
  };

  check();
}

function loadTerminalAssets() {
  if (window.Terminal && window.FitAddon) return Promise.resolve();
  const css = assetUrl('/vendor/xterm/xterm.css');
  const script = assetUrl('/vendor/xterm/xterm.js');
  const fitScript = assetUrl('/vendor/xterm/xterm-addon-fit.js');

  return Promise.all([
    loadStylesheet(css),
    loadScript(script),
    loadScript(fitScript),
  ]).then(() => undefined);
}

function assetUrl(path) {
  const normalized = path.startsWith('/') ? path : `/${path}`;
  return `${basePath}${normalized}`;
}

function loadStylesheet(href) {
  return new Promise((resolve, reject) => {
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = href;
    link.onload = () => resolve();
    link.onerror = () => reject(new Error(`Failed to load ${href}`));
    document.head.append(link);
  });
}

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = src;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`Failed to load ${src}`));
    document.body.append(script);
  });
}

function terminalWsUrl(name) {
  const base = apiBaseUrl || '';
  const resolved = base.startsWith('http')
    ? base
    : `${window.location.origin}${base}`;
  const wsBase = resolved.replace(/^http/, 'ws');
  const token = getAuthToken();
  const query = token ? `?${encodeURIComponent(authBearerTokenParam)}=${encodeURIComponent(token)}` : '';
  return `${wsBase}/spritzes/${encodeURIComponent(name)}/terminal${query}`;
}

function startTerminalSession(name, container, statusEl) {
  const term = new window.Terminal({
    cursorBlink: true,
    convertEol: true,
    fontFamily: 'SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
    theme: {
      background: '#0f172a',
    },
  });
  const fitAddon = new window.FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(container);
  fitAddon.fit();
  let ws = null;
  let closing = false;
  let reconnectAttempts = 0;
  let reconnectTimer = null;

  const onResize = () => {
    fitAddon.fit();
    if (ws) {
      sendResize(ws, term);
    }
  };

  const scheduleReconnect = () => {
    if (closing) return;
    reconnectAttempts += 1;
    const delay = Math.min(10000, 1000 * Math.pow(2, reconnectAttempts - 1));
    statusEl.textContent = `Reconnecting in ${Math.ceil(delay / 1000)}s...`;
    reconnectTimer = setTimeout(connectSocket, delay);
  };

  const connectSocket = () => {
    if (closing) return;
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    statusEl.textContent = reconnectAttempts ? 'Reconnecting...' : 'Connecting...';
    ws = new WebSocket(terminalWsUrl(name));
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      reconnectAttempts = 0;
      statusEl.textContent = 'Connected';
      sendResize(ws, term);
      term.focus();
    };

    ws.onmessage = (event) => {
      if (typeof event.data === 'string') {
        term.write(event.data);
        return;
      }
      term.write(new Uint8Array(event.data));
    };

    ws.onclose = () => {
      if (closing) return;
      scheduleReconnect();
    };

    ws.onerror = () => {
      if (!closing) {
        statusEl.textContent = 'Connection error';
      }
    };
  };

  connectSocket();

  term.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(data);
    }
  });

  window.addEventListener('resize', onResize);

  activeTerminalSession = {
    dispose() {
      window.removeEventListener('resize', onResize);
      closing = true;
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      try {
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
          ws.close();
        }
      } catch {
        // ignore
      }
      try {
        term.dispose();
      } catch {
        // ignore
      }
    },
  };
}

function sendResize(ws, term) {
  if (ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
}

function setupPresets() {
  if (presetsInitialized) return;
  if (!presets.length) return;
  const imageInput = form.querySelector('input[name="image"]');
  const repoInput = form.querySelector('input[name="repo"]');
  const branchInput = form.querySelector('input[name="branch"]');
  const ttlInput = form.querySelector('input[name="ttl"]');

  if (!imageInput) return;
  applyRepoDefaults();

  const panel = document.createElement('div');
  panel.className = 'preset-panel';

  const label = document.createElement('label');
  const select = document.createElement('select');
  select.id = 'preset-select';

  const customOption = document.createElement('option');
  customOption.value = '';
  customOption.textContent = 'Custom';
  select.append(customOption);

  presets.forEach((preset, index) => {
    const option = document.createElement('option');
    option.value = String(index);
    option.textContent = `${preset.name} (${preset.image})`;
    select.append(option);
  });

  const help = document.createElement('small');
  help.className = 'preset-help';
  help.textContent = presets[0]?.description || '';

  label.append('Preset', select);
  panel.append(label, help);
  form.prepend(panel);

  const applyPreset = (preset) => {
    if (!preset) return;
    imageInput.value = preset.image || '';
    if (!hideRepoInputs) {
      if (repoInput && preset.repoUrl !== undefined) repoInput.value = preset.repoUrl || '';
      if (branchInput && preset.branch !== undefined) branchInput.value = preset.branch || '';
    }
    if (ttlInput && preset.ttl !== undefined) ttlInput.value = preset.ttl || '';
    help.textContent = preset.description || '';
    activePresetEnv = normalizePresetEnv(preset.env);
  };

  select.addEventListener('change', () => {
    if (!select.value) {
      help.textContent = '';
      activePresetEnv = null;
      return;
    }
    const preset = presets[Number(select.value)];
    applyPreset(preset);
  });

  if (!imageInput.value && presets[0]) {
    select.value = '0';
    applyPreset(presets[0]);
  }

  presetsInitialized = true;
}

function cleanupTerminal() {
  if (activeTerminalSession) {
    activeTerminalSession.dispose();
    activeTerminalSession = null;
  }
  if (activeTerminalPoll) {
    activeTerminalPoll.cancelled = true;
    activeTerminalPoll = null;
  }
  const terminalCard = document.querySelector('.terminal-card');
  if (terminalCard) terminalCard.remove();
  if (createSection) createSection.hidden = false;
  if (listSection) listSection.hidden = false;
  activeTerminalName = '';
}

function handleRoute() {
  const terminalName = terminalNameFromPath();
    if (terminalName) {
      renderTerminalPage(terminalName);
    } else {
      if (activeTerminalName) cleanupTerminal();
      if (form && refreshBtn) {
        applyRepoDefaults();
        applySharedMountsDefaults();
        setupPresets();
        fetchSpritzes();
      }
    }
  }

window.addEventListener('hashchange', handleRoute);

if (form && refreshBtn) {
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const data = new FormData(form);
    const name = data.get('name');
    const image = data.get('image');

    const payload = {
      name,
      namespace: data.get('namespace') || undefined,
      spec: {
        image,
      },
    };

    if (config.ownerId) {
      payload.spec.owner = { id: config.ownerId };
    }

    const repo = data.get('repo');
    const branch = data.get('branch');
    const ttl = data.get('ttl');
    const sharedMountsRaw = data.get('shared_mounts');
    const repoUrl = (repo || defaultRepoUrl || '').toString().trim();
    const repoBranch = (branch || defaultRepoBranch || '').toString().trim();
    if (repoUrl) {
      payload.spec.repo = { url: repoUrl };
      if (repoBranch) payload.spec.repo.branch = repoBranch;
      if (defaultRepoDir) payload.spec.repo.dir = defaultRepoDir;
    }
    if (ttl) payload.spec.ttl = ttl;
    if (activePresetEnv && activePresetEnv.length > 0) {
      payload.spec.env = activePresetEnv;
    }
    if (sharedMountsRaw) {
      try {
        const mounts = parseSharedMountsInput(sharedMountsRaw);
        if (mounts && mounts.length > 0) {
          payload.userConfig = payload.userConfig || {};
          payload.userConfig.sharedMounts = mounts;
        }
      } catch (err) {
        showNotice(err.message || 'Invalid shared mounts YAML.');
        return;
      }
    }

    try {
      await request('/spritzes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });

      form.reset();
      activePresetEnv = null;
      const presetSelect = document.getElementById('preset-select');
      if (presetSelect) {
        presetSelect.value = '';
        const help = form.querySelector('.preset-help');
        if (help) help.textContent = '';
      }
      applyRepoDefaults();
      applySharedMountsDefaults();
      await fetchSpritzes();
      showNotice('');
    } catch (err) {
      showNotice(err.message || 'Failed to create spritz.');
    }
  });

  refreshBtn.addEventListener('click', fetchSpritzes);
}

clearAuthRedirectFlag();
handleRoute();
