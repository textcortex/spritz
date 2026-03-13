#!/usr/bin/env node

import process from 'node:process';

import {
  assertSmokeCreateResponse,
  buildSmokeSpzEnvironment,
  buildIdempotencyKey,
  buildSmokeToken,
  isForbiddenFailure,
  parseSmokeArgs,
  resolveSpzCommand,
  runCommand,
} from './acp-smoke-lib.mjs';
import { runACPWorkspacePrompt } from './acp-client.mjs';
import { waitForWorkspace } from './workspace-waiter.mjs';

const defaultPromptTemplate = 'Reply with the exact token {{token}} and nothing else.';

function printUsage(code) {
  const lines = [
    'Usage: node e2e/acp-smoke.mjs --owner-id <id> [options]',
    '',
    'Options:',
    '  --namespace <ns>         Override the target namespace (defaults to spz profile or env)',
    '  --presets <a,b>          Comma-separated preset ids to test (required; no built-in defaults)',
    '  --timeout-seconds <n>    Timeout per workspace readiness/prompt cycle (default: 300)',
    '  --prompt <template>      Prompt template, use {{token}} placeholder for the expected token',
    '  --idempotency-prefix <s> Prefix used to derive idempotency keys for smoke creates',
    '  --keep                   Keep created workspaces instead of deleting them',
    '  --help                   Show this message',
    '',
    'Environment:',
    '  SPRITZ_SMOKE_API_URL       Required API base for the service-principal smoke client',
    '  SPRITZ_SMOKE_BEARER_TOKEN  Required bearer token for the service-principal smoke client',
  ];
  console.error(lines.join('\n'));
  process.exit(code);
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

function buildPromptText(template, token) {
  return String(template || defaultPromptTemplate).replaceAll('{{token}}', token);
}

function emitSmokeResult(result) {
  process.stdout.write(`${JSON.stringify(result)}\n`);
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

async function cleanupWorkspace(namespace, name) {
  const result = await runCommand('kubectl', ['-n', namespace, 'delete', 'spritz', name, '--ignore-not-found=true', '--wait=false']);
  if (result.code !== 0) {
    throw new Error(`failed to delete ${name}:\n${result.stderr || result.stdout}`);
  }
}

async function main() {
  const parsed = parseSmokeArgs(process.argv.slice(2));
  if (parsed.help) {
    printUsage(0);
  }
  const options = parsed.values;
  const spzCommand = resolveSpzCommand(process.env);
  const env = buildSmokeSpzEnvironment(process.env, {
    apiUrl: options.apiUrl,
    bearerToken: options.bearerToken,
    namespace: options.namespace,
  });
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
      const workspaceName = assertSmokeCreateResponse(createResponse, options.ownerId, presetId);
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

      const workspaceState = await waitForWorkspace({
        namespace,
        name: workspaceName,
        timeoutSeconds: options.timeoutSeconds,
      });
      const acpEndpoint = workspaceState.acpEndpoint;
      const token = buildSmokeToken(presetId);
      const acpResult = await runACPWorkspacePrompt({
        namespace,
        workspaceName,
        endpoint: acpEndpoint,
        timeoutSeconds: options.timeoutSeconds,
        promptText: buildPromptText(options.promptTemplate, token),
      });
      if (!acpResult.assistantText.trim()) {
        throw new Error(`ACP prompt completed without assistant text (stopReason=${acpResult.stopReason || 'unknown'})`);
      }
      if (!acpResult.assistantText.includes(token)) {
        throw new Error(`assistant reply did not include smoke token ${token}:\n${acpResult.assistantText}`);
      }
      emitSmokeResult({
        presetId,
        workspaceName,
        namespace,
        chatUrl: createResponse.chatUrl,
        workspaceUrl: createResponse.workspaceUrl,
        acpEndpoint,
        stopReason: acpResult.stopReason,
        assistantText: acpResult.assistantText,
      });
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
