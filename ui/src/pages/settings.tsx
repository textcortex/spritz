import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Link, Navigate, NavLink, Outlet, Route, Routes, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  CheckCircle2Icon,
  MessageSquareIcon,
  PlugIcon,
  SaveIcon,
  SendIcon,
  SettingsIcon,
  Trash2Icon,
} from 'lucide-react';
import { toast } from 'sonner';
import { BrandHeader } from '@/components/brand-header';
import { Badge } from '@/components/ui/badge';
import { Button, buttonVariants } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import { cn } from '@/lib/utils';
import {
  connectionRoutePath,
  installationRoutePath,
  primaryConnection,
  slackGatewayRequest,
  slackGatewayPath,
  type SlackInstallResult,
  type SlackInstallSelection,
  type SlackInstallTarget,
  type SlackManagedChannelRoute,
  type SlackManagedConnection,
  type SlackManagedInstallation,
  type SlackWorkspaceTestResult,
} from '@/lib/slack-management';

interface InstallationListResponse {
  status: string;
  installations: SlackManagedInstallation[];
}

interface InstallationDetailResponse {
  status: string;
  installation: SlackManagedInstallation;
  connection?: SlackManagedConnection;
}

interface TargetListResponse {
  status: string;
  teamId: string;
  requestId: string;
  targets: SlackInstallTarget[];
}

function useAsyncValue<T>(load: () => Promise<T>, deps: unknown[]) {
  const [value, setValue] = useState<T | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const requestIDRef = useRef(0);

  const reload = useCallback(async () => {
    const requestID = requestIDRef.current + 1;
    requestIDRef.current = requestID;
    setLoading(true);
    setError('');
    setValue(null);
    try {
      const nextValue = await load();
      if (requestID !== requestIDRef.current) return;
      setValue(nextValue);
    } catch (err) {
      if (requestID !== requestIDRef.current) return;
      setValue(null);
      setError(err instanceof Error ? err.message : 'Request failed.');
    } finally {
      if (requestID === requestIDRef.current) {
        setLoading(false);
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  useEffect(() => {
    reload();
  }, [reload]);

  return { value, error, loading, reload };
}

function SettingsShell() {
  const navItems = [
    { href: '/', label: 'Chat', icon: MessageSquareIcon },
    { href: '/settings/slack/workspaces', label: 'Slack workspaces', icon: PlugIcon },
    { href: '/settings/slack/channels', label: 'Channel routing', icon: SettingsIcon },
  ];

  return (
    <div className="flex min-h-dvh w-full min-w-0 flex-col overflow-x-hidden bg-background md:grid md:grid-cols-[240px_minmax(0,1fr)]">
      <aside className="min-w-0 border-b border-border bg-sidebar px-4 py-4 md:border-b-0 md:border-r">
        <div className="mb-5">
          <BrandHeader />
        </div>
        <nav className="flex gap-2 overflow-x-auto md:flex-col md:overflow-visible">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.href}
                to={item.href}
                end={item.href === '/'}
                className={({ isActive }) =>
                  cn(
                    'inline-flex h-9 shrink-0 items-center gap-2 rounded-[var(--radius-md)] px-3 text-sm font-medium text-sidebar-foreground transition-colors hover:bg-sidebar-accent',
                    isActive && 'bg-sidebar-accent',
                  )
                }
              >
                <Icon aria-hidden="true" className="size-4" />
                {item.label}
              </NavLink>
            );
          })}
        </nav>
      </aside>
      <section className="min-w-0 w-full">
        <Outlet />
      </section>
    </div>
  );
}

function PageFrame({
  title,
  description,
  children,
  action,
}: {
  title: string;
  description?: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-5 px-4 py-5 md:px-7">
      <header className="flex min-h-12 items-start justify-between gap-4 border-b border-border pb-4">
        <div className="min-w-0">
          <h1 className="m-0 text-xl font-semibold tracking-normal">{title}</h1>
          {description && (
            <p className="m-0 mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p>
          )}
        </div>
        {action}
      </header>
      {children}
    </div>
  );
}

function LoadingRows() {
  return (
    <div className="grid gap-2">
      <Skeleton className="h-16 w-full rounded-[var(--radius-lg)]" />
      <Skeleton className="h-16 w-full rounded-[var(--radius-lg)]" />
      <Skeleton className="h-16 w-full rounded-[var(--radius-lg)]" />
    </div>
  );
}

function ErrorBanner({ message }: { message: string }) {
  if (!message) return null;
  return (
    <div className="flex items-start gap-2 rounded-[var(--radius-lg)] border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
      <AlertTriangleIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <span>{message}</span>
    </div>
  );
}

function StatusBadge({ value }: { value?: string }) {
  const normalized = String(value || '').trim() || 'unknown';
  return (
    <Badge variant={normalized === 'ready' ? 'secondary' : 'outline'}>
      {normalized}
    </Badge>
  );
}

function targetName(installation: SlackManagedInstallation): string {
  return installation.currentTarget?.profile?.name || 'No saved target';
}

function teamID(installation: SlackManagedInstallation): string {
  return installation.route.externalTenantId;
}

function hasAllowedAction(installation: SlackManagedInstallation, action: string): boolean {
  return (installation.allowedActions || []).some(
    (candidate) => candidate.trim().toLowerCase() === action.trim().toLowerCase(),
  );
}

function installationIsDisconnected(installation: SlackManagedInstallation): boolean {
  return installation.state.trim().toLowerCase() === 'disconnected';
}

function connectionName(connection: SlackManagedConnection): string {
  return connection.displayName?.trim() || connection.id || 'Default connection';
}

function WorkspaceListPage() {
  const { value, error, loading, reload } = useAsyncValue(
    () => slackGatewayRequest<InstallationListResponse>('/api/slack/workspaces'),
    [],
  );
  const installations = value?.installations || [];

  const disconnect = async (installation: SlackManagedInstallation) => {
    if (!window.confirm(`Disconnect ${teamID(installation)}?`)) return;
    try {
      await slackGatewayRequest('/api/slack/workspaces/disconnect', {
        method: 'POST',
        body: JSON.stringify({ teamId: teamID(installation) }),
      });
      toast.success('Workspace disconnected.');
      reload();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to disconnect workspace.');
    }
  };

  return (
    <PageFrame
      title="Slack Workspaces"
      description="Manage workspace-level Slack app installations and their selected targets."
      action={
        <a href={slackGatewayPath('/slack/install')} className={buttonVariants({ size: 'lg' })}>
          <PlugIcon aria-hidden="true" />
          Install
        </a>
      }
    >
      <ErrorBanner message={error} />
      {loading ? (
        <LoadingRows />
      ) : installations.length === 0 ? (
        <EmptyState text="No Slack workspaces are installed." />
      ) : (
        <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
          {installations.map((installation) => {
            const connection = primaryConnection(installation);
            const canDisconnect = hasAllowedAction(installation, 'disconnect');
            const canReconnect = hasAllowedAction(installation, 'reconnect');
            const canTest = !installationIsDisconnected(installation);
            return (
              <div
                key={installation.id || teamID(installation)}
                className="grid min-w-0 gap-3 overflow-hidden border-b border-border px-4 py-3 last:border-b-0 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center"
              >
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="m-0 truncate text-sm font-semibold">{teamID(installation)}</h2>
                    <StatusBadge value={installation.state} />
                  </div>
                  <p className="m-0 mt-1 truncate text-sm text-muted-foreground">
                    Target: {targetName(installation)}
                    {installation.currentTarget?.ownerLabel ? ` · ${installation.currentTarget.ownerLabel}` : ''}
                  </p>
                </div>
                <div className="grid min-w-0 grid-cols-2 gap-2 sm:flex sm:flex-wrap">
                  {connection && (
                    <Link
                      to={connectionRoutePath(installation.id, connection.id)}
                      className={cn(buttonVariants({ variant: 'outline' }), 'w-full sm:w-auto')}
                    >
                      <SettingsIcon aria-hidden="true" />
                      Channels
                    </Link>
                  )}
                  <Link
                    to={`/settings/slack/workspaces/target?teamId=${encodeURIComponent(teamID(installation))}`}
                    className={cn(buttonVariants({ variant: 'outline' }), 'w-full sm:w-auto')}
                  >
                    Target
                  </Link>
                  {canReconnect && (
                    <a
                      href={slackGatewayPath('/slack/install')}
                      className={cn(buttonVariants({ variant: 'outline' }), 'w-full sm:w-auto')}
                    >
                      <PlugIcon aria-hidden="true" />
                      Reconnect
                    </a>
                  )}
                  {canTest && (
                    <Link
                      to={`/settings/slack/workspaces/test?teamId=${encodeURIComponent(teamID(installation))}`}
                      className={cn(buttonVariants({ variant: 'outline' }), 'w-full sm:w-auto')}
                    >
                      <SendIcon aria-hidden="true" />
                      Test
                    </Link>
                  )}
                  {canDisconnect && (
                    <Button
                      variant="destructive"
                      className="w-full sm:w-auto"
                      onClick={() => disconnect(installation)}
                    >
                      <Trash2Icon aria-hidden="true" />
                      Disconnect
                    </Button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </PageFrame>
  );
}

function EmptyState({ text }: { text: string }) {
  return (
    <div className="rounded-[var(--radius-lg)] border border-dashed border-border px-4 py-10 text-center text-sm text-muted-foreground">
      {text}
    </div>
  );
}

function ChannelListPage() {
  const { value, error, loading } = useAsyncValue(
    () => slackGatewayRequest<InstallationListResponse>('/api/settings/channels'),
    [],
  );
  const installations = value?.installations || [];
  const connectionRows = installations.flatMap((installation) =>
    (installation.connections || []).map((connection) => ({ installation, connection })),
  );

  return (
    <PageFrame
      title="Channel Routing"
      description="Set which Slack channels can relay messages without mentioning the bot."
    >
      <ErrorBanner message={error} />
      {loading ? (
        <LoadingRows />
      ) : installations.length === 0 ? (
        <EmptyState text="No channel-capable Slack workspace installs were found." />
      ) : connectionRows.length === 0 ? (
        <EmptyState text="No channel connections are available for these Slack workspace installs." />
      ) : (
        <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
          {connectionRows.map(({ installation, connection }) => (
            <Link
              key={`${installation.id}:${connection.id}`}
              to={connectionRoutePath(installation.id, connection.id)}
              className="grid gap-1 border-b border-border px-4 py-3 text-sm transition-colors last:border-b-0 hover:bg-muted/60"
            >
              <span className="font-medium">{teamID(installation)}</span>
              <span className="text-muted-foreground">{connectionName(connection)}</span>
              <span className="text-muted-foreground">
                {connection.routes?.length || 0} configured channel{(connection.routes?.length || 0) === 1 ? '' : 's'}
              </span>
            </Link>
          ))}
        </div>
      )}
    </PageFrame>
  );
}

function InstallationPage() {
  const { installationId = '' } = useParams();
  const { value, error, loading } = useAsyncValue(
    () => slackGatewayRequest<InstallationDetailResponse>(
      `/api/settings/channels/installations/${encodeURIComponent(installationId)}`,
    ),
    [installationId],
  );
  const installation = value?.installation;
  const connections = installation?.connections || [];

  return (
    <PageFrame
      title="Slack Workspace"
      action={
        <Link to="/settings/slack/channels" className={buttonVariants({ variant: 'outline' })}>
          <ArrowLeftIcon aria-hidden="true" />
          Back
        </Link>
      }
    >
      <ErrorBanner message={error} />
      {loading ? (
        <LoadingRows />
      ) : !installation ? (
        <EmptyState text="Workspace installation was not found." />
      ) : connections.length === 0 ? (
        <EmptyState text="No channel connections are available for this Slack workspace install." />
      ) : (
        <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
          {connections.map((connection) => (
            <Link
              key={connection.id}
              to={connectionRoutePath(installation.id, connection.id)}
              className="flex items-center justify-between gap-3 border-b border-border px-4 py-3 text-sm last:border-b-0 hover:bg-muted/60"
            >
              <span>
                <span className="block font-medium">{connectionName(connection)}</span>
                <span className="text-muted-foreground">{connection.id}</span>
              </span>
              <StatusBadge value={connection.state} />
            </Link>
          ))}
        </div>
      )}
    </PageFrame>
  );
}

function routeRequireMention(route: SlackManagedChannelRoute): boolean {
  return route.requireMention !== false;
}

function routeEnabled(route: SlackManagedChannelRoute): boolean {
  return route.enabled !== false;
}

function routePolicyPayload(route: SlackManagedChannelRoute) {
  const policy: {
    externalChannelId: string;
    externalChannelType?: string;
    requireMention: boolean;
  } = {
    externalChannelId: route.externalChannelId,
    requireMention: routeRequireMention(route),
  };
  const channelType = String(route.externalChannelType || '').trim();
  if (channelType) {
    policy.externalChannelType = channelType;
  }
  return policy;
}

function ConnectionSettingsPage() {
  const { installationId = '', connectionId = '' } = useParams();
  const loadPath = `/api/settings/channels/installations/${encodeURIComponent(
    installationId,
  )}/connections/${encodeURIComponent(connectionId)}`;
  const { value, error, loading, reload } = useAsyncValue(
    () => slackGatewayRequest<InstallationDetailResponse>(loadPath),
    [loadPath],
  );
  const [routes, setRoutes] = useState<SlackManagedChannelRoute[]>([]);
  const [channelId, setChannelId] = useState('');
  const [channelType, setChannelType] = useState('channel');
  const [requireMention, setRequireMention] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (value?.connection) {
      setRoutes([...(value.connection.routes || [])].filter(routeEnabled));
    }
  }, [value?.connection]);

  const saveRoutes = async (nextRoutes: SlackManagedChannelRoute[]) => {
    setSaving(true);
    try {
      await slackGatewayRequest(loadPath, {
        method: 'PUT',
        body: JSON.stringify({
          channelPolicies: nextRoutes.map(routePolicyPayload),
        }),
      });
      toast.success('Channel settings saved.');
      reload();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to save channel settings.');
    } finally {
      setSaving(false);
    }
  };

  const addRoute = async () => {
    const normalized = channelId.trim();
    if (!normalized) return;
    const nextRoutes = [
      ...routes.filter((route) => route.externalChannelId !== normalized),
      {
        id: normalized,
        externalChannelId: normalized,
        externalChannelType: channelType,
        requireMention,
        enabled: true,
      },
    ].sort((left, right) => left.externalChannelId.localeCompare(right.externalChannelId));
    setChannelId('');
    setRequireMention(true);
    await saveRoutes(nextRoutes);
  };

  const updateRoute = async (externalChannelId: string, nextRequireMention: boolean) => {
    await saveRoutes(
      routes.map((route) =>
        route.externalChannelId === externalChannelId
          ? { ...route, requireMention: nextRequireMention }
          : route,
      ),
    );
  };

  const deleteRoute = async (externalChannelId: string) => {
    if (!window.confirm(`Remove channel route ${externalChannelId}?`)) return;
    await saveRoutes(routes.filter((route) => route.externalChannelId !== externalChannelId));
  };

  const installation = value?.installation;
  const connection = value?.connection;

  return (
    <PageFrame
      title="Channel Settings"
      description={installation ? `${teamID(installation)} · ${connection?.displayName || connection?.id || 'connection'}` : undefined}
      action={
        <Link
          to={installationId ? installationRoutePath(installationId) : '/settings/slack/channels'}
          className={buttonVariants({ variant: 'outline' })}
        >
          <ArrowLeftIcon aria-hidden="true" />
          Back
        </Link>
      }
    >
      <ErrorBanner message={error} />
      {loading ? (
        <LoadingRows />
      ) : !installation || !connection ? (
        <EmptyState text="Channel connection was not found." />
      ) : (
        <div className="grid gap-5">
          <section className="grid gap-3 rounded-[var(--radius-lg)] border border-border p-4">
            <h2 className="m-0 text-sm font-semibold">Add Channel</h2>
            <div className="grid gap-3 md:grid-cols-[minmax(160px,1fr)_160px_180px_auto] md:items-end">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Channel ID</span>
                <Input value={channelId} onChange={(event) => setChannelId(event.target.value)} placeholder="C12345678" />
              </label>
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Type</span>
                <Input value={channelType} onChange={(event) => setChannelType(event.target.value)} />
              </label>
              <label className="flex h-8 items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={!requireMention}
                  onChange={(event) => setRequireMention(!event.target.checked)}
                />
                Relay without mention
              </label>
              <Button onClick={addRoute} disabled={saving || !channelId.trim()}>
                <SaveIcon aria-hidden="true" />
                Save
              </Button>
            </div>
          </section>

          <section className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
            {routes.length === 0 ? (
              <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                No channel overrides are configured.
              </div>
            ) : (
              routes.map((route) => {
                const requiresMention = routeRequireMention(route);
                return (
                  <div
                    key={route.externalChannelId}
                    className="grid gap-3 border-b border-border px-4 py-3 last:border-b-0 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center"
                  >
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{route.externalChannelId}</span>
                        <Badge variant={requiresMention ? 'outline' : 'secondary'}>
                          {requiresMention ? 'Mention required' : 'No mention required'}
                        </Badge>
                      </div>
                      <p className="m-0 mt-1 text-sm text-muted-foreground">{route.externalChannelType || 'channel'}</p>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        variant="outline"
                        disabled={saving}
                        onClick={() => updateRoute(route.externalChannelId, !requiresMention)}
                      >
                        {requiresMention ? 'Disable mention' : 'Require mention'}
                      </Button>
                      <Button
                        variant="destructive"
                        disabled={saving}
                        onClick={() => deleteRoute(route.externalChannelId)}
                      >
                        <Trash2Icon aria-hidden="true" />
                        Remove
                      </Button>
                    </div>
                  </div>
                );
              })
            )}
          </section>
        </div>
      )}
    </PageFrame>
  );
}

function WorkspaceTargetPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const teamId = params.get('teamId') || '';
  const { value, error, loading } = useAsyncValue(
    () => slackGatewayRequest<TargetListResponse>(`/api/slack/workspaces/target?teamId=${encodeURIComponent(teamId)}`),
    [teamId],
  );
  const [selected, setSelected] = useState('');
  const [saving, setSaving] = useState(false);
  const targets = value?.targets || [];

  useEffect(() => {
    if (!selected && targets[0]) setSelected(targets[0].id);
  }, [selected, targets]);

  const selectedTarget = targets.find((target) => target.id === selected);
  const save = async () => {
    if (!selectedTarget) return;
    setSaving(true);
    try {
      await slackGatewayRequest('/api/slack/workspaces/target', {
        method: 'POST',
        body: JSON.stringify({
          teamId,
          requestId: value?.requestId,
          presetInputs: selectedTarget.presetInputs,
        }),
      });
      toast.success('Workspace target updated.');
      navigate('/settings/slack/workspaces');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to update target.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <PageFrame
      title="Workspace Target"
      description={teamId}
      action={<BackToWorkspaces />}
    >
      <ErrorBanner message={error} />
      {loading ? (
        <LoadingRows />
      ) : targets.length === 0 ? (
        <EmptyState text="No targets are available for this workspace." />
      ) : (
        <TargetPicker targets={targets} selected={selected} onSelect={setSelected} />
      )}
      <div>
        <Button onClick={save} disabled={saving || !selectedTarget}>
          <SaveIcon aria-hidden="true" />
          Save target
        </Button>
      </div>
    </PageFrame>
  );
}

function TargetPicker({
  targets,
  selected,
  onSelect,
}: {
  targets: SlackInstallTarget[];
  selected: string;
  onSelect: (id: string) => void;
}) {
  return (
    <div className="overflow-hidden rounded-[var(--radius-lg)] border border-border">
      {targets.map((target) => (
        <label
          key={target.id}
          className="grid cursor-pointer grid-cols-[auto_1fr] gap-3 border-b border-border px-4 py-3 last:border-b-0 hover:bg-muted/60"
        >
          <input
            type="radio"
            className="mt-1"
            checked={selected === target.id}
            onChange={() => onSelect(target.id)}
          />
          <span className="min-w-0">
            <span className="block truncate text-sm font-medium">{target.profile.name || target.id}</span>
            {target.ownerLabel && (
              <span className="block truncate text-sm text-muted-foreground">{target.ownerLabel}</span>
            )}
          </span>
        </label>
      ))}
    </div>
  );
}

function BackToWorkspaces() {
  return (
    <Link to="/settings/slack/workspaces" className={buttonVariants({ variant: 'outline' })}>
      <ArrowLeftIcon aria-hidden="true" />
      Back
    </Link>
  );
}

function WorkspaceTestPage() {
  const [params] = useSearchParams();
  const teamId = params.get('teamId') || '';
  const [channelId, setChannelId] = useState('');
  const [threadTs, setThreadTs] = useState('');
  const [prompt, setPrompt] = useState(`spritz-slack-smoke-${Math.floor(Date.now() / 1000)}`);
  const [dryRun, setDryRun] = useState(false);
  const [result, setResult] = useState<SlackWorkspaceTestResult | null>(null);
  const [sending, setSending] = useState(false);

  const send = async () => {
    setSending(true);
    setResult(null);
    try {
      setResult(await slackGatewayRequest<SlackWorkspaceTestResult>('/api/slack/workspaces/test', {
        method: 'POST',
        body: JSON.stringify({
          teamId,
          channelId,
          threadTs,
          prompt,
          mode: dryRun ? 'dry-run' : 'real',
        }),
      }));
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to send test message.');
    } finally {
      setSending(false);
    }
  };

  return (
    <PageFrame title="Test Slack Workspace" description={teamId} action={<BackToWorkspaces />}>
      <div className="grid max-w-2xl gap-4 rounded-[var(--radius-lg)] border border-border p-4">
        <label className="grid gap-1 text-sm">
          <span className="text-muted-foreground">Channel ID</span>
          <Input value={channelId} onChange={(event) => setChannelId(event.target.value)} placeholder="C12345678" />
        </label>
        <label className="grid gap-1 text-sm">
          <span className="text-muted-foreground">Thread timestamp</span>
          <Input value={threadTs} onChange={(event) => setThreadTs(event.target.value)} placeholder="Optional" />
        </label>
        <label className="grid gap-1 text-sm">
          <span className="text-muted-foreground">Message</span>
          <Input value={prompt} onChange={(event) => setPrompt(event.target.value)} />
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={dryRun} onChange={(event) => setDryRun(event.target.checked)} />
          Dry run
        </label>
        <div>
          <Button onClick={send} disabled={sending || !teamId || !channelId.trim() || !prompt.trim()}>
            <SendIcon aria-hidden="true" />
            Send test
          </Button>
        </div>
      </div>
      {result && (
        <div className="grid max-w-2xl gap-2 rounded-[var(--radius-lg)] border border-border p-4 text-sm">
          <div className="flex items-center gap-2 font-medium">
            <CheckCircle2Icon aria-hidden="true" className="size-4" />
            Outcome: {result.outcome || 'unknown'}
          </div>
          {result.reply && <p className="m-0 text-muted-foreground">{result.reply}</p>}
          {result.conversationId && (
            <p className="m-0 text-muted-foreground">Conversation: {result.conversationId}</p>
          )}
        </div>
      )}
    </PageFrame>
  );
}

function InstallSelectPage() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const requestId = (params.get('requestId') || '').trim();
  const selectionPath = requestId
    ? `/api/slack/install/selection?requestId=${encodeURIComponent(requestId)}`
    : '/api/slack/install/selection';
  const { value, error, loading } = useAsyncValue(
    () => slackGatewayRequest<SlackInstallSelection>(selectionPath),
    [selectionPath],
  );
  const [selected, setSelected] = useState('');
  const [saving, setSaving] = useState(false);
  const targets = value?.targets || [];

  useEffect(() => {
    if (!selected && targets[0]) setSelected(targets[0].id);
  }, [selected, targets]);

  const selectedTarget = useMemo(
    () => targets.find((target) => target.id === selected),
    [selected, targets],
  );

  const submit = async () => {
    if (!value || !selectedTarget) return;
    setSaving(true);
    try {
      const result = await slackGatewayRequest<
        SlackInstallResult | { requestId: string; teamId: string }
      >('/api/slack/install/selection', {
        method: 'POST',
        body: JSON.stringify({
          requestId: value.requestId,
          presetInputs: selectedTarget.presetInputs,
        }),
      });
      if (isSlackInstallResult(result)) {
        navigate(`/settings/slack/install/result?${installResultQueryString(result)}`);
        return;
      }
      navigate(
        `/settings/slack/install/result?status=success&code=installed&provider=slack&operation=channel.install&requestId=${encodeURIComponent(result.requestId)}&teamId=${encodeURIComponent(result.teamId)}`,
      );
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to complete install.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <PageFrame title="Choose Install Target" description={value?.teamId}>
      <ErrorBanner message={error} />
      {loading ? (
        <LoadingRows />
      ) : targets.length === 0 ? (
        <EmptyState text="No install targets are available." />
      ) : (
        <TargetPicker targets={targets} selected={selected} onSelect={setSelected} />
      )}
      <div>
        <Button onClick={submit} disabled={saving || !selectedTarget}>
          Continue
        </Button>
      </div>
    </PageFrame>
  );
}

function installResultFromQuery(params: URLSearchParams): SlackInstallResult | null {
  const status = (params.get('status') || '').trim();
  const code = (params.get('code') || '').trim();
  if (!status && !code) return null;

  const successful = status === 'success';
  return {
    status: status || 'error',
    code: code || 'unknown',
    operation: params.get('operation') || undefined,
    provider: params.get('provider') || undefined,
    requestId: params.get('requestId') || undefined,
    teamId: params.get('teamId') || undefined,
    title: params.get('title') || (successful ? 'Slack connected' : 'Slack install needs attention'),
    message: params.get('message') || (
      successful
        ? 'The Slack workspace is connected.'
        : 'The Slack install flow finished without a detailed result message.'
    ),
    actionLabel: undefined,
    actionHref: undefined,
  };
}

function isSlackInstallResult(value: unknown): value is SlackInstallResult {
  return (
    !!value &&
    typeof value === 'object' &&
    typeof (value as SlackInstallResult).status === 'string' &&
    typeof (value as SlackInstallResult).code === 'string' &&
    typeof (value as SlackInstallResult).title === 'string' &&
    typeof (value as SlackInstallResult).message === 'string'
  );
}

function installResultQueryString(result: SlackInstallResult): string {
  const params = new URLSearchParams();
  params.set('status', result.status);
  params.set('code', result.code);
  if (result.operation) params.set('operation', result.operation);
  if (result.provider) params.set('provider', result.provider);
  if (result.requestId) params.set('requestId', result.requestId);
  if (result.teamId) params.set('teamId', result.teamId);
  if (result.title) params.set('title', result.title);
  if (result.message) params.set('message', result.message);
  if (typeof result.retryable === 'boolean') {
    params.set('retryable', String(result.retryable));
  }
  if (result.actionLabel) params.set('actionLabel', result.actionLabel);
  if (result.actionHref) params.set('actionHref', result.actionHref);
  return params.toString();
}

function InstallResultPage() {
  const [params] = useSearchParams();
  const query = params.toString();
  const fallbackResult = useMemo(() => installResultFromQuery(params), [params]);
  const { value, error, loading } = useAsyncValue(
    () => slackGatewayRequest<SlackInstallResult>(`/api/slack/install/result?${query}`),
    [query],
  );
  const result = (isSlackInstallResult(value) ? value : null) || fallbackResult;
  const errorMessage = result ? '' : error;

  return (
    <PageFrame title={result?.title || 'Slack Install'}>
      <ErrorBanner message={errorMessage} />
      {loading ? (
        <Skeleton className="h-24 w-full rounded-[var(--radius-lg)]" />
      ) : result ? (
        <div className="max-w-2xl rounded-[var(--radius-lg)] border border-border p-4">
          <div className="mb-3 flex items-center gap-2">
            {result.status === 'success' ? (
              <CheckCircle2Icon aria-hidden="true" className="size-5 text-primary" />
            ) : (
              <AlertTriangleIcon aria-hidden="true" className="size-5 text-destructive" />
            )}
            <Badge variant={result.status === 'success' ? 'secondary' : 'destructive'}>
              {result.code}
            </Badge>
          </div>
          <p className="m-0 text-sm text-muted-foreground">{result.message}</p>
          {result.requestId && (
            <p className="m-0 mt-4 text-xs text-muted-foreground">Request ID: {result.requestId}</p>
          )}
          {result.actionHref && (
            <a href={result.actionHref} className={cn(buttonVariants(), 'mt-4')}>
              {result.actionLabel || 'Continue'}
            </a>
          )}
        </div>
      ) : (
        <EmptyState text="Install result is unavailable." />
      )}
    </PageFrame>
  );
}

export function SettingsPage() {
  return (
    <Routes>
      <Route element={<SettingsShell />}>
        <Route index element={<Navigate to="slack/workspaces" replace />} />
        <Route path="slack" element={<Navigate to="workspaces" replace />} />
        <Route path="slack/workspaces" element={<WorkspaceListPage />} />
        <Route path="slack/workspaces/target" element={<WorkspaceTargetPage />} />
        <Route path="slack/workspaces/test" element={<WorkspaceTestPage />} />
        <Route path="slack/channels" element={<ChannelListPage />} />
        <Route path="slack/channels/installations/:installationId" element={<InstallationPage />} />
        <Route
          path="slack/channels/installations/:installationId/connections/:connectionId"
          element={<ConnectionSettingsPage />}
        />
        <Route path="slack/install/select" element={<InstallSelectPage />} />
        <Route path="slack/install/result" element={<InstallResultPage />} />
      </Route>
    </Routes>
  );
}
