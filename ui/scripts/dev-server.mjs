import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const uiDir = path.dirname(__dirname);
const distDir = path.join(uiDir, 'dist');

const port = Number.parseInt(process.env.PORT || '8081', 10);
const apiOrigin = process.env.SPRITZ_UI_DEV_API_ORIGIN || 'http://127.0.0.1:8090';
const ownerId = process.env.SPRITZ_UI_OWNER_ID || 'local-dev';

let assetVersion = `${Date.now()}`;
let buildInFlight = false;
let buildQueued = false;
let buildOk = false;
let lastBuildError = '';
let watchTimer = null;
const liveReloadClients = new Set();

function runtimeReplacements() {
  return {
    '__SPRITZ_API_BASE_URL__': '/api',
    '__SPRITZ_OWNER_ID__': ownerId,
    '__SPRITZ_UI_PRESETS__': process.env.SPRITZ_UI_PRESETS || 'null',
    '__SPRITZ_UI_DEFAULT_REPO_URL__': process.env.SPRITZ_UI_DEFAULT_REPO_URL || '',
    '__SPRITZ_UI_DEFAULT_REPO_DIR__': process.env.SPRITZ_UI_DEFAULT_REPO_DIR || '',
    '__SPRITZ_UI_DEFAULT_REPO_BRANCH__': process.env.SPRITZ_UI_DEFAULT_REPO_BRANCH || '',
    '__SPRITZ_UI_HIDE_REPO_INPUTS__': process.env.SPRITZ_UI_HIDE_REPO_INPUTS || '',
    '__SPRITZ_UI_LAUNCH_QUERY_PARAMS__': process.env.SPRITZ_UI_LAUNCH_QUERY_PARAMS || '',
    '__SPRITZ_UI_AUTH_MODE__': process.env.SPRITZ_UI_AUTH_MODE || '',
    '__SPRITZ_UI_AUTH_TOKEN_STORAGE__': process.env.SPRITZ_UI_AUTH_TOKEN_STORAGE || '',
    '__SPRITZ_UI_AUTH_TOKEN_STORAGE_KEYS__': process.env.SPRITZ_UI_AUTH_TOKEN_STORAGE_KEYS || '',
    '__SPRITZ_UI_AUTH_BEARER_TOKEN_PARAM__': process.env.SPRITZ_UI_AUTH_BEARER_TOKEN_PARAM || '',
    '__SPRITZ_UI_AUTH_LOGIN_URL__': process.env.SPRITZ_UI_AUTH_LOGIN_URL || '',
    '__SPRITZ_UI_AUTH_RETURN_TO_MODE__': process.env.SPRITZ_UI_AUTH_RETURN_TO_MODE || '',
    '__SPRITZ_UI_AUTH_RETURN_TO_PARAM__': process.env.SPRITZ_UI_AUTH_RETURN_TO_PARAM || '',
    '__SPRITZ_UI_AUTH_REDIRECT_ON_UNAUTHORIZED__': process.env.SPRITZ_UI_AUTH_REDIRECT_ON_UNAUTHORIZED || '',
    '__SPRITZ_UI_AUTH_REFRESH_ENABLED__': process.env.SPRITZ_UI_AUTH_REFRESH_ENABLED || '',
    '__SPRITZ_UI_AUTH_REFRESH_URL__': process.env.SPRITZ_UI_AUTH_REFRESH_URL || '',
    '__SPRITZ_UI_AUTH_REFRESH_METHOD__': process.env.SPRITZ_UI_AUTH_REFRESH_METHOD || '',
    '__SPRITZ_UI_AUTH_REFRESH_CREDENTIALS__': process.env.SPRITZ_UI_AUTH_REFRESH_CREDENTIALS || '',
    '__SPRITZ_UI_AUTH_REFRESH_TOKEN_STORAGE_KEYS__': process.env.SPRITZ_UI_AUTH_REFRESH_TOKEN_STORAGE_KEYS || '',
    '__SPRITZ_UI_AUTH_REFRESH_TIMEOUT_MS__': process.env.SPRITZ_UI_AUTH_REFRESH_TIMEOUT_MS || '',
    '__SPRITZ_UI_AUTH_REFRESH_COOLDOWN_MS__': process.env.SPRITZ_UI_AUTH_REFRESH_COOLDOWN_MS || '',
    '__SPRITZ_UI_AUTH_REFRESH_HEADERS__': process.env.SPRITZ_UI_AUTH_REFRESH_HEADERS || '',
    '__SPRITZ_UI_ASSET_VERSION__': assetVersion,
  };
}

function transformRuntimeContent(fileName, content) {
  let next = content;
  for (const [placeholder, value] of Object.entries(runtimeReplacements())) {
    next = next.split(placeholder).join(value);
  }
  if (fileName === 'index.html') {
    next = next.replace(
      '</body>',
      `<script>
const spritzLiveReload = new EventSource('/__spritz/live-reload');
spritzLiveReload.onmessage = function (event) {
  if (event.data === 'reload') window.location.reload();
};
</script></body>`,
    );
  }
  return next;
}

function notifyReload() {
  for (const client of liveReloadClients) {
    client.write('data: reload\n\n');
  }
}

function runBuild() {
  if (buildInFlight) {
    buildQueued = true;
    return;
  }
  buildInFlight = true;
  const child = spawn('pnpm', ['build'], {
    cwd: uiDir,
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let output = '';
  child.stdout.on('data', (chunk) => {
    const text = chunk.toString();
    output += text;
    process.stdout.write(text);
  });
  child.stderr.on('data', (chunk) => {
    const text = chunk.toString();
    output += text;
    process.stderr.write(text);
  });
  child.on('close', (code) => {
    buildInFlight = false;
    if (code === 0) {
      buildOk = true;
      lastBuildError = '';
      assetVersion = `${Date.now()}`;
      notifyReload();
      process.stdout.write('[spritz-ui] rebuild complete\n');
    } else {
      buildOk = false;
      lastBuildError = output || `build failed with exit code ${code}`;
      process.stderr.write('[spritz-ui] rebuild failed\n');
    }
    if (buildQueued) {
      buildQueued = false;
      runBuild();
    }
  });
}

function queueBuild(reason) {
  if (watchTimer) {
    clearTimeout(watchTimer);
  }
  watchTimer = setTimeout(() => {
    process.stdout.write(`[spritz-ui] change detected: ${reason}\n`);
    runBuild();
  }, 120);
}

function watchPath(target) {
  fs.watch(target, { recursive: true }, (_eventType, fileName) => {
    queueBuild(`${path.relative(uiDir, target)}${fileName ? `/${fileName}` : ''}`);
  });
}

function contentType(filePath) {
  switch (path.extname(filePath)) {
    case '.html':
      return 'text/html; charset=utf-8';
    case '.js':
      return 'application/javascript; charset=utf-8';
    case '.css':
      return 'text/css; charset=utf-8';
    case '.json':
      return 'application/json; charset=utf-8';
    case '.svg':
      return 'image/svg+xml';
    default:
      return 'application/octet-stream';
  }
}

function writeBuildError(res) {
  res.writeHead(503, { 'content-type': 'text/plain; charset=utf-8' });
  res.end(buildInFlight ? 'UI build in progress...\n' : `UI build failed.\n\n${lastBuildError}\n`);
}

function proxyRequest(req, res) {
  const target = new URL(req.url, apiOrigin);
  const proxyReq = http.request(
    target,
    {
      method: req.method,
      headers: {
        ...req.headers,
        host: target.host,
        connection: 'close',
      },
    },
    (proxyRes) => {
      res.writeHead(proxyRes.statusCode || 502, proxyRes.headers);
      proxyRes.pipe(res);
    },
  );
  proxyReq.on('error', (error) => {
    res.writeHead(502, { 'content-type': 'text/plain; charset=utf-8' });
    res.end(`proxy error: ${error.message}\n`);
  });
  req.pipe(proxyReq);
}

function serveLiveReload(req, res) {
  res.writeHead(200, {
    'content-type': 'text/event-stream',
    'cache-control': 'no-cache, no-transform',
    connection: 'keep-alive',
  });
  res.write('\n');
  liveReloadClients.add(res);
  req.on('close', () => {
    liveReloadClients.delete(res);
  });
}

function serveFile(req, res) {
  if (!buildOk) {
    writeBuildError(res);
    return;
  }
  const url = new URL(req.url, `http://${req.headers.host || '127.0.0.1'}`);
  let filePath = path.join(distDir, decodeURIComponent(url.pathname));
  if (url.pathname === '/') {
    filePath = path.join(distDir, 'index.html');
  }
  if (!filePath.startsWith(distDir)) {
    res.writeHead(403);
    res.end('forbidden\n');
    return;
  }
  if (!fs.existsSync(filePath) || fs.statSync(filePath).isDirectory()) {
    filePath = path.join(distDir, 'index.html');
  }
  const baseName = path.basename(filePath);
  if (baseName === 'index.html' || baseName === 'config.js') {
    const content = fs.readFileSync(filePath, 'utf8');
    res.writeHead(200, { 'content-type': contentType(filePath), 'cache-control': 'no-store' });
    res.end(transformRuntimeContent(baseName, content));
    return;
  }
  const headers = { 'content-type': contentType(filePath) };
  if (/\.(?:js|css)$/.test(filePath)) {
    headers['cache-control'] = 'no-store';
  }
  res.writeHead(200, headers);
  fs.createReadStream(filePath).pipe(res);
}

runBuild();
watchPath(path.join(uiDir, 'src'));
watchPath(path.join(uiDir, 'public'));
watchPath(path.join(uiDir, 'scripts'));

const server = http.createServer((req, res) => {
  if (!req.url) {
    res.writeHead(400);
    res.end('bad request\n');
    return;
  }
  if (req.url === '/__spritz/live-reload') {
    serveLiveReload(req, res);
    return;
  }
  if (req.url.startsWith('/api/')) {
    proxyRequest(req, res);
    return;
  }
  serveFile(req, res);
});

server.on('upgrade', (req, socket, head) => {
  if (!req.url || !req.url.startsWith('/api/')) {
    socket.destroy();
    return;
  }
  const target = new URL(req.url, apiOrigin);
  const proxyReq = http.request({
    protocol: target.protocol,
    hostname: target.hostname,
    port: target.port,
    path: target.pathname + target.search,
    method: req.method,
    headers: {
      ...req.headers,
      host: target.host,
      connection: 'Upgrade',
      upgrade: req.headers.upgrade || 'websocket',
    },
  });

  proxyReq.on('upgrade', (proxyRes, proxySocket, proxyHead) => {
    const headers = Object.entries(proxyRes.headers)
      .map(([key, value]) => `${key}: ${value}`)
      .join('\r\n');
    socket.write(`HTTP/1.1 101 Switching Protocols\r\n${headers}\r\n\r\n`);
    if (proxyHead.length > 0) {
      socket.write(proxyHead);
    }
    if (head.length > 0) {
      proxySocket.write(head);
    }
    proxySocket.pipe(socket).pipe(proxySocket);
  });

  proxyReq.on('error', () => socket.destroy());
  proxyReq.end();
});

server.listen(port, '127.0.0.1', () => {
  process.stdout.write(`spritz ui dev server listening on http://127.0.0.1:${port}\n`);
});
