import { useState, useEffect, useRef, type CSSProperties } from 'react';
import { useParams, Link } from 'react-router-dom';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import { useConfig } from '@/lib/config';
import { getAuthToken } from '@/lib/api';
import { buildTerminalTheme } from '@/lib/branding';
import { resolveWebSocketConnect } from '@/lib/connect-ticket';
import { chatPath } from '@/lib/urls';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';
import { ArrowLeftIcon } from 'lucide-react';

type ConnectionStatus = 'connecting' | 'connected' | 'disconnected' | 'error';

export function TerminalPage() {
  const { name } = useParams<{ name: string }>();
  const config = useConfig();
  const terminalTheme = buildTerminalTheme(config.branding.terminal);
  const terminalRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');

  useEffect(() => {
    if (!name || !terminalRef.current) return;
    const instanceName = name;
    let disposed = false;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: terminalTheme.background,
        foreground: terminalTheme.foreground,
        cursor: terminalTheme.cursor,
      },
    });
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(terminalRef.current);
    fitAddon.fit();
    xtermRef.current = term;
    fitAddonRef.current = fitAddon;

    function scheduleReconnect() {
      if (disposed) return;
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      term.write('\r\n\x1b[33m--- Connection closed. Reconnecting in 3s... ---\x1b[0m\r\n');
      reconnectTimerRef.current = setTimeout(() => {
        void connect();
      }, 3000);
    }

    async function connect() {
      if (disposed) return;
      setStatus('connecting');
      const session = new URLSearchParams(window.location.search).get('session') || undefined;
      const { wsUrl, protocols } = await resolveWebSocketConnect({
        apiBaseUrl: config.apiBaseUrl,
        websocketBaseUrl: config.websocketBaseUrl,
        directConnectPath: `/spritzes/${encodeURIComponent(instanceName)}/terminal${session ? `?session=${encodeURIComponent(session)}` : ''}`,
        ticketPath: `/spritzes/${encodeURIComponent(instanceName)}/terminal/connect-ticket`,
        useConnectTicket: Boolean(getAuthToken()),
        ticketBody: session ? { session } : undefined,
      });
      const ws = protocols.length ? new WebSocket(wsUrl, protocols) : new WebSocket(wsUrl);
      ws.binaryType = 'arraybuffer';
      wsRef.current = ws;
      let opened = false;

      ws.onopen = () => {
        opened = true;
        setStatus('connected');
        const dims = fitAddon.proposeDimensions();
        const cols = dims?.cols ?? 80;
        const rows = dims?.rows ?? 24;
        ws.send(JSON.stringify({ type: 'resize', cols, rows }));
      };

      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data));
        } else {
          term.write(event.data);
        }
      };

      ws.onclose = () => {
        if (wsRef.current === ws) {
          wsRef.current = null;
        }
        if (disposed) return;
        if (!opened) {
          setStatus('disconnected');
          if (reconnectTimerRef.current) {
            clearTimeout(reconnectTimerRef.current);
            reconnectTimerRef.current = null;
          }
          queueMicrotask(() => {
            void connect();
          });
          return;
        }
        setStatus('disconnected');
        scheduleReconnect();
      };

      ws.onerror = () => {
        setStatus('error');
      };
    }

    void connect();

    const inputDisposable = term.onData((data) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(data);
      }
    });

    const binaryDisposable = term.onBinary((data) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        const buffer = new Uint8Array(data.length);
        for (let i = 0; i < data.length; i++) buffer[i] = data.charCodeAt(i);
        wsRef.current.send(buffer);
      }
    });

    const resizeDisposable = term.onResize(({ cols, rows }) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });

    const handleWindowResize = () => fitAddon.fit();
    window.addEventListener('resize', handleWindowResize);

    return () => {
      disposed = true;
      inputDisposable.dispose();
      binaryDisposable.dispose();
      resizeDisposable.dispose();
      window.removeEventListener('resize', handleWindowResize);
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      wsRef.current?.close();
      term.dispose();
      xtermRef.current = null;
      fitAddonRef.current = null;
      wsRef.current = null;
    };
  }, [
    name,
    config.apiBaseUrl,
    config.websocketBaseUrl,
    terminalTheme.background,
    terminalTheme.cursor,
    terminalTheme.foreground,
  ]);

  if (!name) {
    return (
      <div className="flex items-center justify-center p-6">
        <p className="text-muted-foreground">No instance specified.</p>
      </div>
    );
  }

  return (
    <div
      className="flex h-full flex-col bg-[var(--terminal-shell-background)] text-[var(--terminal-shell-foreground)]"
      style={
        {
          '--terminal-shell-background': terminalTheme.background,
          '--terminal-shell-foreground': terminalTheme.foreground,
          '--terminal-shell-border': `color-mix(in srgb, ${terminalTheme.foreground} 16%, transparent)`,
        } as CSSProperties
      }
    >
      <div className="flex items-center justify-between border-b border-[var(--terminal-shell-border)] px-4 py-2">
        <div className="flex items-center gap-3">
          <Link to={chatPath(name)}>
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5 border-[var(--terminal-shell-border)] bg-transparent text-[var(--terminal-shell-foreground)] hover:bg-white/10 hover:text-[var(--terminal-shell-foreground)]"
            >
              <ArrowLeftIcon className="size-3.5" />
              Back
            </Button>
          </Link>
          <code
            className="text-sm"
            style={{ color: `color-mix(in srgb, ${terminalTheme.foreground} 70%, transparent)` }}
          >
            {name}
          </code>
        </div>
        <div className="flex items-center gap-2">
          <span
            className={cn(
              'inline-block size-2 rounded-full',
              status === 'connected' && 'bg-green-500',
              status === 'connecting' && 'animate-pulse bg-yellow-500',
              status === 'disconnected' && 'bg-muted-foreground',
              status === 'error' && 'bg-red-500',
            )}
          />
          <span
            className="text-xs capitalize"
            style={{ color: `color-mix(in srgb, ${terminalTheme.foreground} 70%, transparent)` }}
          >
            {status}
          </span>
        </div>
      </div>
      <div ref={terminalRef} className="flex-1 overflow-hidden p-1" />
    </div>
  );
}
