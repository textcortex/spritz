import { describe, expect, it } from 'vite-plus/test';
import { buildWebSocketUrlFromConnectPath } from '@/lib/network';

describe('buildWebSocketUrlFromConnectPath', () => {
  it('uses the api base origin when websocketBaseUrl is unset', () => {
    expect(buildWebSocketUrlFromConnectPath('/api/acp/conversations/conv-1/connect', {
      apiBaseUrl: 'https://spritz.example.com/api',
      locationHref: 'http://localhost:3000/c/covo/conv-1',
    })).toBe('wss://spritz.example.com/api/acp/conversations/conv-1/connect');
  });

  it('preserves websocket base path prefixes ahead of the api path', () => {
    expect(buildWebSocketUrlFromConnectPath('/api/acp/conversations/conv-1/connect', {
      apiBaseUrl: 'https://spritz.example.com/proxy/api',
      websocketBaseUrl: 'https://spritz.example.com/proxy/api',
      locationHref: 'http://localhost:3000/c/covo/conv-1',
    })).toBe('wss://spritz.example.com/proxy/api/acp/conversations/conv-1/connect');
  });
});
