import { useConfig } from '@/lib/config';
import { getLogoUrl, getProductName } from '@/lib/branding';
import { cn } from '@/lib/utils';

interface BrandHeaderProps {
  compact?: boolean;
}

export function BrandHeader({ compact = false }: BrandHeaderProps) {
  const { branding } = useConfig();
  const productName = getProductName(branding);
  const logoUrl = getLogoUrl(branding);

  return (
    <div className="flex min-w-0 items-center gap-3">
      <div className="flex shrink-0 items-center">
        <img
          src={logoUrl}
          alt={`${productName} logo`}
          className={cn(
            'block w-auto shrink-0 object-contain',
            compact ? 'h-9 max-w-9' : 'h-10 max-w-24',
          )}
        />
      </div>
      {!compact && (
        <span className="truncate text-[15px] font-semibold tracking-tight text-foreground">
          {productName}
        </span>
      )}
    </div>
  );
}
