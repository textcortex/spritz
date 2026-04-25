import { config } from './config';

export interface SlackInstallTargetProfile {
  name: string;
  imageUrl?: string;
}

export interface SlackInstallTarget {
  id: string;
  profile: SlackInstallTargetProfile;
  ownerLabel?: string;
  presetInputs: Record<string, unknown>;
}

export interface SlackManagedInstallationRoute {
  principalId: string;
  provider: string;
  externalScopeType: string;
  externalTenantId: string;
}

export interface SlackManagedChannelRoute {
  id: string;
  externalChannelId: string;
  externalChannelType?: string;
  requireMention?: boolean | null;
  enabled?: boolean | null;
}

export interface SlackManagedConnection {
  id: string;
  displayName?: string;
  isDefault: boolean;
  state: string;
  routes?: SlackManagedChannelRoute[];
}

export interface SlackManagedInstallation {
  id: string;
  route: SlackManagedInstallationRoute;
  state: string;
  currentTarget?: {
    id: string;
    profile: SlackInstallTargetProfile;
    ownerLabel?: string;
  } | null;
  installationConfig?: {
    channelPolicies?: SlackManagedChannelRoute[];
  };
  connections?: SlackManagedConnection[];
  allowedActions?: string[];
  problemCode?: string;
  disconnectedAt?: string;
}

export interface SlackInstallSelection {
  status: string;
  state: string;
  requestId: string;
  teamId: string;
  targets: SlackInstallTarget[];
}

export interface SlackInstallResult {
  status: string;
  code: string;
  operation?: string;
  provider?: string;
  requestId?: string;
  teamId?: string;
  title: string;
  message: string;
  retryable?: boolean;
  actionLabel?: string;
  actionHref?: string;
}

export interface SlackWorkspaceTestResult {
  status: string;
  outcome?: string;
  reply?: string;
  conversationId?: string;
  postedMessageTs?: string;
}

export function slackGatewayBasePath(): string {
  const configured = String(config.slackGatewayBasePath || '').trim();
  if (/^\/+$/.test(configured)) return '';
  const normalized = (configured || '/slack-gateway').replace(/\/+$/g, '');
  if (!normalized) return '/slack-gateway';
  return normalized.startsWith('/') ? normalized : `/${normalized}`;
}

export function slackGatewayPath(path: string): string {
  const normalizedPath = `/${String(path || '').replace(/^\/+/g, '')}`;
  return `${slackGatewayBasePath()}${normalizedPath}`;
}

async function parseGatewayResponse(res: Response): Promise<unknown> {
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function gatewayErrorMessage(payload: unknown, fallback: string): string {
  if (payload && typeof payload === 'object' && 'message' in payload) {
    const message = String((payload as { message?: unknown }).message || '').trim();
    if (message) return message;
  }
  if (typeof payload === 'string' && payload.trim()) return payload.trim();
  return fallback;
}

export async function slackGatewayRequest<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers || {});
  if (options.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const res = await fetch(slackGatewayPath(path), {
    credentials: 'include',
    ...options,
    headers,
  });
  const payload = await parseGatewayResponse(res);
  if (!res.ok) {
    throw new Error(gatewayErrorMessage(payload, res.statusText || 'Request failed'));
  }
  return payload as T;
}

export function primaryConnection(
  installation?: SlackManagedInstallation | null,
): SlackManagedConnection | null {
  const connections = installation?.connections || [];
  return (
    connections.find((connection) => connection.isDefault) ||
    connections[0] ||
    null
  );
}

export function connectionRoutePath(installationId: string, connectionId: string): string {
  return `/settings/slack/channels/installations/${encodeURIComponent(
    installationId,
  )}/connections/${encodeURIComponent(connectionId)}`;
}

export function installationRoutePath(installationId: string): string {
  return `/settings/slack/channels/installations/${encodeURIComponent(installationId)}`;
}
