import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { SettingsPage } from './settings';

const requestMock = vi.hoisted(() => vi.fn());

vi.mock('@/lib/slack-management', async () => {
  const actual = await vi.importActual<typeof import('@/lib/slack-management')>(
    '@/lib/slack-management',
  );
  return {
    ...actual,
    slackGatewayRequest: (...args: unknown[]) => requestMock(...args),
  };
});

describe('SettingsPage', () => {
  beforeEach(() => {
    requestMock.mockReset();
    requestMock.mockImplementation((path: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        return Promise.resolve({ status: 'ok' });
      }
      return Promise.resolve({
        status: 'ok',
        installation: {
          id: 'chinst_test',
          state: 'ready',
          route: {
            provider: 'slack',
            principalId: 'shared-slack-app',
            externalScopeType: 'workspace',
            externalTenantId: 'T_workspace',
          },
          allowedActions: ['manage_channels'],
          connections: [
            {
              id: 'chconn_test',
              displayName: 'zeno',
              isDefault: true,
              status: 'ready',
              routes: [],
            },
          ],
        },
        connection: {
          id: 'chconn_test',
          displayName: 'zeno',
          isDefault: true,
          status: 'ready',
          routes: [],
        },
        path,
      });
    });
  });

  it('requires bot mentions by default when adding a channel route', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={['/slack/channels/installations/chinst_test/connections/chconn_test']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    const mentionToggle = await screen.findByLabelText('Relay without mention');
    expect((mentionToggle as HTMLInputElement).checked).toBe(false);

    await user.type(screen.getByLabelText('Channel ID'), 'C1234567890');
    await user.click(screen.getByRole('button', { name: /Save/i }));

    await waitFor(() => {
      expect(requestMock).toHaveBeenCalledWith(
        '/api/settings/channels/installations/chinst_test/connections/chconn_test',
        expect.objectContaining({
          method: 'PUT',
          body: JSON.stringify({
            channelPolicies: [
              {
                externalChannelId: 'C1234567890',
                externalChannelType: 'channel',
                requireMention: true,
              },
            ],
          }),
        }),
      );
    });
  });
});
