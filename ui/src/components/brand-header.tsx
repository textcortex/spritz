import { useConfig } from '@/lib/config';
import { getLogoUrl, getProductName } from '@/lib/branding';

interface BrandHeaderProps {
  compact?: boolean;
}

export function BrandHeader({ compact = false }: BrandHeaderProps) {
  const { branding } = useConfig();
  const productName = getProductName(branding);
  const logoUrl = getLogoUrl(branding);
  const initials = productName.slice(0, 1).toUpperCase();

  return (
    <div className="flex min-w-0 items-center gap-3">
      {logoUrl ? (
        <div className="flex size-9 shrink-0 items-center justify-center rounded-[var(--radius-lg)] bg-background p-1">
          <img
            src={logoUrl}
            alt={`${productName} logo`}
            className="max-h-full max-w-full object-contain"
          />
        </div>
      ) : (
        <div className="flex size-8 items-center justify-center rounded-[var(--radius-lg)] bg-primary text-sm font-semibold text-primary-foreground">
          {initials}
        </div>
      )}
      {!compact && (
        <span className="truncate text-[15px] font-semibold tracking-tight text-foreground">
          {productName}
        </span>
      )}
    </div>
  );
}
