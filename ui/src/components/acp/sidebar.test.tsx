import type React from 'react';
import { describe, it, expect, vi } from 'vite-plus/test';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Sidebar } from './sidebar';

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

function createSpritz(name: string) {
  return {
    metadata: { name, namespace: 'default' },
    spec: { image: `example.com/${name}:latest` },
    status: {
      phase: 'Ready',
      acp: { state: 'ready' },
    },
  };
}

function createConversation(name: string, title: string, spritzName: string) {
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
  },
) => React.ReactElement;

describe('Sidebar', () => {
  it('moves the focused agent to the top, highlights it, and collapses other agents', () => {
    render(
      <MemoryRouter>
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
          onNewConversation={vi.fn()}
          collapsed={false}
          onToggleCollapse={vi.fn()}
          mobileOpen={false}
          onCloseMobile={vi.fn()}
          focusedSpritzName="beta"
        />
      </MemoryRouter>,
    );

    const agentHeaders = screen.getAllByRole('button', { name: / conversations$/i });
    expect(agentHeaders[0]?.getAttribute('aria-label')).toBe('beta conversations');
    expect(screen.getByRole('button', { name: 'beta conversations' }).getAttribute('aria-current')).toBe('true');
    expect(screen.getByRole('button', { name: 'beta conversations' }).getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByRole('button', { name: 'alpha conversations' }).getAttribute('aria-expanded')).toBe('false');
  });
});
