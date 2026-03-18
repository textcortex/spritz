import { useRef } from 'react';
import { CreateForm } from '@/components/create-form';
import { SpritzList, type SpritzListHandle } from '@/components/spritz-list';

export function CreatePage() {
  const listRef = useRef<SpritzListHandle>(null);

  const handleCreated = () => {
    listRef.current?.refresh();
  };

  return (
    <div className="mx-auto max-w-[960px] space-y-8 px-6 py-12 md:px-12">
      <div>
        <h2 className="text-2xl font-semibold">Create Spritz</h2>
        <p className="mt-1 text-[15px] text-muted-foreground">
          Spin up an ephemeral dev Spritz managed by API.
        </p>
      </div>

      <div className="rounded-[20px] border border-[#e5e5e5] bg-card p-8 text-card-foreground dark:border-border">
        <CreateForm onCreated={handleCreated} />
      </div>

      <SpritzList ref={listRef} />
    </div>
  );
}
