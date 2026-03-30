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
  ticketBody?: unknown;
}): Promise<ResolvedWebSocketConnect> {
  const {
    apiBaseUrl,
    websocketBaseUrl,
    directConnectPath,
    ticketPath,
    useConnectTicket,
    ticketBody,
  } = options;

  if (!useConnectTicket) {
    return {
      wsUrl: buildApiWebSocketUrl(apiBaseUrl, directConnectPath, {
        websocketBaseUrl,
      }),
      protocols: [],
    };
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

  const ticket = await request<ConnectTicket>(ticketPath, requestOptions);
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
