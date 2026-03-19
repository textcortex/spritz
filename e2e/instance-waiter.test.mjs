import assert from 'node:assert/strict';
import test from 'node:test';

import { summarizeInstanceFailure, waitForInstance } from './instance-waiter.mjs';

test('summarizeInstanceFailure prioritizes shared mount init failures', () => {
  const result = summarizeInstanceFailure({
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

test('summarizeInstanceFailure reports image pull failures distinctly', () => {
  const result = summarizeInstanceFailure({
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

test('waitForInstance returns the ready instance payload and discovered ACP endpoint', async () => {
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

  const result = await waitForInstance({
    namespace: 'example-ns',
    name: 'example-instance',
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
      return { items: [{ metadata: { name: 'example-instance-pod' } }] };
    },
  });

  assert.equal(result.spritz, readySpritz);
  assert.deepEqual(result.acpEndpoint, { port: 9321, path: '/rpc' });
  assert.equal(result.failureSummary, null);
});

test('waitForInstance recovers from transient kubectl polling errors', async () => {
  let failed = false;

  const result = await waitForInstance({
    namespace: 'example-ns',
    name: 'example-instance',
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

test('waitForInstance fails with the last staged failure summary on timeout', async () => {
  await assert.rejects(
    () => waitForInstance({
      namespace: 'example-ns',
      name: 'example-instance',
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
