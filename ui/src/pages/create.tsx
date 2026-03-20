import { useRef } from 'react';
import { BrandHeader } from '@/components/brand-header';
import { CreateForm } from '@/components/create-form';
import { SpritzList, type SpritzListHandle } from '@/components/spritz-list';

export function CreatePage() {
  const listRef = useRef<SpritzListHandle>(null);

  const handleCreated = () => {
    listRef.current?.refresh();
  };

  return (
    <div className="mx-auto flex max-w-[960px] flex-col gap-8 px-6 py-12 md:px-12">
      <div className="flex flex-col gap-1">
        <BrandHeader />
        <h2 className="mt-4 text-2xl font-semibold">Create Instance</h2>
        <p className="text-[15px] text-muted-foreground">
          Spin up an ephemeral instance managed by the API.
        </p>
      </div>

      <div className="rounded-[var(--radius-xl)] border border-border bg-card p-4 text-card-foreground">
        <CreateForm onCreated={handleCreated} />
      </div>

      <SpritzList ref={listRef} />
    </div>
  );
}
