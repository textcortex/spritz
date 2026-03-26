import { describe, expect, it } from 'vite-plus/test';
import { screen } from '@testing-library/react';
import { BrandHeader } from '@/components/brand-header';
import { renderWithProviders } from '@/test/helpers';

describe('BrandHeader', () => {
  it('renders the bundled default logo when no custom logo is configured', () => {
    renderWithProviders(<BrandHeader />);

    expect(screen.getByAltText('Spritz logo').getAttribute('src')).toBe('/agentol.svg');
  });

  it('renders the configured brand name and logo', () => {
    renderWithProviders(<BrandHeader />, {
      config: {
        branding: {
          productName: 'Example Console',
          logoUrl: 'https://console.example.com/logo.png',
          faviconUrl: '',
          theme: {
            background: '',
            foreground: '',
            muted: '',
            mutedForeground: '',
            primary: '',
            primaryForeground: '',
            border: '',
            destructive: '',
            radius: '',
          },
          terminal: {
            background: '',
            foreground: '',
            cursor: '',
          },
        },
      },
    });

    expect(screen.getByText('Example Console')).not.toBeNull();
    expect(screen.getByAltText('Example Console logo').getAttribute('src')).toBe(
      'https://console.example.com/logo.png',
    );
  });
});
