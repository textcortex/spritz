import type { Spritz } from '@/types/spritz';

export const DEFAULT_PROVISIONING_MESSAGE = 'Creating your agent instance.';

export function isSpritzChatReady(spritz: Spritz | null | undefined): boolean {
  if (!spritz) return false;
  return spritz.status?.phase === 'Ready' && spritz.status?.acp?.state === 'ready';
}

export function buildProvisioningPlaceholderSpritz(name: string): Spritz {
  const normalizedName = String(name || '').trim();
  return {
    metadata: {
      name: normalizedName,
      namespace: '',
    },
    spec: {
      image: '',
    },
    status: {
      phase: 'Provisioning',
      message: DEFAULT_PROVISIONING_MESSAGE,
      acp: {
        state: 'starting',
      },
    },
  };
}

export function getProvisioningStatusLine(spritz: Spritz | null | undefined): string {
  const message = String(spritz?.status?.message || '').trim();
  if (message) return message;
  const composite = [spritz?.status?.phase, spritz?.status?.acp?.state]
    .map((value) => String(value || '').trim())
    .filter(Boolean)
    .join(' · ');
  return composite || DEFAULT_PROVISIONING_MESSAGE;
}
