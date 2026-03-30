export interface Spritz {
  metadata: {
    name: string;
    namespace: string;
  };
  spec: {
    image: string;
    agentRef?: {
      type?: string;
      provider?: string;
      id?: string;
    };
    profileOverrides?: {
      name?: string;
      imageUrl?: string;
    };
    ssh?: {
      mode: string;
    };
  };
  status: {
    phase: string;
    message?: string;
    url?: string;
    profile?: {
      name?: string;
      imageUrl?: string;
      source?: string;
      syncer?: string;
      observedGeneration?: number;
      lastSyncedAt?: string;
      lastError?: string;
    };
    acp?: {
      state: string;
      agentInfo?: {
        name?: string;
        title?: string;
        version?: string;
      };
    };
    ssh?: {
      host: string;
      user: string;
      port: number;
    };
  };
}
