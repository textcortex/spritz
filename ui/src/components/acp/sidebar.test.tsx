import type React from 'react';
import { describe, expect, it, vi } from 'vite-plus/test';
import { screen } from '@testing-library/react';
import { Sidebar } from './sidebar';
import { renderWithProviders } from '@/test/helpers';
import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

vi.mock('@/components/brand-header', () => ({
  BrandHeader: ({ compact }: { compact?: boolean }) => (
    <div>{compact ? 'Brand compact' : 'Brand'}</div>
  ),
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

function createSpritz(name: string): Spritz {
  return {
    metadata: { name, namespace: 'default' },
    spec: { image: `example.com/${name}:latest` },
    status: {
      phase: 'Ready',
      acp: { state: 'ready' },
    },
  };
}

function createConversation(
  name: string,
  title: string,
  spritzName: string,
): ConversationInfo {
  return {
    metadata: { name },
    spec: {
      sessionId: `${name}-session`,
      title,
      spritzName,
    },
    status: {
      bindingState: 'active',
    },
  };
}

const SidebarWithFocus = Sidebar as unknown as (
  props: React.ComponentProps<typeof Sidebar> & {
    focusedSpritzName?: string | null;
    focusedSpritz?: Spritz | null;
  },
) => React.ReactElement;

describe('Sidebar', () => {
  it('uses the branded emphasis treatment for the active conversation', () => {
    const spritz = createSpritz('claude-code-lucky-tidepool');
    const conversation = createConversation('conv-1', 'Today work', 'claude-code-lucky-tidepool');

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

  it('keeps agents in alphabetical order regardless of focus and collapses non-focused agents', () => {
    renderWithProviders(
      <SidebarWithFocus
        agents={[
          {
            spritz: createSpritz('beta'),
            conversations: [createConversation('beta-conv', 'Beta conversation', 'beta')],
          },
          {
            spritz: createSpritz('alpha'),
            conversations: [createConversation('alpha-conv', 'Alpha conversation', 'alpha')],
          },
        ]}
        selectedConversationId="beta-conv"
        onSelectConversation={vi.fn()}
        onNewConversation={vi.fn()}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        mobileOpen={false}
        onCloseMobile={vi.fn()}
        focusedSpritzName="beta"
      />,
    );

    const agentHeaders = screen.getAllByRole('button', {
      name: / conversations$/i,
    });
    // Alpha comes first alphabetically, even though beta is focused
    expect(agentHeaders[0]?.getAttribute('aria-label')).toBe('alpha conversations');
    expect(agentHeaders[1]?.getAttribute('aria-label')).toBe('beta conversations');
    expect(
      screen
        .getByRole('button', { name: 'beta conversations' })
        .getAttribute('aria-current'),
    ).toBe('true');
    expect(
      screen
        .getByRole('button', { name: 'beta conversations' })
        .getAttribute('aria-expanded'),
    ).toBe('true');
    expect(
      screen
        .getByRole('button', { name: 'alpha conversations' })
        .getAttribute('aria-expanded'),
    ).toBe('false');
  });

  it('sorts agents alphabetically even when passed in reverse order', () => {
    renderWithProviders(
      <Sidebar
        agents={[
          {
            spritz: createSpritz('zulu'),
            conversations: [createConversation('zulu-conv', 'Zulu conversation', 'zulu')],
          },
          {
            spritz: createSpritz('alpha'),
            conversations: [createConversation('alpha-conv', 'Alpha conversation', 'alpha')],
          },
          {
            spritz: createSpritz('mike'),
            conversations: [createConversation('mike-conv', 'Mike conversation', 'mike')],
          },
        ]}
        selectedConversationId={null}
        onSelectConversation={vi.fn()}
        onNewConversation={vi.fn()}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        mobileOpen={false}
        onCloseMobile={vi.fn()}
      />,
    );

    const agentHeaders = screen.getAllByRole('button', {
      name: / conversations$/i,
    });
    expect(agentHeaders[0]?.getAttribute('aria-label')).toBe('alpha conversations');
    expect(agentHeaders[1]?.getAttribute('aria-label')).toBe('mike conversations');
    expect(agentHeaders[2]?.getAttribute('aria-label')).toBe('zulu conversations');
  });

  it('calls onNewConversation with focused instance name, not first alphabetical', async () => {
    const onNewConversation = vi.fn();
    const { default: userEvent } = await import('@testing-library/user-event');

    renderWithProviders(
      <SidebarWithFocus
        agents={[
          {
            spritz: createSpritz('alpha'),
            conversations: [createConversation('alpha-conv', 'Alpha conversation', 'alpha')],
          },
          {
            spritz: createSpritz('beta'),
            conversations: [createConversation('beta-conv', 'Beta conversation', 'beta')],
          },
        ]}
        selectedConversationId="beta-conv"
        onSelectConversation={vi.fn()}
        onNewConversation={onNewConversation}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        mobileOpen={false}
        onCloseMobile={vi.fn()}
        focusedSpritzName="beta"
      />,
    );

    // Click the top-level "New chat" button (pencil icon)
    const newChatButton = screen.getByRole('button', { name: 'New chat' });
    await userEvent.click(newChatButton);

    // Should create for focused instance "beta", not alphabetically first "alpha"
    expect(onNewConversation).toHaveBeenCalledWith('beta');
  });

  it('falls back to first agent for New chat when no instance is focused', async () => {
    const onNewConversation = vi.fn();
    const { default: userEvent } = await import('@testing-library/user-event');

    renderWithProviders(
      <Sidebar
        agents={[
          {
            spritz: createSpritz('alpha'),
            conversations: [createConversation('alpha-conv', 'Alpha conversation', 'alpha')],
          },
          {
            spritz: createSpritz('beta'),
            conversations: [createConversation('beta-conv', 'Beta conversation', 'beta')],
          },
        ]}
        selectedConversationId={null}
        onSelectConversation={vi.fn()}
        onNewConversation={onNewConversation}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        mobileOpen={false}
        onCloseMobile={vi.fn()}
      />,
    );

    const newChatButton = screen.getByRole('button', { name: 'New chat' });
    await userEvent.click(newChatButton);

    // No focused instance, so falls back to first alphabetical ("alpha")
    expect(onNewConversation).toHaveBeenCalledWith('alpha');
  });

  it('shows a selected optimistic provisioning conversation for a focused route before the agent is discoverable', () => {
    renderWithProviders(
      <SidebarWithFocus
        agents={[]}
        selectedConversationId={null}
        onSelectConversation={vi.fn()}
        onNewConversation={vi.fn()}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        mobileOpen={false}
        onCloseMobile={vi.fn()}
        focusedSpritzName="zeno-fresh-ridge"
        focusedSpritz={null}
      />,
    );

    expect(screen.getByText('zeno-fresh-ridge')).toBeTruthy();
    expect(screen.getByText('Creating your agent instance.')).toBeTruthy();
    expect(
      screen.getByText('Starting…').closest('[aria-current="true"]'),
    ).toBeTruthy();
  });
});
