declare const module:
  | {
      exports?: unknown;
    }
  | undefined;

declare global {
  interface Error {
    code?: string;
    payload?: unknown;
    rpcError?: unknown;
    status?: number;
  }

  interface Window {
    SPRITZ_CONFIG?: any;
    SpritzACPClient?: any;
    SpritzACPRender?: any;
    SpritzACPPage?: any;
    SpritzCreateFormState?: any;
    SpritzCreateFormRequest?: any;
    SpritzPresetConfig?: any;
    SpritzPresetPanel?: any;
    Terminal?: any;
    FitAddon?: any;
  }
}

export {};
