#!/usr/bin/env node

import process from 'node:process';
import { spawn } from 'node:child_process';
import net from 'node:net';

import {
  buildIdempotencyKey,
  buildSmokeToken,
  extractACPText,
  findFreePort,
  isForbiddenFailure,
  joinACPTextChunks,
  parsePresetList,
  resolveSpzCommand,
  runCommand,
  summarizeWorkspaceFailure,
} from './acp-smoke-lib.mjs';

const defaultPromptTemplate = 'Reply with the exact token {{token}} and nothing else.';
const defaultPresets = ['openclaw', 'claude-code'];
const defaultTimeoutSeconds = 300;
const defaultReadyPollSeconds = 5;
const defaultNamespace = process.env.SPRITZ_NAMESPACE || process.env.SPRITZ_SMOKE_NAMESPACE || '';

function parseArgs(argv) {
  const values = {
    ownerId: process.env.SPRITZ_SMOKE_OWNER_ID || '',
    namespace: defaultNamespace,
    presets: [...defaultPresets],
    timeoutSeconds: defaultTimeoutSeconds,
    keep: false,
    promptTemplate: process.env.SPRITZ_SMOKE_PROMPT || defaultPromptTemplate,
    idempotencyPrefix: process.env.SPRITZ_SMOKE_IDEMPOTENCY_PREFIX || `spritz-smoke-${Date.now()}`,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    const next = argv[index + 1];
    if (arg === '--owner-id' && next) {
      values.ownerId = next;
      index += 1;
      continue;
    }
    if (arg === '--namespace' && next) {
      values.namespace = next;
      index += 1;
      continue;
    }
    if (arg === '--presets' && next) {
      values.presets = parsePresetList(next);
      index += 1;
      continue;
    }
    if (arg === '--timeout-seconds' && next) {
      values.timeoutSeconds = Number.parseInt(next, 10);
      index += 1;
      continue;
    }
    if (arg === '--prompt' && next) {
      values.promptTemplate = next;
      index += 1;
      continue;
    }
    if (arg === '--idempotency-prefix' && next) {
      values.idempotencyPrefix = next;
      index += 1;
      continue;
    }
    if (arg === '--keep') {
      values.keep = true;
      continue;
    }
    if (arg === '--help') {
      printUsage(0);
    }
    throw new Error(`unknown argument: ${arg}`);
  }

  if (!values.ownerId.trim()) {
    throw new Error('--owner-id is required');
  }
  if (!values.presets.length) {
    throw new Error('at least one preset is required');
  }
  if (!Number.isFinite(values.timeoutSeconds) || values.timeoutSeconds <= 0) {
    throw new Error('--timeout-seconds must be a positive integer');
  }

  return values;
}

function printUsage(code) {
  const lines = [
    'Usage: node e2e/acp-smoke.mjs --owner-id <id> [options]',
    '',
    'Options:',
    '  --namespace <ns>         Override the target namespace (defaults to spz profile or env)',
    '  --presets <a,b>          Comma-separated preset ids to test (default: openclaw,claude-code)',
    '  --timeout-seconds <n>    Timeout per workspace readiness/prompt cycle (default: 300)',
    '  --prompt <template>      Prompt template, use {{token}} placeholder for the expected token',
    '  --idempotency-prefix <s> Prefix used to derive idempotency keys for smoke creates',
    '  --keep                   Keep created workspaces instead of deleting them',
    '  --help                   Show this message',
  ];
  console.error(lines.join('\n'));
  process.exit(code);
}

async function hasCommandOnPath(command) {
  const result = await runCommand('sh', ['-lc', `command -v ${command} >/dev/null 2>&1`]);
  return result.code === 0;
}

function buildSpzEnvironment(namespace) {
  const env = { ...process.env };
  if (process.env.SPRITZ_SMOKE_API_URL) env.SPRITZ_API_URL = process.env.SPRITZ_SMOKE_API_URL;
  if (process.env.SPRITZ_SMOKE_BEARER_TOKEN) env.SPRITZ_BEARER_TOKEN = process.env.SPRITZ_SMOKE_BEARER_TOKEN;
  if (namespace) env.SPRITZ_NAMESPACE = namespace;
  return env;
}

async function runSpz(spzCommand, subcommandArgs, options = {}) {
  const result = await runCommand(spzCommand.command, [...spzCommand.args, ...subcommandArgs], {
    env: options.env,
    cwd: options.cwd,
  });
  return result;
}

function parseJSONOutput(result, context) {
  try {
    return JSON.parse(result.stdout);
  } catch (error) {
    throw new Error(`${context} returned non-JSON output: ${error.message}\nstdout:\n${result.stdout}\nstderr:\n${result.stderr}`);
  }
}

async function kubectlJSON(args) {
  const result = await runCommand('kubectl', args);
  if (result.code !== 0) {
    throw new Error(`kubectl ${args.join(' ')} failed:\n${result.stderr || result.stdout}`);
  }
  try {
    return JSON.parse(result.stdout);
  } catch (error) {
    throw new Error(`kubectl ${args.join(' ')} returned invalid JSON: ${error.message}`);
  }
}

async function waitForWorkspace(namespace, name, timeoutSeconds) {
  const deadline = Date.now() + timeoutSeconds * 1000;
  let lastFailure = { stage: 'create', message: 'workspace not observed yet' };

  while (Date.now() < deadline) {
    const spritz = await kubectlJSON(['-n', namespace, 'get', 'spritz', name, '-o', 'json']);
    const podList = await kubectlJSON(['-n', namespace, 'get', 'pods', '-l', `spritz.sh/name=${name}`, '-o', 'json']);
    if (spritz?.status?.phase === 'Ready' && spritz?.status?.acp?.state === 'ready') {
      return { spritz, podList };
    }
    lastFailure = summarizeWorkspaceFailure({ spritz, podList });
    await new Promise((resolve) => setTimeout(resolve, defaultReadyPollSeconds * 1000));
  }

  throw new Error(`workspace ${name} did not become usable within ${timeoutSeconds}s (${lastFailure.stage}: ${lastFailure.message})`);
}

async function waitForLocalPort(port, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const reachable = await new Promise((resolve) => {
      const socket = net.connect({ host: '127.0.0.1', port });
      socket.once('connect', () => {
        socket.destroy();
        resolve(true);
      });
      socket.once('error', () => {
        resolve(false);
      });
    });
    if (reachable) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`local port ${port} did not become reachable in time`);
}

async function startPortForward(namespace, serviceName, targetPort) {
  const localPort = await findFreePort();
  const child = spawn('kubectl', ['-n', namespace, 'port-forward', `service/${serviceName}`, `${localPort}:${targetPort}`], {
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stderr = '';
  let stdout = '';
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

async function connectACP(localPort, timeoutSeconds) {
  const socket = new WebSocket(`ws://127.0.0.1:${localPort}/`);
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
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', (event) => reject(new Error(`ACP websocket failed: ${event.message || 'unknown error'}`)), {
      once: true,
    });
  });

  async function rpc(id, method, params) {
    return new Promise((resolve, reject) => {
      pending.set(id, resolve);
      socket.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
      setTimeout(() => {
        if (pending.has(id)) {
          pending.delete(id);
          reject(new Error(`ACP request ${method} timed out`));
        }
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

function buildPromptText(template, token) {
  return String(template || defaultPromptTemplate).replaceAll('{{token}}', token);
}

async function promptWorkspace(namespace, name, presetId, promptTemplate, timeoutSeconds) {
  const token = buildSmokeToken(presetId);
  const portForward = await startPortForward(namespace, name, 2529);
  let client;
  try {
    client = await connectACP(portForward.localPort, timeoutSeconds);
    const init = await client.rpc('init-1', 'initialize', {
      protocolVersion: 1,
      clientCapabilities: {},
      clientInfo: { name: 'spritz-smoke', title: 'Spritz Smoke', version: '1.0.0' },
    });
    if (init.error) {
      throw new Error(`ACP initialize failed: ${JSON.stringify(init.error)}`);
    }
    const created = await client.rpc('new-1', 'session/new', { cwd: '/home/dev', mcpServers: [] });
    if (created.error || !created.result?.sessionId) {
      throw new Error(`ACP session/new failed: ${JSON.stringify(created.error || created.result)}`);
    }
    const promptResult = await client.rpc('prompt-1', 'session/prompt', {
      sessionId: created.result.sessionId,
      prompt: [{ type: 'text', text: buildPromptText(promptTemplate, token) }],
    });
    if (promptResult.error) {
      throw new Error(`ACP session/prompt failed: ${JSON.stringify(promptResult.error)}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 750));
    const assistantChunks = client.updates
      .filter((update) => update?.sessionUpdate === 'agent_message_chunk')
      .map((update) => update.content);
    const assistantTextCombined = joinACPTextChunks(assistantChunks);
    const assistantTextForDisplay = assistantChunks
      .map((content) => extractACPText(content))
      .filter(Boolean)
      .join('\n');
    const assistantText = assistantTextCombined || assistantTextForDisplay;
    if (!assistantText.trim()) {
      throw new Error(`ACP prompt completed without assistant text (stopReason=${promptResult.result?.stopReason || 'unknown'})`);
    }
    if (!assistantTextCombined.includes(token)) {
      throw new Error(`assistant reply did not include smoke token ${token}:\n${assistantText}`);
    }
    return {
      sessionId: created.result.sessionId,
      stopReason: promptResult.result?.stopReason || '',
      assistantText,
    };
  } finally {
    try {
      client?.close();
    } finally {
      await portForward.stop();
    }
  }
}

async function ensureProvisionerDeny(spzCommand, env, namespace, createdName) {
  const listResult = await runSpz(spzCommand, ['list', '--namespace', namespace], { env });
  if (listResult.code === 0 || !isForbiddenFailure(listResult)) {
    throw new Error(`service principal list should fail with forbidden, got:\n${listResult.stderr || listResult.stdout}`);
  }
  const deleteResult = await runSpz(spzCommand, ['delete', createdName, '--namespace', namespace], { env });
  if (deleteResult.code === 0 || !isForbiddenFailure(deleteResult)) {
    throw new Error(`service principal delete should fail with forbidden for ${createdName}, got:\n${deleteResult.stderr || deleteResult.stdout}`);
  }
}

async function createWorkspace(spzCommand, env, options) {
  const args = [
    'create',
    '--preset', options.presetId,
    '--owner-id', options.ownerId,
    '--idle-ttl', '24h',
    '--ttl', '168h',
    '--idempotency-key', options.idempotencyKey,
    '--source', 'smoke',
    '--request-id', options.idempotencyKey,
  ];
  if (options.namespace) {
    args.push('--namespace', options.namespace);
  }
  const result = await runSpz(spzCommand, args, { env });
  if (result.code !== 0) {
    throw new Error(`spz create failed for ${options.presetId}:\n${result.stderr || result.stdout}`);
  }
  return parseJSONOutput(result, `spz create (${options.presetId})`);
}

async function createWorkspaceMismatch(spzCommand, env, options) {
  const args = [
    'create',
    '--preset', options.presetId,
    '--owner-id', options.ownerId,
    '--idle-ttl', '24h',
    '--ttl', '168h',
    '--idempotency-key', options.idempotencyKey,
    '--source', 'smoke',
    '--request-id', `${options.idempotencyKey}-mismatch`,
  ];
  if (options.namespace) {
    args.push('--namespace', options.namespace);
  }
  return runSpz(spzCommand, args, { env });
}

function assertCreateResponse(response, ownerId, presetId) {
  const spritzName = response?.spritz?.metadata?.name;
  if (!spritzName) {
    throw new Error(`create response missing spritz metadata.name: ${JSON.stringify(response, null, 2)}`);
  }
  if (response.ownerId !== ownerId) {
    throw new Error(`expected ownerId ${ownerId}, got ${response.ownerId || '<empty>'}`);
  }
  if (response.actorType !== 'service') {
    throw new Error(`expected actorType service, got ${response.actorType || '<empty>'}`);
  }
  if (!response.chatUrl || !response.workspaceUrl || !response.accessUrl) {
    throw new Error(`create response missing canonical URLs: ${JSON.stringify(response, null, 2)}`);
  }
  if (response.presetId !== presetId) {
    throw new Error(`expected presetId ${presetId}, got ${response.presetId || '<empty>'}`);
  }
  return spritzName;
}

async function cleanupWorkspace(namespace, name) {
  const result = await runCommand('kubectl', ['-n', namespace, 'delete', 'spritz', name, '--ignore-not-found=true', '--wait=false']);
  if (result.code !== 0) {
    throw new Error(`failed to delete ${name}:\n${result.stderr || result.stdout}`);
  }
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  const hasSpzOnPath = await hasCommandOnPath('spz');
  const spzCommand = resolveSpzCommand(process.env, { hasSpzOnPath });
  const env = buildSpzEnvironment(options.namespace);
  const createdWorkspaces = [];

  try {
    for (let index = 0; index < options.presets.length; index += 1) {
      const presetId = options.presets[index];
      const idempotencyKey = buildIdempotencyKey(options.idempotencyPrefix, presetId);
      const createResponse = await createWorkspace(spzCommand, env, {
        presetId,
        ownerId: options.ownerId,
        namespace: options.namespace,
        idempotencyKey,
      });
      const workspaceName = assertCreateResponse(createResponse, options.ownerId, presetId);
      const namespace = createResponse.namespace || options.namespace;
      if (!namespace) {
        throw new Error(`create response for ${workspaceName} did not include a namespace and no namespace was configured`);
      }
      createdWorkspaces.push({ namespace, name: workspaceName });

      const replayResponse = await createWorkspace(spzCommand, env, {
        presetId,
        ownerId: options.ownerId,
        namespace,
        idempotencyKey,
      });
      if (replayResponse?.spritz?.metadata?.name !== workspaceName || replayResponse?.replayed !== true) {
        throw new Error(`idempotent replay failed for ${presetId}: ${JSON.stringify(replayResponse, null, 2)}`);
      }

      if (index === 0) {
        await ensureProvisionerDeny(spzCommand, env, namespace, workspaceName);
      }

      if (index === 0 && options.presets.length > 1) {
        const mismatchPreset = options.presets.find((value) => value !== presetId);
        if (mismatchPreset) {
          const mismatch = await createWorkspaceMismatch(spzCommand, env, {
            presetId: mismatchPreset,
            ownerId: options.ownerId,
            namespace,
            idempotencyKey,
          });
          if (mismatch.code === 0) {
            throw new Error(`idempotency mismatch should fail, but ${mismatchPreset} create succeeded`);
          }
          const mismatchOutput = `${mismatch.stderr}\n${mismatch.stdout}`;
          if (!/idempotencyKey already used with a different request/i.test(mismatchOutput)) {
            throw new Error(`unexpected idempotency mismatch output:\n${mismatchOutput}`);
          }
        }
      }

      await waitForWorkspace(namespace, workspaceName, options.timeoutSeconds);
      const acpResult = await promptWorkspace(namespace, workspaceName, presetId, options.promptTemplate, options.timeoutSeconds);
      console.log(JSON.stringify({
        presetId,
        workspaceName,
        namespace,
        chatUrl: createResponse.chatUrl,
        workspaceUrl: createResponse.workspaceUrl,
        stopReason: acpResult.stopReason,
        assistantText: acpResult.assistantText,
      }, null, 2));
    }
  } finally {
    if (!options.keep) {
      for (const workspace of createdWorkspaces.reverse()) {
        try {
          await cleanupWorkspace(workspace.namespace, workspace.name);
        } catch (error) {
          console.error(`[cleanup] ${error.message}`);
        }
      }
    }
  }
}

main().catch((error) => {
  console.error(error.message || error);
  process.exit(1);
});
