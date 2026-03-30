import { createElement } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';
import { ConfigProvider, resolveConfig, type Preset } from './config';
import { parsePresets, usePresets } from './presets';

const requestMock = vi.hoisted(() => vi.fn());

vi.mock('./api', () => ({
  request: (...args: unknown[]) => requestMock(...args),
}));

function PresetProbe() {
  const presets = usePresets();
  return createElement(
    'div',
    null,
    presets.length
      ? presets.map((preset) => createElement('div', { key: preset.id || preset.name }, preset.name))
      : 'empty',
  );
}

describe('parsePresets', () => {
  beforeEach(() => {
    requestMock.mockReset();
  });

  it('returns raw arrays directly', () => {
    const input = [{ name: 'Test', image: 'test:latest', description: '', repoUrl: '', branch: '', ttl: '' }];
    expect(parsePresets(input)).toBe(input);
  });

  it('returns an empty list for placeholder string', () => {
    const result = parsePresets('__SPRITZ_UI_PRESETS__');
    expect(result).toEqual([]);
  });

  it('returns an empty list for empty string', () => {
    const result = parsePresets('');
    expect(result).toEqual([]);
  });

  it('returns an empty list for whitespace-only string', () => {
    const result = parsePresets('   ');
    expect(result).toEqual([]);
  });

  it('returns an empty list for undefined', () => {
    const result = parsePresets(undefined);
    expect(result).toEqual([]);
  });

  it('returns an empty list for null', () => {
    const result = parsePresets(null);
    expect(result).toEqual([]);
  });

  it('parses valid JSON string into preset array', () => {
    const presets = [{ name: 'Custom', image: 'custom:v1', description: 'desc', repoUrl: '', branch: '', ttl: '' }];
    const result = parsePresets(JSON.stringify(presets));
    expect(result).toEqual(presets);
  });

  it('returns empty array for malformed JSON (fails closed)', () => {
    const result = parsePresets('not valid json {{{');
    expect(result).toEqual([]);
  });

  it('returns empty array for non-array JSON', () => {
    const result = parsePresets('{"key": "value"}');
    expect(result).toEqual([]);
  });
});

describe('usePresets', () => {
  beforeEach(() => {
    requestMock.mockReset();
  });

  it('loads presets from the API', async () => {
    requestMock.mockResolvedValue({
      items: [
        {
          id: 'codex',
          name: 'Codex',
          image: 'spritz-codex:latest',
          description: 'Codex example image.',
          repoUrl: '',
          branch: '',
          ttl: '',
        },
      ],
    });

    render(
      createElement(
        ConfigProvider,
        {
          value: resolveConfig({
            apiBaseUrl: '/api',
            presets: JSON.stringify([
              {
                id: 'legacy',
                name: 'Legacy',
                image: 'legacy:latest',
                description: 'legacy',
                repoUrl: '',
                branch: '',
                ttl: '',
              },
            ]),
          }),
        },
        createElement(PresetProbe),
      ),
    );

    await waitFor(() => {
      expect(screen.getByText('Codex')).toBeDefined();
    });
    expect(screen.queryByText('Legacy')).toBeNull();
    expect(requestMock).toHaveBeenCalledWith('/presets');
  });

  it('falls back to configured presets when the API request fails', async () => {
    const configuredPreset: Preset = {
      id: 'claude-code',
      name: 'Claude Code',
      image: 'spritz-claude-code:latest',
      description: 'Claude Code example image.',
      repoUrl: '',
      branch: '',
      ttl: '',
    };
    requestMock.mockRejectedValue(new Error('boom'));

    render(
      createElement(
        ConfigProvider,
        {
          value: resolveConfig({
            apiBaseUrl: '/api',
            presets: JSON.stringify([configuredPreset]),
          }),
        },
        createElement(PresetProbe),
      ),
    );

    await waitFor(() => {
      expect(screen.getByText('Claude Code')).toBeDefined();
    });
  });

  it('filters hidden presets out of the fallback catalog when the API request fails', async () => {
    requestMock.mockRejectedValue(new Error('boom'));

    render(
      createElement(
        ConfigProvider,
        {
          value: resolveConfig({
            apiBaseUrl: '/api',
            presets: JSON.stringify([
              {
                id: 'visible',
                name: 'Visible',
                image: 'visible:latest',
                description: 'visible',
                repoUrl: '',
                branch: '',
                ttl: '',
              },
              {
                id: 'hidden',
                name: 'Hidden',
                image: 'hidden:latest',
                description: 'hidden',
                repoUrl: '',
                branch: '',
                ttl: '',
                hidden: true,
              },
            ]),
          }),
        },
        createElement(PresetProbe),
      ),
    );

    await waitFor(() => {
      expect(screen.getByText('Visible')).toBeDefined();
    });
    expect(screen.queryByText('Hidden')).toBeNull();
  });

  it('treats an empty API catalog as authoritative', async () => {
    const configuredPreset: Preset = {
      id: 'claude-code',
      name: 'Claude Code',
      image: 'spritz-claude-code:latest',
      description: 'Claude Code example image.',
      repoUrl: '',
      branch: '',
      ttl: '',
    };
    requestMock.mockResolvedValue({ items: [] });

    render(
      createElement(
        ConfigProvider,
        {
          value: resolveConfig({
            apiBaseUrl: '/api',
            presets: JSON.stringify([configuredPreset]),
          }),
        },
        createElement(PresetProbe),
      ),
    );

    await waitFor(() => {
      expect(requestMock).toHaveBeenCalledWith('/presets');
    });
    expect(screen.queryByText('Claude Code')).toBeNull();
    expect(screen.getByText('empty')).toBeDefined();
  });
});
