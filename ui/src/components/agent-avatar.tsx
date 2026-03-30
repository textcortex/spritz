import { useEffect, useState } from 'react';
import { cn } from '@/lib/utils';
import { getAgentInitials } from '@/lib/spritz-profile';

interface AgentAvatarProps {
  name: string;
  imageUrl?: string;
  className?: string;
}

export function AgentAvatar({ name, imageUrl, className }: AgentAvatarProps) {
  const [imageFailed, setImageFailed] = useState(false);
  const initials = getAgentInitials(name);
  const showImage = Boolean(imageUrl) && !imageFailed;

  useEffect(() => {
    setImageFailed(false);
  }, [imageUrl]);

  return (
    <div
      aria-hidden="true"
      className={cn(
        'flex size-9 shrink-0 items-center justify-center overflow-hidden rounded-full border border-border bg-muted text-[11px] font-semibold text-muted-foreground',
        className,
      )}
    >
      {showImage ? (
        <img
          src={imageUrl}
          alt=""
          className="size-full object-cover"
          loading="lazy"
          onError={() => setImageFailed(true)}
        />
      ) : (
        <span>{initials}</span>
      )}
    </div>
  );
}
