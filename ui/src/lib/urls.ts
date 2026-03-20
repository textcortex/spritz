import type { Spritz } from '@/types/spritz';
import { config } from './config';

/** Returns the canonical chat path prefix for the active Spritz UI config. */
export function normalizeChatPathPrefix(raw: string | undefined = config.chatPathPrefix): string {
  const trimmed = String(raw || '').trim();
  if (!trimmed || trimmed === '/') return '/c';
  const prefixed = trimmed.startsWith('/') ? trimmed : `/${trimmed}`;
  return prefixed.length > 1 ? prefixed.replace(/\/+$/, '') : '/c';
}

function chatRoutePrefixSegment(): string {
  return normalizeChatPathPrefix().replace(/^\/+/, '');
}

function parseBoolean(value: unknown, fallback: boolean): boolean {
  if (value === undefined || value === null || value === '') return fallback;
  if (typeof value === 'boolean') return value;
  const normalized = String(value).trim().toLowerCase();
  if (['true', '1', 'yes', 'y', 'on'].includes(normalized)) return true;
  if (['false', '0', 'no', 'n', 'off'].includes(normalized)) return false;
  return fallback;
}

function normalizeTemplateMap(value: unknown): Record<string, string> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  const normalized: Record<string, string> = {};
  for (const [key, raw] of Object.entries(value as Record<string, unknown>)) {
    const name = String(key || '').trim();
    if (!name) continue;
    if (raw === undefined || raw === null) continue;
    normalized[name] = String(raw);
  }
  return normalized;
}

function parseTemplateMap(value: unknown): Record<string, string> {
  const placeholder = '__SPRITZ_UI_LAUNCH_QUERY_PARAMS__';
  if (!value) return {};
  if (typeof value === 'object') return normalizeTemplateMap(value);
  const trimmed = String(value).trim();
  if (!trimmed || trimmed === placeholder) return {};
  try {
    return normalizeTemplateMap(JSON.parse(trimmed));
  } catch {
    return {};
  }
}

const launchQueryParams = parseTemplateMap(config.launch.queryParams);

function applyTemplatePlaceholders(template: string, context: Record<string, string>): string {
  if (!template) return '';
  return String(template).replace(/\{([a-zA-Z0-9_]+)\}/g, (_, key: string) => {
    const value = context[key];
    if (value === undefined || value === null) return '';
    return String(value);
  });
}

export function buildOpenUrl(rawUrl: string | undefined, spritz: Spritz): string {
  const input = String(rawUrl || '').trim();
  if (!input) return '';
  let url: URL;
  try {
    url = new URL(input, window.location.href);
  } catch {
    return input;
  }
  const queryEntries = Object.entries(launchQueryParams);
  if (!queryEntries.length) return url.href;

  const name = String(spritz?.metadata?.name || '').trim();
  const namespace = String(spritz?.metadata?.namespace || '').trim();
  const context: Record<string, string> = {
    origin: url.origin,
    host: url.host,
    hostname: url.hostname,
    path: url.pathname,
    query: url.search,
    name,
    namespace,
    name_encoded: encodeURIComponent(name),
    namespace_encoded: encodeURIComponent(namespace),
    path_encoded: encodeURIComponent(url.pathname),
    ui_origin: window.location.origin,
    ws_origin: url.origin.replace(/^http/i, 'ws'),
  };
  for (const [param, template] of queryEntries) {
    const value = applyTemplatePlaceholders(template, context);
    url.searchParams.set(param, value);
  }
  return url.href;
}

export function describeChatAction(spritz: Spritz): { label: string; title: string; disabled: boolean } {
  const phase = String(spritz?.status?.phase || '').trim().toLowerCase();
  const acpState = String(spritz?.status?.acp?.state || '').trim().toLowerCase();
  if (acpState === 'ready') {
    return { label: 'Chat', title: 'Open agent chat.', disabled: false };
  }
  if (phase === 'ready') {
    return { label: 'Preparing chat…', title: 'Instance is ready, but chat services are still starting.', disabled: true };
  }
  if (phase === 'failed' || phase === 'error') {
    return { label: 'Chat status', title: 'Open the chat page to inspect the current instance state.', disabled: false };
  }
  return { label: 'Starting…', title: 'Instance is still provisioning.', disabled: true };
}

export function terminalPath(name: string): string {
  return `/terminal/${encodeURIComponent(name)}`;
}

/** Returns the canonical chat route for an instance, or the chat landing page when omitted. */
export function chatPath(name?: string): string {
  const prefix = normalizeChatPathPrefix();
  if (!name) return prefix;
  return `${prefix}/${encodeURIComponent(name)}`;
}

/** Returns the canonical chat route for a specific instance conversation. */
export function chatConversationPath(name: string, conversationId: string): string {
  return `${chatPath(name)}/${encodeURIComponent(conversationId)}`;
}

/** Returns the React Router path fragment for the canonical chat prefix. */
export function chatRoutePath(optionalName: boolean): string {
  return `${chatRoutePrefixSegment()}/:name${optionalName ? "?" : ""}`;
}

/** Returns the React Router path fragment for the canonical conversation route. */
export function chatConversationRoutePath(): string {
  return `${chatRoutePrefixSegment()}/:name/:conversationId`;
}

export const hideRepoInputs = parseBoolean(config.repoDefaults.hideInputs, false);
export const defaultRepoUrl = String(config.repoDefaults.url || '').trim();
export const defaultRepoBranch = String(config.repoDefaults.branch || '').trim();
export const defaultRepoDir = String(config.repoDefaults.dir || '').trim();
