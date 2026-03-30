import { request } from '@/lib/api';
import { buildApiWebSocketUrl, buildWebSocketUrlFromConnectPath } from '@/lib/network';

export interface ConnectTicket {
  type: 'connect-ticket';
  ticket: string;
  expiresAt: string;
  protocol: string;
  connectPath: string;
}

export interface ResolvedWebSocketConnect {
  wsUrl: string;
  protocols: string[];
}

function getErrorStatus(error: unknown): number | null {
  const status = (error as { status?: unknown })?.status;
  return typeof status === 'number' ? status : null;
}

function shouldFallbackToDirectConnect(error: unknown): boolean {
  const status = getErrorStatus(error);
  return status === 404 || status === 405 || status === 501;
}

/**
 * Resolves the browser WebSocket handshake for a Spritz endpoint.
 *
 * Hosted browser sessions can connect directly. Bearer-token browser clients
 * first mint a short-lived connect ticket and present it as a subprotocol.
 */
export async function resolveWebSocketConnect(options: {
  apiBaseUrl: string;
  websocketBaseUrl: string;
  directConnectPath: string;
  ticketPath: string;
  useConnectTicket: boolean;
  bearerToken?: string;
  bearerTokenParam?: string;
  ticketBody?: unknown;
}): Promise<ResolvedWebSocketConnect> {
  const {
    apiBaseUrl,
    websocketBaseUrl,
    directConnectPath,
    ticketPath,
    useConnectTicket,
    bearerToken,
    bearerTokenParam,
    ticketBody,
  } = options;

  const directConnection: ResolvedWebSocketConnect = {
    wsUrl: buildApiWebSocketUrl(apiBaseUrl, directConnectPath, {
      bearerToken,
      bearerTokenParam,
      websocketBaseUrl,
    }),
    protocols: [],
  };

  if (!useConnectTicket) {
    return directConnection;
  }

  const headers: Record<string, string> = {};
  const requestOptions: RequestInit = { method: 'POST' };
  if (ticketBody !== undefined) {
    headers['Content-Type'] = 'application/json';
    requestOptions.body = JSON.stringify(ticketBody);
  }
  if (Object.keys(headers).length > 0) {
    requestOptions.headers = headers;
  }

  let ticket: ConnectTicket | null;
  try {
    ticket = await request<ConnectTicket>(ticketPath, requestOptions);
  } catch (error) {
    if (shouldFallbackToDirectConnect(error)) {
      return directConnection;
    }
    throw error;
  }
  if (!ticket || !ticket.ticket || !ticket.protocol || !ticket.connectPath) {
    throw new Error('Invalid connect ticket response.');
  }

  return {
    wsUrl: buildWebSocketUrlFromConnectPath(ticket.connectPath, {
      apiBaseUrl,
      websocketBaseUrl,
    }),
    protocols: [ticket.protocol, `spritz-ticket.v1.${ticket.ticket}`],
  };
}
