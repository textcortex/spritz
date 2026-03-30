import { describe, expect, it } from 'vite-plus/test';
import {
  getAgentInitials,
  getConversationAgentImageUrl,
  getConversationAgentName,
  getSpritzProfileImageUrl,
  getSpritzProfileName,
} from './spritz-profile';

describe('spritz profile helpers', () => {
  it('prefers synced profile name over ACP metadata and instance name', () => {
    expect(
      getSpritzProfileName({
        metadata: { name: 'tidy-otter', namespace: 'spritz-test' },
        spec: {
          image: 'example.com/agent:latest',
        },
        status: {
          phase: 'Ready',
          profile: { name: 'Helpful Otter' },
          acp: {
            state: 'ready',
            agentInfo: { title: 'ACP Title', name: 'acp-name' },
          },
        },
      }),
    ).toBe('Helpful Otter');
  });

  it('falls back from the spritz profile to ACP metadata and the instance name', () => {
    expect(
      getSpritzProfileName({
        metadata: { name: 'tidy-otter', namespace: 'spritz-test' },
        spec: {
          image: 'example.com/agent:latest',
        },
        status: {
          phase: 'Ready',
          acp: {
            state: 'ready',
            agentInfo: { title: 'ACP Title' },
          },
        },
      }),
    ).toBe('ACP Title');
  });

  it('uses the spritz profile for conversation rendering', () => {
    const spritz = {
      metadata: { name: 'tidy-otter', namespace: 'spritz-test' },
      spec: {
        image: 'example.com/agent:latest',
      },
      status: {
        phase: 'Ready',
        profile: {
          name: 'Helpful Otter',
          imageUrl: 'https://example.com/otter.png',
        },
      },
    };

    expect(
      getConversationAgentName(
        {
          metadata: { name: 'conv-1' },
          spec: { sessionId: 'sess-1', spritzName: 'tidy-otter' },
        },
        spritz,
      ),
    ).toBe('Helpful Otter');
    expect(getSpritzProfileImageUrl(spritz)).toBe('https://example.com/otter.png');
    expect(
      getConversationAgentImageUrl(
        {
          metadata: { name: 'conv-1' },
          spec: { sessionId: 'sess-1', spritzName: 'tidy-otter' },
        },
        spritz,
      ),
    ).toBe('https://example.com/otter.png');
  });

  it('derives stable initials from an agent name', () => {
    expect(getAgentInitials('Helpful Otter')).toBe('HO');
    expect(getAgentInitials('Solo')).toBe('SO');
    expect(getAgentInitials('   ')).toBe('?');
  });
});
