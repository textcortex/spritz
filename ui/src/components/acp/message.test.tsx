import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { act, render, screen } from '@testing-library/react';
import { ChatMessage } from './message';
import type { ACPMessage } from '@/types/acp';

describe('ChatMessage', () => {
  const writeText = vi.fn<(text: string) => Promise<void>>();
  const execCommand = vi.fn<(command: string) => boolean>();

  beforeEach(() => {
    writeText.mockReset();
    writeText.mockResolvedValue(undefined);
    execCommand.mockReset();
    execCommand.mockReturnValue(true);
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommand,
    });
  });

  it('copies assistant message text and updates the action label', async () => {
    const message: ACPMessage = {
      role: 'assistant',
      blocks: [{ type: 'text', text: 'helm get values' }],
    };

    render(<ChatMessage message={message} />);

    const copyButton = screen.getByRole('button', { name: 'Copy message' });
    await act(async () => {
      copyButton.click();
      await Promise.resolve();
    });

    expect(writeText).toHaveBeenCalledWith('helm get values');
    expect(screen.getByRole('button', { name: 'Message copied' })).toBeDefined();
  });

  it('falls back to execCommand when the async clipboard API is unavailable', async () => {
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    });

    const message: ACPMessage = {
      role: 'assistant',
      blocks: [{ type: 'text', text: 'kubectl -n spritz-system get pods' }],
    };

    render(<ChatMessage message={message} />);

    const copyButton = screen.getByRole('button', { name: 'Copy message' });
    await act(async () => {
      copyButton.click();
      await Promise.resolve();
    });

    expect(execCommand).toHaveBeenCalledWith('copy');
    expect(screen.getByRole('button', { name: 'Message copied' })).toBeDefined();
  });

  it('does not render copy actions for tool cards', () => {
    const message: ACPMessage = {
      role: 'tool',
      title: 'Tool result',
      blocks: [{ type: 'text', text: 'kubectl output' }],
    };

    render(<ChatMessage message={message} />);

    expect(screen.queryByRole('button', { name: 'Copy message' })).toBeNull();
  });

  it('uses the brand accent treatment for neutral status pills', () => {
    const message: ACPMessage = {
      role: 'system',
      title: 'Update',
      status: 'in_progress',
      blocks: [{ type: 'text', text: 'Provisioning instance' }],
    };

    render(<ChatMessage message={message} />);

    const pill = screen.getByText('in progress');
    expect(pill.className).toContain('bg-[color-mix(in_srgb,var(--primary)_12%,transparent)]');
    expect(pill.className).toContain('text-primary');
  });
});
