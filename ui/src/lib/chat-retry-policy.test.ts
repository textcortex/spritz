import { describe, expect, it } from 'vite-plus/test';
import {
  DEFAULT_MAX_RECONNECT_DELAY_MS,
  DEFAULT_RECONNECT_DELAY_MS,
  getReconnectDelayMs,
  shouldForceBootstrapOnRetry,
} from './chat-retry-policy';

describe('getReconnectDelayMs', () => {
  it('uses exponential backoff from the base delay', () => {
    expect(getReconnectDelayMs(0)).toBe(DEFAULT_RECONNECT_DELAY_MS);
    expect(getReconnectDelayMs(1)).toBe(DEFAULT_RECONNECT_DELAY_MS);
    expect(getReconnectDelayMs(2)).toBe(DEFAULT_RECONNECT_DELAY_MS * 2);
    expect(getReconnectDelayMs(3)).toBe(DEFAULT_RECONNECT_DELAY_MS * 4);
  });

  it('caps the delay at the max delay', () => {
    expect(getReconnectDelayMs(100)).toBe(DEFAULT_MAX_RECONNECT_DELAY_MS);
  });
});

describe('shouldForceBootstrapOnRetry', () => {
  it('forces bootstrap when explicitly requested', () => {
    expect(shouldForceBootstrapOnRetry(0, true)).toBe(true);
  });

  it('forces bootstrap only on later retries by default', () => {
    expect(shouldForceBootstrapOnRetry(0)).toBe(false);
    expect(shouldForceBootstrapOnRetry(1)).toBe(false);
    expect(shouldForceBootstrapOnRetry(2)).toBe(true);
  });
});
