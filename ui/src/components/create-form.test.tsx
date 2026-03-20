import type React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { render, screen } from '@testing-library/react';
import { CreateForm } from './create-form';

vi.mock('@/lib/config', async () => {
  const actual = await vi.importActual<typeof import('@/lib/config')>('@/lib/config');
  return {
    ...actual,
    useConfig: () => ({ ...actual.config, ownerId: 'user-1' }),
  };
});

vi.mock('@/lib/presets', () => ({
  usePresets: () => [],
}));

vi.mock('@/components/notice-banner', () => ({
  useNotice: () => ({ showNotice: vi.fn() }),
}));

vi.mock('@/components/preset-panel', () => ({
  PresetPanel: () => null,
  findPresetIndex: () => '',
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
  });

  it('does not advertise shared mounts in the default advanced user config copy', () => {
    render(<CreateForm />);

    const textarea = screen.getByLabelText('User config (YAML/JSON)');
    expect(textarea.getAttribute('placeholder')).not.toContain('sharedMounts');
    expect(
      screen.getByText(/Provide ttl, repo, env, or resources\. JSON is also accepted\./),
    ).toBeDefined();
  });
});
