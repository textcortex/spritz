import assert from 'node:assert/strict';
import test from 'node:test';

import { summarizeWorkspaceFailure, waitForWorkspace } from './workspace-waiter.mjs';

test('summarizeWorkspaceFailure prioritizes shared mount init failures', () => {
  const result = summarizeWorkspaceFailure({
    spritz: { status: { phase: 'Provisioning', message: 'waiting for deployment' } },
    podList: {
      items: [
        {
          status: {
            initContainerStatuses: [
              {
                name: 'shared-mounts-init',
                state: {
                  waiting: {
                    reason: 'CrashLoopBackOff',
                    message: 'timed out talking to spritz-api',
                  },
                },
              },
            ],
          },
        },
      ],
    },
  });

  assert.deepEqual(result, {
    stage: 'shared-mount-init',
    message: 'timed out talking to spritz-api',
  });
});

test('summarizeWorkspaceFailure reports image pull failures distinctly', () => {
  const result = summarizeWorkspaceFailure({
    spritz: { status: { phase: 'Provisioning', message: 'waiting for deployment' } },
    podList: {
      items: [
        {
          status: {
            containerStatuses: [
              {
                name: 'spritz',
                state: {
                  waiting: {
                    reason: 'ImagePullBackOff',
                    message: 'image not found',
                  },
                },
              },
            ],
          },
        },
      ],
    },
  });

  assert.deepEqual(result, {
    stage: 'image-pull',
    message: 'image not found',
  });
});

test('waitForWorkspace returns the ready workspace payload and discovered ACP endpoint', async () => {
  let callCount = 0;
  const readySpritz = {
    status: {
      phase: 'Ready',
      acp: {
        state: 'ready',
        endpoint: {
          port: 9321,
          path: '/rpc',
        },
      },
    },
  };

  const result = await waitForWorkspace({
    namespace: 'example-ns',
    name: 'example-workspace',
    timeoutSeconds: 1,
    pollSeconds: 0.001,
    kubectlGetJSON: async (args) => {
      callCount += 1;
      if (callCount <= 2) {
        if (args.includes('spritz')) {
          return { status: { phase: 'Provisioning', message: 'still starting' } };
        }
        return { items: [] };
      }
      if (args.includes('spritz')) {
        return readySpritz;
      }
      return { items: [{ metadata: { name: 'example-workspace-pod' } }] };
    },
  });

  assert.equal(result.spritz, readySpritz);
  assert.deepEqual(result.acpEndpoint, { port: 9321, path: '/rpc' });
  assert.equal(result.failureSummary, null);
});

test('waitForWorkspace recovers from transient kubectl polling errors', async () => {
  let failed = false;

  const result = await waitForWorkspace({
    namespace: 'example-ns',
    name: 'example-workspace',
    timeoutSeconds: 1,
    pollSeconds: 0.001,
    kubectlGetJSON: async (args) => {
      if (!failed) {
        failed = true;
        throw new Error('temporary apiserver timeout');
      }
      if (args.includes('spritz')) {
        return { status: { phase: 'Ready', acp: { state: 'ready' } } };
      }
      return { items: [] };
    },
  });

  assert.equal(result.spritz.status.phase, 'Ready');
  assert.deepEqual(result.acpEndpoint, { port: 2529, path: '/' });
});

test('waitForWorkspace fails with the last staged failure summary on timeout', async () => {
  await assert.rejects(
    () => waitForWorkspace({
      namespace: 'example-ns',
      name: 'example-workspace',
      timeoutSeconds: 0.01,
      pollSeconds: 0.001,
      kubectlGetJSON: async (args) => {
        if (args.includes('spritz')) {
          return { status: { phase: 'Provisioning', message: 'waiting for deployment' } };
        }
        return {
          items: [
            {
              status: {
                initContainerStatuses: [
                  {
                    name: 'shared-mounts-init',
                    state: {
                      waiting: {
                        reason: 'CrashLoopBackOff',
                        message: 'timed out talking to spritz-api',
                      },
                    },
                  },
                ],
              },
            },
          ],
        };
      },
    }),
    /shared-mount-init: timed out talking to spritz-api/,
  );
});
