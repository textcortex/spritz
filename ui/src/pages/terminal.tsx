import { useParams, Link } from 'react-router-dom';
import { Button } from '@/components/ui/button';

export function TerminalPage() {
  const { name } = useParams<{ name: string }>();

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
      <div className="flex flex-col gap-1">
        <h2 className="text-2xl font-bold">Terminal</h2>
        <p className="text-muted-foreground">
          Connected to: <code className="rounded bg-muted px-1.5 py-0.5 text-sm">{name}</code>
        </p>
      </div>
      <div className="rounded-lg border bg-card p-6 text-card-foreground">
        <p className="text-sm text-muted-foreground">Terminal will be implemented in Phase 5.</p>
      </div>
      <div>
        <Link to="/">
          <Button variant="outline">Back to Chat</Button>
        </Link>
      </div>
    </div>
  );
}
