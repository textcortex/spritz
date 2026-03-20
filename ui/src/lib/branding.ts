import type {
  BrandingConfig,
  BrandingTerminalConfig,
  BrandingThemeConfig,
} from '@/lib/config';

const DEFAULT_PRODUCT_NAME = 'Spritz';
const MANAGED_FAVICON_ATTR = 'data-spritz-branding-favicon';

const DEFAULT_TERMINAL_THEME = {
  background: '#000000',
  foreground: '#f0f0f0',
  cursor: '#f0f0f0',
};

const THEME_VARIABLES: Record<keyof BrandingThemeConfig, string> = {
  background: '--brand-background',
  foreground: '--brand-foreground',
  muted: '--brand-muted',
  mutedForeground: '--brand-muted-foreground',
  primary: '--brand-primary',
  primaryForeground: '--brand-primary-foreground',
  border: '--brand-border',
  destructive: '--brand-destructive',
  radius: '--brand-radius',
};

function normalize(value: string | null | undefined): string {
  return String(value || '').trim();
}

export function getProductName(branding?: BrandingConfig | null): string {
  return normalize(branding?.productName) || DEFAULT_PRODUCT_NAME;
}

export function getLogoUrl(branding?: BrandingConfig | null): string {
  return normalize(branding?.logoUrl);
}

export function applyBrandingTheme(
  theme?: BrandingThemeConfig | null,
  root: HTMLElement = document.documentElement,
): () => void {
  const previous = new Map<string, string>();

  for (const [key, cssVariable] of Object.entries(THEME_VARIABLES)) {
    previous.set(cssVariable, root.style.getPropertyValue(cssVariable));
    const value = normalize(theme?.[key as keyof BrandingThemeConfig]);
    if (value) {
      root.style.setProperty(cssVariable, value);
    } else {
      root.style.removeProperty(cssVariable);
    }
  }

  return () => {
    for (const [cssVariable, value] of previous.entries()) {
      if (value) {
        root.style.setProperty(cssVariable, value);
      } else {
        root.style.removeProperty(cssVariable);
      }
    }
  };
}

export function syncDocumentBranding(
  branding?: BrandingConfig | null,
  doc: Document = document,
): () => void {
  const previousTitle = doc.title;
  const previousManagedFavicon = doc.querySelector<HTMLLinkElement>(`link[${MANAGED_FAVICON_ATTR}]`);
  const existingFavicon = doc.querySelector<HTMLLinkElement>('link[rel="icon"], link[rel="shortcut icon"]');
  const faviconUrl = normalize(branding?.faviconUrl);

  doc.title = getProductName(branding);

  if (faviconUrl) {
    const favicon = existingFavicon || doc.createElement('link');
    favicon.rel = 'icon';
    favicon.href = faviconUrl;
    favicon.setAttribute(MANAGED_FAVICON_ATTR, 'true');
    if (!favicon.parentNode) {
      doc.head.appendChild(favicon);
    }
  } else if (existingFavicon?.hasAttribute(MANAGED_FAVICON_ATTR)) {
    existingFavicon.remove();
  }

  return () => {
    doc.title = previousTitle;

    const currentManagedFavicon = doc.querySelector<HTMLLinkElement>(`link[${MANAGED_FAVICON_ATTR}]`);
    if (currentManagedFavicon) {
      currentManagedFavicon.remove();
    }

    if (previousManagedFavicon && !previousManagedFavicon.parentNode) {
      doc.head.appendChild(previousManagedFavicon);
    }
  };
}

export function buildTerminalTheme(terminal?: BrandingTerminalConfig | null) {
  return {
    background: normalize(terminal?.background) || DEFAULT_TERMINAL_THEME.background,
    foreground: normalize(terminal?.foreground) || DEFAULT_TERMINAL_THEME.foreground,
    cursor: normalize(terminal?.cursor) || DEFAULT_TERMINAL_THEME.cursor,
  };
}
