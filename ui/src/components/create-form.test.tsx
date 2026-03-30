import type React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { CreateForm } from './create-form';

const requestMock = vi.hoisted(() => vi.fn());
const currentPresets = vi.hoisted(() => ({
  value: [] as Array<{
    id?: string;
    name: string;
    image: string;
    description: string;
    repoUrl: string;
    branch: string;
    ttl: string;
  }>,
}));

vi.mock('@/lib/config', async () => {
  const actual = await vi.importActual<typeof import('@/lib/config')>('@/lib/config');
  return {
    ...actual,
    useConfig: () => ({ ...actual.config, ownerId: 'user-1' }),
  };
});

vi.mock('@/lib/api', () => ({
  request: (...args: unknown[]) => requestMock(...args),
}));

vi.mock('@/lib/presets', () => ({
  usePresets: () => currentPresets.value,
}));

vi.mock('@/components/notice-banner', () => ({
  useNotice: () => ({ showNotice: vi.fn() }),
}));

vi.mock('@/components/preset-panel', () => ({
  PresetPanel: ({ selectedIndex }: { selectedIndex: string }) => (
    <div data-testid="preset-index">{selectedIndex}</div>
  ),
  findPresetIndex: (
    presets: Array<{ name?: string; image?: string }>,
    selection: { mode?: string; presetName?: string; presetImage?: string },
  ) => {
    if (!selection || selection.mode !== 'preset') return '';
    const idx = presets.findIndex((preset) => {
      const matchesImage = selection.presetImage && preset.image === selection.presetImage;
      const matchesName = selection.presetName && preset.name === selection.presetName;
      if (selection.presetImage && selection.presetName) return matchesImage && matchesName;
      return Boolean(matchesImage || matchesName);
    });
    return idx >= 0 ? String(idx) : '';
  },
}));

vi.mock('@/components/ui/button', () => ({
  Button: ({
    children,
    render,
    ...props
  }: React.ComponentProps<'button'> & { render?: React.ReactNode }) => (
    <button type="button" {...props}>
      {render ?? children}
    </button>
  ),
}));

vi.mock('@/components/ui/input', () => ({
  Input: (props: React.ComponentProps<'input'>) => <input {...props} />,
}));

vi.mock('@/components/ui/label', () => ({
  Label: ({ children, ...props }: React.ComponentProps<'label'>) => (
    <label {...props}>{children}</label>
  ),
}));

vi.mock('@/components/ui/textarea', () => ({
  Textarea: (props: React.ComponentProps<'textarea'>) => <textarea {...props} />,
}));

vi.mock('@/components/ui/tooltip', () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({
    children,
    render,
  }: {
    children?: React.ReactNode;
    render?: React.ReactNode;
  }) => <>{render ?? children}</>,
}));

describe('CreateForm', () => {
  beforeEach(() => {
    window.localStorage.clear();
    requestMock.mockReset();
    currentPresets.value = [];
  });

  it('does not advertise shared mounts in the default advanced user config copy', () => {
    render(<CreateForm />);

    const textarea = screen.getByLabelText('User config (YAML/JSON)');
    expect(textarea.getAttribute('placeholder')).not.toContain('sharedMounts');
    expect(
      screen.getByText(/Provide ttl, repo, env, or resources\. JSON is also accepted\./),
    ).toBeDefined();
  });

  it('restores a saved preset selection after presets load and submits presetId', async () => {
    window.localStorage.setItem('spritz:create-form', JSON.stringify({
      selection: {
        mode: 'preset',
        presetName: 'Codex',
        presetImage: 'spritz-codex:latest',
      },
      fields: {
        image: 'spritz-codex:latest',
        repo: '',
        branch: '',
        ttl: '',
        namespace: '',
        userConfig: '',
      },
    }));

    const createBodies: Array<Record<string, unknown>> = [];
    requestMock.mockImplementation(async (path: string, options?: { body?: string }) => {
      if (path === '/spritzes/suggest-name') {
        return { name: 'codex-young-prairie' };
      }
      if (path === '/spritzes') {
        createBodies.push(JSON.parse(String(options?.body || '{}')));
        return {};
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    const view = render(<CreateForm />);

    currentPresets.value = [{
      id: 'codex',
      name: 'Codex',
      image: 'spritz-codex:latest',
      description: 'Codex example image.',
      repoUrl: '',
      branch: '',
      ttl: '',
    }];
    view.rerender(<CreateForm />);

    await waitFor(() => {
      expect(screen.getByTestId('preset-index').textContent).toBe('0');
    });

    fireEvent.click(screen.getByRole('button', { name: /Create instance/i }));

    await waitFor(() => {
      expect(createBodies).toHaveLength(1);
    });
    expect(createBodies[0].presetId).toBe('codex');
    expect((createBodies[0].spec as Record<string, unknown> | undefined)?.image).toBeUndefined();
  });
});
