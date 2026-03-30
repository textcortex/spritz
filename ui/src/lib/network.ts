const URL_PARSE_BASE = 'http://spritz.local';

function resolveLocationHref(locationHref?: string): string {
  if (locationHref) return locationHref;
  if (typeof window !== 'undefined' && window.location?.href) {
    return window.location.href;
  }
  return `${URL_PARSE_BASE}/`;
}

function normalizeApiBaseUrl(apiBaseUrl: string, locationHref?: string): URL {
  const trimmed = String(apiBaseUrl || '').trim();
  const base = trimmed || '/';
  return new URL(base, resolveLocationHref(locationHref));
}

function normalizeWebSocketBaseUrl(
  apiBaseUrl: string,
  websocketBaseUrl?: string,
  locationHref?: string,
): URL {
  const location = new URL(resolveLocationHref(locationHref));
  const explicitBase = String(websocketBaseUrl || '').trim();
  if (explicitBase) {
    return new URL(explicitBase, location.href);
  }
  const apiUrl = normalizeApiBaseUrl(apiBaseUrl, locationHref);
  const sameHostUrl = new URL(location.origin);
  sameHostUrl.pathname = apiUrl.pathname;
  sameHostUrl.search = apiUrl.search;
  sameHostUrl.hash = apiUrl.hash;
  return sameHostUrl;
}

function normalizeRelativePath(path: string): URL {
  const trimmed = String(path || '').trim();
  const normalized = trimmed.startsWith('/') ? trimmed : `/${trimmed}`;
  return new URL(normalized || '/', URL_PARSE_BASE);
}

function joinPaths(basePath: string, relativePath: string): string {
  const normalizedBase = `/${String(basePath || '').replace(/^\/+|\/+$/g, '')}`;
  const normalizedRelative = String(relativePath || '').replace(/^\/+/, '');
  if (!normalizedRelative) return normalizedBase === '/' ? '/' : normalizedBase;
  if (normalizedBase === '/') return `/${normalizedRelative}`;
  return `${normalizedBase}/${normalizedRelative}`;
}

export function buildApiWebSocketUrl(
  apiBaseUrl: string,
  path: string,
  options?: {
    bearerToken?: string;
    bearerTokenParam?: string;
    websocketBaseUrl?: string;
    locationHref?: string;
  },
): string {
  const url = normalizeWebSocketBaseUrl(
    apiBaseUrl,
    options?.websocketBaseUrl,
    options?.locationHref,
  );
  const relative = normalizeRelativePath(path);
  url.pathname = joinPaths(url.pathname, relative.pathname);
  url.search = relative.search;
  url.hash = relative.hash;
  if (url.protocol === 'https:') {
    url.protocol = 'wss:';
  } else if (url.protocol === 'http:') {
    url.protocol = 'ws:';
  }
  const bearerToken = String(options?.bearerToken || '').trim();
  if (bearerToken) {
    url.searchParams.set(
      String(options?.bearerTokenParam || 'token').trim() || 'token',
      bearerToken,
    );
  }
  return url.toString();
}

/**
 * Builds a browser WebSocket URL from a server-supplied connect path.
 *
 * The connect path is expected to be rooted at the server API path already, so
 * we preserve it instead of appending it to the configured API base path.
 */
export function buildWebSocketUrlFromConnectPath(
  connectPath: string,
  options?: {
    websocketBaseUrl?: string;
    locationHref?: string;
  },
): string {
  const url = normalizeWebSocketBaseUrl('', options?.websocketBaseUrl, options?.locationHref);
  const relative = normalizeRelativePath(connectPath);
  url.pathname = relative.pathname;
  url.search = relative.search;
  url.hash = relative.hash;
  if (url.protocol === 'https:') {
    url.protocol = 'wss:';
  } else if (url.protocol === 'http:') {
    url.protocol = 'ws:';
  }
  return url.toString();
}
