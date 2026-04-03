import { config } from './config';
import { normalizeHtmlErrorText } from './html-error';

function parseBoolean(value: unknown, fallback: boolean): boolean {
  if (value === undefined || value === null || value === '') return fallback;
  if (typeof value === 'boolean') return value;
  const normalized = String(value).trim().toLowerCase();
  if (['true', '1', 'yes', 'y', 'on'].includes(normalized)) return true;
  if (['false', '0', 'no', 'n', 'off'].includes(normalized)) return false;
  return fallback;
}

function parseNumber(value: unknown, fallback: number): number {
  if (value === undefined || value === null || value === '') return fallback;
  const parsed = Number(value);
  return Number.isNaN(parsed) ? fallback : parsed;
}

function parseStorageKeys(value: unknown): string[] {
  if (!value) return [];
  if (Array.isArray(value)) {
    return value.map((item) => String(item).trim()).filter(Boolean);
  }
  return String(value)
    .split(/[;,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

// Derived auth config
const authConfig = config.auth;
const authMode = (authConfig.mode || '').toLowerCase();
const authTokenStorage = (authConfig.tokenStorage || 'localStorage').toLowerCase();
const authTokenStorageKeys = parseStorageKeys(authConfig.tokenStorageKeys);
const authBearerTokenParam = (authConfig.bearerTokenParam || 'token').trim() || 'token';
const authLoginUrl = (authConfig.loginUrl || '').trim();
const authReturnToMode = (authConfig.returnToMode || 'auto').toLowerCase();
const authReturnToParam = (authConfig.returnToParam || '').trim();
const authRedirectOnUnauthorized = parseBoolean(authConfig.redirectOnUnauthorized, true);
const authRefreshConfig = authConfig.refresh;
const authRefreshEnabled = parseBoolean(authRefreshConfig.enabled, false);
const authRefreshUrl = String(authRefreshConfig.url || '').trim();
const authRefreshMethod = String(authRefreshConfig.method || 'POST').toUpperCase();
const authRefreshCredentials = String(authRefreshConfig.credentials || 'include');
const authRefreshTimeoutMs = parseNumber(authRefreshConfig.timeoutMs, 5000);
const authRefreshCooldownMs = parseNumber(authRefreshConfig.cooldownMs, 30000);
const authRefreshHeadersRaw = String(authRefreshConfig.headers || '').trim();
const authRefreshTokenStorageKeys = parseStorageKeys(authRefreshConfig.tokenStorageKeys);
const authRedirectStorageKey = 'spritz-auth-redirected';
const authReturnToPlaceholder = '__SPRITZ_RETURN_TO__';

let authRefreshInFlight: Promise<{ ok: boolean; attemptId: number }> | null = null;
let authRefreshAttemptId = 0;
let lastAuthRefreshAt = 0;

function getStorage(): Storage | null {
  if (authTokenStorage === 'sessionstorage' && window.sessionStorage) {
    return window.sessionStorage;
  }
  return window.localStorage || null;
}

function readTokenFromStorage(keys: string[]): string {
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

function writeTokenToStorage(keys: string[], token: string): boolean {
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

function authModeAllowsBearer(): boolean {
  if (!authMode) return false;
  if (authMode === 'bearer' || authMode === 'auto') return true;
  const parts = authMode.split(/[,+]/).map((part) => part.trim());
  return parts.includes('bearer');
}

export function getAuthToken(): string {
  if (!authModeAllowsBearer()) return '';
  return readTokenFromStorage(authTokenStorageKeys);
}

/**
 * Attempts the configured bearer refresh flow for direct WebSocket connections.
 * Returns the latest stored bearer token and whether a refresh succeeded.
 */
export async function refreshAuthTokenForWebSocket(): Promise<{
  token: string;
  refreshed: boolean;
}> {
  const token = getAuthToken();
  if (!shouldAttemptAuthRefresh()) {
    return { token, refreshed: false };
  }
  const refreshResult = await runAuthRefresh();
  return {
    token: getAuthToken(),
    refreshed: refreshResult.ok,
  };
}

function getAuthRefreshToken(): string {
  return readTokenFromStorage(authRefreshTokenStorageKeys);
}

export function clearAuthRedirectFlag(): void {
  try {
    if (window.sessionStorage) {
      window.sessionStorage.removeItem(authRedirectStorageKey);
    }
  } catch {
    // ignore storage errors
  }
}

function shouldRedirectOnUnauthorized(): boolean {
  return Boolean(authLoginUrl) && authRedirectOnUnauthorized;
}

function isLoginUrlAbsolute(value: string): boolean {
  if (!value) return false;
  return /^https?:\/\//i.test(value) || value.startsWith('//');
}

function buildReturnToUrl(): string {
  const path = window.location.pathname || '/';
  const search = window.location.search || '';
  const hash = window.location.hash || '';
  const returnPath = `${path}${search}${hash}`;
  const mode =
    authReturnToMode === 'auto'
      ? isLoginUrlAbsolute(authLoginUrl)
        ? 'absolute'
        : 'path'
      : authReturnToMode;
  if (mode === 'absolute') {
    return `${window.location.origin}${returnPath}`;
  }
  return returnPath;
}

function resolveLoginUrl(): string {
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

function redirectToLogin(message?: string): boolean {
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
  window.location.assign(loginHref);
  return true;
}

function hasAuthorizationHeader(headers: Record<string, string>): boolean {
  if (!headers || typeof headers !== 'object') return false;
  return Object.keys(headers).some((key) => key.toLowerCase() === 'authorization');
}

function buildRefreshHeaders(): Record<string, string> {
  let headers: Record<string, string> = {};
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

function normalizeRefreshCredentials(value: string): RequestCredentials {
  if (value === 'omit' || value === 'same-origin' || value === 'include') {
    return value;
  }
  return 'include';
}

function shouldAttemptAuthRefresh(): boolean {
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

function readTokenField(payload: Record<string, unknown>, key: string): string {
  if (!payload || typeof payload !== 'object') return '';
  const direct = payload[key];
  if (typeof direct === 'string' && direct) return direct;
  if (direct) return String(direct);
  const data = payload.data as Record<string, unknown> | undefined;
  const nested = data && typeof data === 'object' ? data[key] : null;
  if (typeof nested === 'string' && nested) return nested;
  if (nested) return String(nested);
  return '';
}

function extractAuthTokens(payload: Record<string, unknown>) {
  const accessToken =
    readTokenField(payload, 'access_token') ||
    readTokenField(payload, 'accessToken') ||
    readTokenField(payload, 'token');
  const refreshToken =
    readTokenField(payload, 'refresh_token') ||
    readTokenField(payload, 'refreshToken');
  return { accessToken, refreshToken };
}

function updateAuthTokensFromPayload(payload: Record<string, unknown>) {
  const { accessToken, refreshToken } = extractAuthTokens(payload);
  const accessUpdated = accessToken ? writeTokenToStorage(authTokenStorageKeys, accessToken) : false;
  const refreshUpdated = refreshToken
    ? writeTokenToStorage(authRefreshTokenStorageKeys, refreshToken)
    : false;
  return { accessUpdated, refreshUpdated };
}

async function readResponse(res: Response) {
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

function isJSend(data: unknown): data is { status: string; message?: string; data?: Record<string, unknown> } {
  if (!data || typeof data !== 'object') return false;
  return 'status' in data;
}

async function runAuthRefresh(): Promise<{ ok: boolean; attemptId: number }> {
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

interface RequestOptions extends RequestInit {
  __authRefreshAttemptId?: number;
}

interface ApiError extends Error {
  status: number;
  payload?: unknown;
}

function normalizeErrorMessage(value: unknown): string {
  const text = String(value || '').trim();
  if (!text) return '';
  return normalizeHtmlErrorText(text);
}

function structuredPublicError(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' ? (value as Record<string, unknown>) : null;
}

function jsendPublicErrorMessage(jsend: Record<string, unknown> | null): string {
  if (!jsend) return '';
  const data = structuredPublicError(jsend.data);
  const directError = structuredPublicError(data?.error);
  return (
    normalizeErrorMessage(directError?.message) ||
    normalizeErrorMessage(data?.message) ||
    normalizeErrorMessage(jsend.message) ||
    normalizeErrorMessage(data?.error)
  );
}

export async function request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T | null> {
  const headers = new Headers(options.headers || {});
  const token = getAuthToken();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  const res = await fetch(`${config.apiBaseUrl}${path}`, {
    credentials: 'include',
    ...options,
    headers,
  });
  const { text, data } = await readResponse(res);
  const jsend = isJSend(data) ? data : null;

  if (res.ok && jsend && jsend.status !== 'success') {
    const message =
      jsendPublicErrorMessage(jsend as Record<string, unknown>) ||
      normalizeErrorMessage(text) ||
      res.statusText ||
      'Request failed';
    const err = new Error(String(message)) as ApiError;
    err.status = res.status;
    err.payload = jsend;
    throw err;
  }

  if (!res.ok) {
    const message =
      (jsend && jsendPublicErrorMessage(jsend as Record<string, unknown>)) ||
      normalizeErrorMessage((data as Record<string, unknown>)?.error) ||
      normalizeErrorMessage((data as Record<string, unknown>)?.message) ||
      normalizeErrorMessage(text) ||
      res.statusText ||
      'Request failed';

    if (
      (res.status === 401 || res.status === 403) &&
      options.__authRefreshAttemptId !== authRefreshAttemptId
    ) {
      const refreshResult = await runAuthRefresh();
      if (refreshResult.ok) {
        return request<T>(path, { ...options, __authRefreshAttemptId: refreshResult.attemptId });
      }
    }
    if ((res.status === 401 || res.status === 403) && redirectToLogin(String(message))) {
      throw new Error(String(message));
    }
    const err = new Error(String(message)) as ApiError;
    err.status = res.status;
    err.payload = jsend || data;
    throw err;
  }

  clearAuthRedirectFlag();
  if (res.status === 204) return null;
  if (jsend) return (jsend.data ?? null) as T;
  if (data !== null) return data as T;
  return text ? (text as unknown as T) : null;
}

export { authBearerTokenParam };
