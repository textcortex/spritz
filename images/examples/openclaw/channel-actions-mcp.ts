#!/usr/bin/env node

type JsonRpcRequest = {
  jsonrpc?: string;
  id?: string | number | null;
  method?: string;
  params?: unknown;
};

type ToolCallParams = {
  name?: string;
  arguments?: {
    teamId?: string;
    channelId?: string;
    messageTs?: string;
    reaction?: string;
    remove?: boolean;
  };
};

const serverInfo = {
  name: "spritz-channel-actions",
  version: "0.1.0",
};

function send(message: Record<string, unknown>) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function sendResult(id: JsonRpcRequest["id"], result: Record<string, unknown>) {
  send({ jsonrpc: "2.0", id, result });
}

function sendError(id: JsonRpcRequest["id"], code: number, message: string) {
  send({ jsonrpc: "2.0", id: id ?? null, error: { code, message } });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function normalizeReaction(value: string | undefined): string {
  const trimmed = (value ?? "").trim();
  if (trimmed === "👀") {
    return "eyes";
  }
  return trimmed.replace(/^:+|:+$/g, "");
}

function readConfiguredEnv(name: string): string {
  const value = (process.env[name] ?? "").trim();
  if (/^\$\{[A-Z0-9_]+\}$/.test(value)) {
    return "";
  }
  return value;
}

async function callChannelAction(args: NonNullable<ToolCallParams["arguments"]>) {
  const baseUrl = readConfiguredEnv("SPRITZ_CHANNEL_ACTIONS_BASE_URL").replace(/\/+$/, "");
  const token = readConfiguredEnv("SPRITZ_CHANNEL_ACTIONS_TOKEN");
  if (!baseUrl || !token) {
    throw new Error("Spritz channel action endpoint is not configured.");
  }
  const response = await fetch(`${baseUrl}/internal/channel-actions/slack/reactions`, {
    method: "POST",
    headers: {
      authorization: `Bearer ${token}`,
      "content-type": "application/json",
    },
    body: JSON.stringify({
      teamId: (args.teamId ?? "").trim(),
      channelId: (args.channelId ?? "").trim(),
      messageTs: (args.messageTs ?? "").trim(),
      reaction: normalizeReaction(args.reaction),
      remove: Boolean(args.remove),
    }),
  });
  if (!response.ok) {
    const body = await response.text().catch(() => "");
    throw new Error(`Spritz channel action failed: ${response.status} ${body.trim()}`.trim());
  }
}

async function handleRequest(request: JsonRpcRequest) {
  if (!request.method) {
    sendError(request.id, -32600, "method is required");
    return;
  }
  switch (request.method) {
    case "initialize":
      sendResult(request.id, {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo,
      });
      return;
    case "notifications/initialized":
      return;
    case "tools/list":
      sendResult(request.id, {
        tools: [
          {
            name: "slack_react_message",
            description:
              "Add or remove a Slack reaction on an existing message visible to this channel gateway.",
            inputSchema: {
              type: "object",
              additionalProperties: false,
              properties: {
                teamId: {
                  type: "string",
                  description: "Slack workspace/team id from the channel context.",
                },
                channelId: {
                  type: "string",
                  description: "Slack channel id from the channel context.",
                },
                messageTs: {
                  type: "string",
                  description: "Slack message timestamp, usually message_ts from the channel context.",
                },
                reaction: {
                  type: "string",
                  description: "Slack reaction name, for example eyes, white_check_mark, or :eyes:.",
                },
                remove: {
                  type: "boolean",
                  description: "Set true to remove the reaction instead of adding it.",
                },
              },
              required: ["teamId", "channelId", "messageTs", "reaction"],
            },
          },
        ],
      });
      return;
    case "tools/call": {
      const params = isRecord(request.params) ? (request.params as ToolCallParams) : {};
      if (params.name !== "slack_react_message") {
        sendError(request.id, -32602, "unknown tool");
        return;
      }
      const args = params.arguments ?? {};
      const reaction = normalizeReaction(args.reaction);
      if (!args.teamId?.trim() || !args.channelId?.trim() || !args.messageTs?.trim() || !reaction) {
        sendError(request.id, -32602, "teamId, channelId, messageTs, and reaction are required");
        return;
      }
      try {
        await callChannelAction({ ...args, reaction });
        sendResult(request.id, {
          content: [
            {
              type: "text",
              text: args.remove ? "Reaction removed." : "Reaction added.",
            },
          ],
        });
      } catch (error) {
        sendResult(request.id, {
          isError: true,
          content: [
            {
              type: "text",
              text: error instanceof Error ? error.message : String(error),
            },
          ],
        });
      }
      return;
    }
    default:
      sendError(request.id, -32601, "method not found");
  }
}

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  for (;;) {
    const newline = buffer.indexOf("\n");
    if (newline < 0) {
      break;
    }
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    if (!line) {
      continue;
    }
    let request: JsonRpcRequest;
    try {
      request = JSON.parse(line);
    } catch {
      sendError(null, -32700, "parse error");
      continue;
    }
    void handleRequest(request);
  }
});
