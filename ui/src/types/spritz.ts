export interface Spritz {
  metadata: {
    name: string;
    namespace: string;
  };
  spec: {
    image: string;
    ssh?: {
      mode: string;
    };
  };
  status: {
    phase: string;
    message?: string;
    url?: string;
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
