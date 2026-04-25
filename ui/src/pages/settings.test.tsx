import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom';
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

  it('asks for confirmation before removing a channel route', async () => {
    const user = userEvent.setup();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
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
                  id: 'chroute_existing',
                  externalChannelId: 'C_EXISTING',
                  externalChannelType: 'channel',
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
              id: 'chroute_existing',
              externalChannelId: 'C_EXISTING',
              externalChannelType: 'channel',
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

    await screen.findByText('C_EXISTING');
    await user.click(screen.getByRole('button', { name: 'Remove' }));
    expect(confirmSpy).toHaveBeenCalledWith('Remove channel route C_EXISTING?');
    expect(
      requestMock.mock.calls.some(([, options]) => (options as RequestInit | undefined)?.method === 'PUT'),
    ).toBe(false);

    confirmSpy.mockReturnValue(true);
    await user.click(screen.getByRole('button', { name: 'Remove' }));

    await waitFor(() => {
      const putCall = requestMock.mock.calls.find(
        ([, options]) => (options as RequestInit | undefined)?.method === 'PUT',
      );
      expect(putCall).toBeDefined();
      const payload = JSON.parse(String((putCall?.[1] as RequestInit).body));
      expect(payload).toEqual({ channelPolicies: [] });
    });
    confirmSpy.mockRestore();
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

  it('shows legacy channel policies as read-only when installs have no connections', async () => {
    requestMock.mockResolvedValue({
      status: 'ok',
      installations: [
        {
          id: 'chinst_legacy',
          state: 'ready',
          route: {
            provider: 'slack',
            principalId: 'shared-slack-app',
            externalScopeType: 'workspace',
            externalTenantId: 'T_workspace',
          },
          installationConfig: {
            channelPolicies: [
              { externalChannelId: 'C_LEGACY', externalChannelType: 'channel', requireMention: false },
            ],
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

    expect(await screen.findByText('Legacy channel policies')).toBeTruthy();
    expect(await screen.findByText('C_LEGACY')).toBeTruthy();
    expect(await screen.findByText('Settings unavailable')).toBeTruthy();
    expect(screen.queryByRole('link', { name: /Legacy channel policies/i })).toBeNull();
  });

  it('shows legacy channel policies on an installation detail page', async () => {
    requestMock.mockResolvedValue({
      status: 'ok',
      installation: {
        id: 'chinst_legacy',
        state: 'ready',
        route: {
          provider: 'slack',
          principalId: 'shared-slack-app',
          externalScopeType: 'workspace',
          externalTenantId: 'T_workspace',
        },
        installationConfig: {
          channelPolicies: [
            { externalChannelId: 'C_LEGACY', externalChannelType: 'channel', requireMention: false },
          ],
        },
        allowedActions: ['manage_channels'],
        connections: [],
      },
    });

    render(
      <MemoryRouter initialEntries={['/slack/channels/installations/chinst_legacy']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('Legacy channel policies')).toBeTruthy();
    expect(await screen.findByText('C_LEGACY')).toBeTruthy();
    expect(await screen.findByText('Settings unavailable')).toBeTruthy();
  });

  it('shows connection identity in channel routing rows', async () => {
    requestMock.mockResolvedValue({
      status: 'ok',
      installations: [
        {
          id: 'chinst_multi',
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
              id: 'chconn_default',
              displayName: 'Default connection',
              isDefault: true,
              state: 'ready',
              routes: [],
            },
            {
              id: 'chconn_zeno',
              displayName: 'Zeno',
              isDefault: false,
              state: 'ready',
              routes: [{ externalChannelId: 'C123', requireMention: false }],
            },
          ],
        },
      ],
    });

    render(
      <MemoryRouter initialEntries={['/slack/channels']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('Default connection')).toBeTruthy();
    expect(await screen.findByText('Zeno')).toBeTruthy();
    expect(await screen.findByText('1 configured channel')).toBeTruthy();
  });

  it('clears previous channel settings when a later route load fails', async () => {
    const user = userEvent.setup();
    requestMock.mockImplementation((path: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        return Promise.resolve({ status: 'ok' });
      }
      if (path.includes('/connections/chconn_test')) {
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
                    id: 'chroute_existing',
                    externalChannelId: 'C_EXISTING',
                    externalChannelType: 'channel',
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
                id: 'chroute_existing',
                externalChannelId: 'C_EXISTING',
                externalChannelType: 'channel',
                requireMention: false,
                enabled: true,
              },
            ],
          },
          path,
        });
      }
      if (path.includes('/connections/missing')) {
        return Promise.reject(new Error('connection missing'));
      }
      return Promise.reject(new Error(`unexpected request: ${path}`));
    });

    render(
      <MemoryRouter initialEntries={['/slack/channels/installations/chinst_test/connections/chconn_test']}>
        <Link to="/slack/channels/installations/chinst_test/connections/missing">Broken route</Link>
        <SettingsPage />
      </MemoryRouter>,
    );

    await screen.findByText('C_EXISTING');
    await user.click(screen.getByRole('link', { name: 'Broken route' }));

    expect(await screen.findByText('connection missing')).toBeTruthy();
    expect(await screen.findByText('Channel connection was not found.')).toBeTruthy();
    expect(screen.queryByText('C_EXISTING')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Remove' })).toBeNull();
  });

  it('renders a reconnect action for disconnected workspace installs when allowed', async () => {
    requestMock.mockResolvedValue({
      status: 'ok',
      installations: [
        {
          id: 'chinst_disconnected',
          state: 'disconnected',
          route: {
            provider: 'slack',
            principalId: 'shared-slack-app',
            externalScopeType: 'workspace',
            externalTenantId: 'T_workspace',
          },
          allowedActions: ['reconnect'],
          connections: [],
        },
      ],
    });

    render(
      <MemoryRouter initialEntries={['/slack/workspaces']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    const reconnect = await screen.findByRole('link', { name: /Reconnect/i });
    expect(reconnect.getAttribute('href')).toBe('/slack-gateway/slack/install');
    expect(screen.queryByRole('link', { name: 'Target' })).toBeNull();
    expect(screen.queryByRole('link', { name: /Test/i })).toBeNull();
  });

  it('renders the target action only when allowed', async () => {
    requestMock.mockResolvedValue({
      status: 'ok',
      installations: [
        {
          id: 'chinst_ready',
          state: 'ready',
          route: {
            provider: 'slack',
            principalId: 'shared-slack-app',
            externalScopeType: 'workspace',
            externalTenantId: 'T_workspace',
          },
          allowedActions: ['changeTarget'],
          connections: [],
        },
      ],
    });

    render(
      <MemoryRouter initialEntries={['/slack/workspaces']}>
        <SettingsPage />
      </MemoryRouter>,
    );

    const target = await screen.findByRole('link', { name: 'Target' });
    expect(target.getAttribute('href')).toBe('/settings/slack/workspaces/target?teamId=T_workspace');
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
      if (path === '/api/slack/install/selection?requestId=install-request-1&state=pending-state-1' && options?.method !== 'POST') {
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
      <MemoryRouter initialEntries={['/settings/slack/install/select?requestId=install-request-1&state=pending-state-1']}>
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
    const postCall = requestMock.mock.calls.find(
      ([path, options]) => path === '/api/slack/install/selection' && (options as RequestInit | undefined)?.method === 'POST',
    );
    expect(JSON.parse(String((postCall?.[1] as RequestInit).body))).toMatchObject({
      requestId: 'install-request-1',
      state: 'pending-state-1',
      presetInputs: { agentId: 'ag_workspace' },
    });
  });

  it('routes typed install picker load failures to the install result page', async () => {
    const installResult = {
      status: 'error',
      code: 'state.expired',
      operation: 'channel.install',
      provider: 'slack',
      requestId: 'install-request-1',
      title: 'Install link expired',
      message: 'This install link expired before it completed. Start the install again.',
      retryable: true,
      actionLabel: 'Start install again',
      actionHref: '/slack-gateway/slack/install',
    };
    requestMock.mockImplementation((path: string) => {
      if (path === '/api/slack/install/selection?requestId=install-request-1') {
        return Promise.resolve(installResult);
      }
      if (path.startsWith('/api/slack/install/result?')) {
        return Promise.resolve(installResult);
      }
      return Promise.reject(new Error(`unexpected request: ${path}`));
    });

    render(
      <MemoryRouter initialEntries={['/settings/slack/install/select?requestId=install-request-1']}>
        <Routes>
          <Route path="settings/*" element={<SettingsPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText('Install link expired')).toBeTruthy();
    expect(await screen.findByText('state.expired')).toBeTruthy();
    expect(await screen.findByRole('link', { name: 'Start install again' })).toBeTruthy();
  });

  it('does not render query-provided install result action links', async () => {
    requestMock.mockImplementation((path: string) => {
      if (path.startsWith('/api/slack/install/result?')) {
        return Promise.reject(new Error('gateway unavailable'));
      }
      return Promise.reject(new Error(`unexpected request: ${path}`));
    });

    render(
      <MemoryRouter
        initialEntries={[
          '/settings/slack/install/result?status=error&code=internal.error&actionLabel=Continue&actionHref=javascript:alert(1)',
        ]}
      >
        <Routes>
          <Route path="settings/*" element={<SettingsPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByText('Slack install needs attention')).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Continue' })).toBeNull();
  });
});
