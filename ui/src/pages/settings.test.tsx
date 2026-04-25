import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
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
      const putCall = requestMock.mock.calls.find(
        ([, options]) => (options as RequestInit | undefined)?.method === 'PUT',
      );
      expect(putCall?.[0]).toBe('/api/settings/channels/installations/chinst_test/connections/chconn_test');
      const payload = JSON.parse(String((putCall?.[1] as RequestInit).body));
      expect(payload).toEqual({
        channelPolicies: [
          {
            externalChannelId: 'C1234567890',
            externalChannelType: 'channel',
            requireMention: true,
          },
        ],
      });
    });
  });

  it('preserves existing route channel types when saving', async () => {
    const user = userEvent.setup();
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
              routes: [
                {
                  id: 'route_dm',
                  externalChannelId: 'D12345678',
                  externalChannelType: 'im',
                  requireMention: true,
                  enabled: true,
                },
                {
                  id: 'route_legacy',
                  externalChannelId: 'C_LEGACY',
                  requireMention: false,
                  enabled: true,
                },
              ],
            },
          ],
        },
        connection: {
          id: 'chconn_test',
          displayName: 'zeno',
          isDefault: true,
          status: 'ready',
          routes: [
            {
              id: 'route_dm',
              externalChannelId: 'D12345678',
              externalChannelType: 'im',
              requireMention: true,
              enabled: true,
            },
            {
              id: 'route_legacy',
              externalChannelId: 'C_LEGACY',
              requireMention: false,
              enabled: true,
            },
          ],
        },
        path,
      });
    });

    render(
      <MemoryRouter initialEntries={['/slack/channels/installations/chinst_test/connections/chconn_test']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    await screen.findByText('D12345678');
    await user.click(screen.getByRole('button', { name: 'Disable mention' }));

    await waitFor(() => {
      const putCall = requestMock.mock.calls.find(
        ([, options]) => (options as RequestInit | undefined)?.method === 'PUT',
      );
      expect(putCall).toBeDefined();
      const payload = JSON.parse(String((putCall?.[1] as RequestInit).body));
      expect(payload.channelPolicies).toEqual([
        {
          externalChannelId: 'D12345678',
          externalChannelType: 'im',
          requireMention: false,
        },
        {
          externalChannelId: 'C_LEGACY',
          requireMention: false,
        },
      ]);
    });
  });

  it('shows an empty state when installs have no channel connections', async () => {
    requestMock.mockResolvedValue({
      status: 'ok',
      installations: [
        {
          id: 'chinst_without_connections',
          state: 'ready',
          route: {
            provider: 'slack',
            principalId: 'shared-slack-app',
            externalScopeType: 'workspace',
            externalTenantId: 'T_workspace',
          },
          allowedActions: ['manage_channels'],
          connections: [],
        },
      ],
    });

    render(
      <MemoryRouter initialEntries={['/slack/channels']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    expect(
      await screen.findByText('No channel connections are available for these Slack workspace installs.'),
    ).toBeTruthy();
  });

  it('redirects the Slack settings landing page to workspace settings', async () => {
    render(
      <MemoryRouter initialEntries={['/settings/slack']}>
        <Routes>
          <Route path="settings/*" element={<SettingsPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: 'Slack Workspaces' })).toBeTruthy();
    expect(await screen.findByText('No Slack workspaces are installed.')).toBeTruthy();
  });

  it('routes typed install picker failures to the install result page', async () => {
    const user = userEvent.setup();
    const installResult = {
      status: 'error',
      code: 'identity.unresolved',
      operation: 'channel.install',
      provider: 'slack',
      requestId: 'install-request-1',
      teamId: 'T_workspace',
      title: 'Install could not be linked',
      message: 'Link the expected account, then start the install again.',
      retryable: true,
      actionLabel: 'Start install again',
      actionHref: '/slack-gateway/slack/install',
    };
    requestMock.mockImplementation((path: string, options?: RequestInit) => {
      if (path === '/api/slack/install/selection' && options?.method !== 'POST') {
        return Promise.resolve({
          status: 'resolved',
          requestId: 'install-request-1',
          teamId: 'T_workspace',
          targets: [
            {
              id: 'ag_workspace',
              profile: { name: 'Workspace Helper' },
              presetInputs: { agentId: 'ag_workspace' },
            },
          ],
        });
      }
      if (path === '/api/slack/install/selection' && options?.method === 'POST') {
        return Promise.resolve(installResult);
      }
      if (path.startsWith('/api/slack/install/result?')) {
        return Promise.resolve(installResult);
      }
      return Promise.reject(new Error(`unexpected request: ${path}`));
    });

    render(
      <MemoryRouter initialEntries={['/settings/slack/install/select']}>
        <Routes>
          <Route path="settings/*" element={<SettingsPage />} />
        </Routes>
      </MemoryRouter>,
    );

    await screen.findByText('Workspace Helper');
    await user.click(screen.getByRole('button', { name: /Continue/i }));

    expect(await screen.findByText('Install could not be linked')).toBeTruthy();
    expect(await screen.findByText('identity.unresolved')).toBeTruthy();
    expect(await screen.findByText('Request ID: install-request-1')).toBeTruthy();
  });
});
