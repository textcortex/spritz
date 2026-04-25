import { afterEach, describe, expect, it, vi } from 'vite-plus/test';

async function loadSlackManagement(config: typeof window.SPRITZ_CONFIG) {
  vi.resetModules();
  window.SPRITZ_CONFIG = config;
  return import('./slack-management');
}

afterEach(() => {
  delete window.SPRITZ_CONFIG;
  vi.resetModules();
});

describe('slack gateway path helpers', () => {
  it('uses the default Slack gateway base path', async () => {
    const { slackGatewayBasePath, slackGatewayPath } = await loadSlackManagement({});

    expect(slackGatewayBasePath()).toBe('/slack-gateway');
    expect(slackGatewayPath('/slack/install')).toBe('/slack-gateway/slack/install');
  });

  it('treats an explicit root base path as same-origin root', async () => {
    const { slackGatewayBasePath, slackGatewayPath } = await loadSlackManagement({
      slackGatewayBasePath: '/',
    });

    expect(slackGatewayBasePath()).toBe('');
    expect(slackGatewayPath('/api/settings/channels')).toBe('/api/settings/channels');
    expect(slackGatewayPath('/slack/install')).toBe('/slack/install');
  });

  it('preserves an absolute Slack gateway base URL', async () => {
    const { slackGatewayBasePath, slackGatewayPath } = await loadSlackManagement({
      slackGatewayBasePath: 'https://gateway.example.test/slack-gateway/',
    });

    expect(slackGatewayBasePath()).toBe('https://gateway.example.test/slack-gateway');
    expect(slackGatewayPath('/slack/workspaces')).toBe(
      'https://gateway.example.test/slack-gateway/slack/workspaces',
    );
  });
});
