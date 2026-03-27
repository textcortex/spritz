export const DEFAULT_RECONNECT_DELAY_MS = 2000;
export const DEFAULT_MAX_RECONNECT_DELAY_MS = 15000;

interface ChatRetryPolicyOptions {
  baseDelayMs?: number;
  maxDelayMs?: number;
}

/**
 * Returns the reconnect delay for the next retry using capped exponential backoff.
 */
export function getReconnectDelayMs(
  retryCount: number,
  options: ChatRetryPolicyOptions = {},
): number {
  const {
    baseDelayMs = DEFAULT_RECONNECT_DELAY_MS,
    maxDelayMs = DEFAULT_MAX_RECONNECT_DELAY_MS,
  } = options;
  return Math.min(baseDelayMs * (2 ** Math.max(retryCount - 1, 0)), maxDelayMs);
}

/**
 * Escalates retries into a fresh bootstrap after the initial reconnect attempt.
 */
export function shouldForceBootstrapOnRetry(
  retryCount: number,
  forceBootstrap = false,
): boolean {
  return forceBootstrap || retryCount > 1;
}
