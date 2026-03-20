import { describe, expect, it } from 'vite-plus/test';
import { renderWithProviders } from '@/test/helpers';
import { BrandingEffects } from '@/components/branding-effects';

describe('BrandingEffects', () => {
  it('applies document branding and theme variables', () => {
    document.title = 'Before';

    renderWithProviders(<BrandingEffects />, {
      config: {
        branding: {
          productName: 'Example Console',
          logoUrl: '',
          faviconUrl: 'https://console.example.com/favicon.ico',
          theme: {
            background: '#f8fafc',
            foreground: '#0f172a',
            muted: '#e2e8f0',
            mutedForeground: '#475569',
            primary: '#1d4ed8',
            primaryForeground: '#eff6ff',
            border: '#cbd5e1',
            destructive: '#dc2626',
            radius: '1rem',
          },
          terminal: {
            background: '#111827',
            foreground: '#f9fafb',
            cursor: '#60a5fa',
          },
        },
      },
    });

    expect(document.title).toBe('Example Console');
    expect(document.documentElement.style.getPropertyValue('--brand-primary')).toBe('#1d4ed8');
    expect(document.documentElement.style.getPropertyValue('--brand-radius')).toBe('1rem');

    const favicon = document.head.querySelector('link[rel="icon"]');
    expect(favicon).not.toBeNull();
    expect(favicon?.getAttribute('href')).toBe('https://console.example.com/favicon.ico');
  });
});
