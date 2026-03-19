import { resolveACPEndpoint, runCommand } from './acp-smoke-lib.mjs';

const defaultReadyPollSeconds = 5;

export async function kubectlJSON(args, options = {}) {
  const result = await runCommand('kubectl', args, { timeoutMs: options.timeoutMs });
  if (result.timedOut) {
    throw new Error(`kubectl ${args.join(' ')} timed out after ${options.timeoutMs}ms`);
  }
  if (result.code !== 0) {
    throw new Error(`kubectl ${args.join(' ')} failed:\n${result.stderr || result.stdout}`);
  }
  try {
    return JSON.parse(result.stdout);
  } catch (error) {
    throw new Error(`kubectl ${args.join(' ')} returned invalid JSON: ${error.message}`);
  }
}

export function summarizeInstanceFailure({ spritz, podList }) {
  const status = spritz?.status || {};
  const pod = Array.isArray(podList?.items) ? podList.items[0] : null;
  if (!pod) {
    return {
      stage: 'create',
      message: status.message || 'instance pod not created',
    };
  }

  const initStatuses = Array.isArray(pod.status?.initContainerStatuses) ? pod.status.initContainerStatuses : [];
  for (const container of initStatuses) {
    const waiting = container?.state?.waiting;
    if (!waiting) continue;
    if (container.name === 'shared-mounts-init') {
      return {
        stage: 'shared-mount-init',
        message: waiting.message || waiting.reason || 'shared mount init is blocked',
      };
    }
    return {
      stage: 'init',
      message: waiting.message || waiting.reason || `${container.name} init container is blocked`,
    };
  }

  const statuses = Array.isArray(pod.status?.containerStatuses) ? pod.status.containerStatuses : [];
  for (const container of statuses) {
    const waiting = container?.state?.waiting;
    if (!waiting) continue;
    if (waiting.reason === 'ImagePullBackOff' || waiting.reason === 'ErrImagePull') {
      return {
        stage: 'image-pull',
        message: waiting.message || waiting.reason,
      };
    }
    return {
      stage: 'startup',
      message: waiting.message || waiting.reason || `${container.name} is waiting`,
    };
  }

  const terminated = statuses.find((container) => container?.state?.terminated);
  if (terminated?.state?.terminated) {
    return {
      stage: 'startup',
      message:
        terminated.state.terminated.message ||
        terminated.state.terminated.reason ||
        `${terminated.name} terminated unexpectedly`,
    };
  }

  if (status.phase && status.phase !== 'Ready') {
    return {
      stage: 'readiness',
      message: status.message || `instance phase is ${status.phase}`,
    };
  }

  if (status.acp?.state && status.acp.state !== 'ready') {
    return {
      stage: 'acp',
      message: status.message || `ACP state is ${status.acp.state}`,
    };
  }

  return {
    stage: 'unknown',
    message: status.message || 'instance did not become ready',
  };
}

export async function waitForInstance(options) {
  const {
    namespace,
    name,
    timeoutSeconds,
    pollSeconds = defaultReadyPollSeconds,
    kubectlGetJSON = kubectlJSON,
  } = options;
  const deadline = Date.now() + timeoutSeconds * 1000;
  let lastFailure = { stage: 'create', message: 'instance not observed yet' };

  while (Date.now() < deadline) {
    const remainingMs = Math.max(deadline - Date.now(), 1000);
    const kubectlTimeoutMs = Math.min(remainingMs, pollSeconds * 1000);
    let spritz;
    let podList;
    try {
      spritz = await kubectlGetJSON(['-n', namespace, 'get', 'spritz', name, '-o', 'json'], { timeoutMs: kubectlTimeoutMs });
      podList = await kubectlGetJSON(['-n', namespace, 'get', 'pods', '-l', `spritz.sh/name=${name}`, '-o', 'json'], { timeoutMs: kubectlTimeoutMs });
    } catch (error) {
      lastFailure = {
        stage: 'readiness',
        message: error.message || 'kubectl polling failed',
      };
      await new Promise((resolve) => setTimeout(resolve, pollSeconds * 1000));
      continue;
    }
    if (spritz?.status?.phase === 'Ready' && spritz?.status?.acp?.state === 'ready') {
      return {
        spritz,
        podList,
        acpEndpoint: resolveACPEndpoint(spritz),
        failureSummary: null,
      };
    }
    lastFailure = summarizeInstanceFailure({ spritz, podList });
    await new Promise((resolve) => setTimeout(resolve, pollSeconds * 1000));
  }

  throw new Error(`instance ${name} did not become usable within ${timeoutSeconds}s (${lastFailure.stage}: ${lastFailure.message})`);
}
