import http from "node:http";

const DEFAULT_SHUTDOWN_TIMEOUT_MS = 5_000;

export function normalizePath(value, fallback) {
  const raw = typeof value === "string" ? value.trim() : "";
  if (!raw) {
    return fallback;
  }
  return raw.startsWith("/") ? raw : `/${raw}`;
}

export function parseListenAddress(value) {
  const raw = typeof value === "string" ? value.trim() : "";
  if (!raw) {
    throw new Error("listen address is required");
  }
  if (raw.startsWith("[")) {
    const closing = raw.indexOf("]");
    if (closing === -1 || raw[closing + 1] !== ":") {
      throw new Error(`invalid listen address: ${raw}`);
    }
    return {
      host: raw.slice(1, closing),
      port: Number.parseInt(raw.slice(closing + 2), 10),
    };
  }
  const separator = raw.lastIndexOf(":");
  if (separator === -1) {
    throw new Error(`invalid listen address: ${raw}`);
  }
  return {
    host: raw.slice(0, separator),
    port: Number.parseInt(raw.slice(separator + 1), 10),
  };
}

export function writeJSON(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(body));
}

export function resolveWSExports(wsModule) {
  const WebSocket = wsModule?.WebSocket ?? wsModule?.default?.WebSocket ?? wsModule?.default ?? wsModule;
  const WebSocketServer =
    wsModule?.WebSocketServer ??
    wsModule?.Server ??
    wsModule?.default?.WebSocketServer ??
    wsModule?.default?.Server;
  return { WebSocket, WebSocketServer };
}

export function resolveWebSocketServerExport(wsModule) {
  return (
    wsModule?.WebSocketServer ??
    wsModule?.Server ??
    wsModule?.default?.WebSocketServer ??
    wsModule?.default?.Server
  );
}

export function createACPRequestHandler({ config, runtime, logger }) {
  return async function handleRequest(req, res) {
    const pathname = new URL(req.url ?? "/", "http://spritz-acp.local").pathname;
    if (req.method === "GET" && pathname === config.healthPath) {
      const health = await runtime.health();
      const statusCode = health.ok ? 200 : 503;
      writeJSON(res, statusCode, health);
      return;
    }
    if (req.method === "GET" && pathname === config.metadataPath) {
      writeJSON(res, 200, runtime.metadata);
      return;
    }
    if (req.method === "GET" && pathname === config.acpPath) {
      res.writeHead(426, { "content-type": "text/plain; charset=utf-8" });
      res.end("upgrade required");
      return;
    }
    logger?.warn?.(`unexpected ACP adapter request: ${req.method} ${pathname}`);
    res.writeHead(404);
    res.end();
  };
}

export async function serveSpritzACPServer({
  config,
  runtime,
  loadWSModule,
  logger = console,
  serverName = "spritz-acp-server",
  shutdownTimeoutMs = DEFAULT_SHUTDOWN_TIMEOUT_MS,
}) {
  const wsModule = await loadWSModule();
  const WebSocketServer = resolveWebSocketServerExport(wsModule);
  if (!WebSocketServer) {
    throw new Error("Failed to load WebSocketServer from ws.");
  }

  const { host, port } = parseListenAddress(config.listenAddr);
  const server = http.createServer(createACPRequestHandler({ config, runtime, logger }));
  const wss = new WebSocketServer({ noServer: true });

  wss.on("connection", (socket, request) => {
    runtime.attachWebSocket(socket, request);
  });

  server.on("upgrade", (request, socket, head) => {
    const pathname = new URL(request.url ?? "/", "http://spritz-acp.local").pathname;
    if (pathname !== config.acpPath) {
      socket.destroy();
      return;
    }
    wss.handleUpgrade(request, socket, head, (websocket) => {
      wss.emit("connection", websocket, request);
    });
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, resolve);
  });
  logger?.log?.(`${serverName} listening on ${host}:${port}${config.acpPath}`);

  let closingPromise = null;
  const close = async () => {
    if (closingPromise) {
      return closingPromise;
    }
    closingPromise = (async () => {
      server.closeIdleConnections?.();
      server.closeAllConnections?.();
      for (const client of wss.clients) {
        try {
          client.close();
        } catch {}
      }
      await Promise.all([
        new Promise((resolve) => server.close(resolve)),
        Promise.resolve(runtime.close?.()),
      ]);
    })();
    return closingPromise;
  };

  const signalHandler = () => {
    const forceExitTimer = setTimeout(() => process.exit(1), shutdownTimeoutMs);
    forceExitTimer.unref?.();
    void close().finally(() => {
      clearTimeout(forceExitTimer);
      process.exit(0);
    });
  };
  process.once("SIGINT", signalHandler);
  process.once("SIGTERM", signalHandler);

  return {
    server,
    wss,
    close,
  };
}
