import { describe, expect, it, vi } from 'vite-plus/test';
import { screen } from '@testing-library/react';
import { Sidebar } from './sidebar';
import { renderWithProviders } from '@/test/helpers';
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

describe('Sidebar', () => {
  it('uses the branded emphasis treatment for the active conversation', () => {
    const spritz = {
      metadata: { name: 'claude-code-lucky-tidepool' },
    } as Spritz;
    const conversation = {
      metadata: { name: 'conv-1' },
      spec: { title: 'Today work', spritzName: 'claude-code-lucky-tidepool' },
      status: {},
    } as ConversationInfo;

    renderWithProviders(
      <Sidebar
        agents={[{ spritz, conversations: [conversation] }]}
        selectedConversationId="conv-1"
        onSelectConversation={vi.fn()}
        onNewConversation={vi.fn()}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        mobileOpen={false}
        onCloseMobile={vi.fn()}
      />,
    );

    const activeConversation = screen.getByRole('button', { name: 'Today work' });
    expect(activeConversation.className).toContain('bg-[var(--surface-emphasis)]');
    expect(activeConversation.className).toContain('text-primary');
    expect(activeConversation.className).toContain(
      'shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--primary)_14%,transparent)]',
    );
  });
});
