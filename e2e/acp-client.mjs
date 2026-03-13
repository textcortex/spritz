import { spawn } from 'node:child_process';
import net from 'node:net';

import {
  extractACPText,
  findFreePort,
  joinACPTextChunks,
  resolveWebSocketConstructor,
  waitForWebSocketOpen,
} from './acp-smoke-lib.mjs';

export function waitForLocalPort(port, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    async function probe() {
      if (Date.now() >= deadline) {
        reject(new Error(`local port ${port} did not become reachable in time`));
        return;
      }
      const reachable = await new Promise((probeResolve) => {
        const socket = net.connect({ host: '127.0.0.1', port });
        socket.once('connect', () => {
          socket.destroy();
          probeResolve(true);
        });
        socket.once('error', () => {
          probeResolve(false);
        });
      });
      if (reachable) {
        resolve();
        return;
      }
      setTimeout(probe, 250);
    }
    probe().catch(reject);
  });
}

export async function startACPPortForward(namespace, serviceName, targetPort) {
  const localPort = await findFreePort();
  const child = spawn('kubectl', ['-n', namespace, 'port-forward', `service/${serviceName}`, `${localPort}:${targetPort}`], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => {
    stdout += String(chunk);
  });
  child.stderr.on('data', (chunk) => {
    stderr += String(chunk);
  });
  const exitPromise = new Promise((resolve) => {
    child.once('close', (code, signal) => resolve({ code, signal, stdout, stderr }));
  });
  try {
    await waitForLocalPort(localPort, 10000);
    return {
      localPort,
      async stop() {
        child.kill('SIGTERM');
        const result = await exitPromise;
        if (result.code && result.code !== 0 && !/terminated|signal/i.test(result.stderr || '')) {
          throw new Error(`kubectl port-forward failed:\n${result.stderr || result.stdout}`);
        }
      },
    };
  } catch (error) {
    child.kill('SIGTERM');
    const result = await exitPromise;
    throw new Error(`${error.message}\nport-forward stderr:\n${result.stderr}`);
  }
}

export async function connectACPWebSocket(url, timeoutSeconds, options = {}) {
  const WebSocket = options.WebSocket || resolveWebSocketConstructor();
  const socket = new WebSocket(url);
  const pending = new Map();
  const updates = [];
  const rpcTimeoutMs = Math.max(timeoutSeconds * 1000, 1000);

  socket.addEventListener('message', (event) => {
    const message = JSON.parse(String(event.data));
    if (message.id !== undefined && pending.has(message.id)) {
      pending.get(message.id)(message);
      pending.delete(message.id);
      return;
    }
    if (message.method === 'session/update' && message.params?.update) {
      updates.push(message.params.update);
    }
  });

  await waitForWebSocketOpen(socket, rpcTimeoutMs);

  async function rpc(id, method, params) {
    return new Promise((resolve, reject) => {
      pending.set(id, resolve);
      socket.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
      setTimeout(() => {
        if (!pending.has(id)) {
          return;
        }
        pending.delete(id);
        reject(new Error(`ACP request ${method} timed out`));
      }, rpcTimeoutMs).unref?.();
    });
  }

  return {
    socket,
    updates,
    rpc,
    close() {
      socket.close();
    },
  };
}

export function collectAssistantText(updates) {
  const assistantChunks = (Array.isArray(updates) ? updates : [])
    .filter((update) => update?.sessionUpdate === 'agent_message_chunk')
    .map((update) => update.content);
  const assistantTextCombined = joinACPTextChunks(assistantChunks);
  const assistantTextForDisplay = assistantChunks
    .map((content) => extractACPText(content))
    .filter(Boolean)
    .join('\n');
  return assistantTextCombined || assistantTextForDisplay;
}

export async function withACPWorkspaceClient(options, callback) {
  const {
    namespace,
    workspaceName,
    endpoint,
    timeoutSeconds,
    startPortForward = startACPPortForward,
    connectWebSocket = connectACPWebSocket,
  } = options;
  const portForward = await startPortForward(namespace, workspaceName, endpoint.port);
  let client;
  try {
    client = await connectWebSocket(`ws://127.0.0.1:${portForward.localPort}${endpoint.path}`, timeoutSeconds, options);
    return await callback(client);
  } finally {
    try {
      client?.close();
    } finally {
      await portForward.stop();
    }
  }
}

export async function runACPWorkspacePrompt(options) {
  const {
    namespace,
    workspaceName,
    endpoint,
    timeoutSeconds,
    promptText,
    cwd = '/home/dev',
    mcpServers = [],
    clientInfo = { name: 'spritz-smoke', title: 'Spritz Smoke', version: '1.0.0' },
    settleDelayMs = 750,
    withWorkspaceClient = withACPWorkspaceClient,
  } = options;

  return withWorkspaceClient(
    {
      namespace,
      workspaceName,
      endpoint,
      timeoutSeconds,
      WebSocket: options.WebSocket,
      startPortForward: options.startPortForward,
      connectWebSocket: options.connectWebSocket,
    },
    async (client) => {
      const init = await client.rpc('init-1', 'initialize', {
        protocolVersion: 1,
        clientCapabilities: {},
        clientInfo,
      });
      if (init.error) {
        throw new Error(`ACP initialize failed: ${JSON.stringify(init.error)}`);
      }
      const created = await client.rpc('new-1', 'session/new', { cwd, mcpServers });
      if (created.error || !created.result?.sessionId) {
        throw new Error(`ACP session/new failed: ${JSON.stringify(created.error || created.result)}`);
      }
      const promptResult = await client.rpc('prompt-1', 'session/prompt', {
        sessionId: created.result.sessionId,
        prompt: [{ type: 'text', text: promptText }],
      });
      if (promptResult.error) {
        throw new Error(`ACP session/prompt failed: ${JSON.stringify(promptResult.error)}`);
      }
      if (settleDelayMs > 0) {
        await new Promise((resolve) => setTimeout(resolve, settleDelayMs));
      }
      return {
        sessionId: created.result.sessionId,
        stopReason: promptResult.result?.stopReason || '',
        assistantText: collectAssistantText(client.updates),
        updates: [...client.updates],
      };
    },
  );
}
