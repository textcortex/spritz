#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const ACP_CLI_COMPAT_BASENAME = "spritz-acp-cli-compat.js";
export const ACP_COMPAT_BASENAME = "spritz-acp-compat.js";

function requireMatch(source, pattern, label) {
  const match = source.match(pattern);
  if (!match?.[1]) {
    throw new Error(`Failed to resolve ${label} from the installed OpenClaw ACP bundle.`);
  }
  return match[1];
}

function assertReadableFile(filePath, label) {
  if (!fs.existsSync(filePath)) {
    throw new Error(`${label} not found: ${filePath}`);
  }
}

export function resolveOpenclawPackageRoot(args = process.argv.slice(2), env = process.env) {
  const cliValue = args[0]?.trim();
  if (cliValue) {
    return path.resolve(cliValue);
  }
  const envValue = env.OPENCLAW_PACKAGE_ROOT?.trim();
  if (envValue) {
    return path.resolve(envValue);
  }
  return "/usr/local/lib/node_modules/openclaw";
}

export function selectAcpCliBundle(distDir) {
  const entry = fs
    .readdirSync(distDir)
    .filter((name) => /^acp-cli-.*\.js$/.test(name))
    .sort()[0];
  if (!entry) {
    throw new Error(`No acp-cli bundle found under ${distDir}`);
  }
  return entry;
}

export function resolveAcpCliDependencies(acpCliSource) {
  return {
    callBasename: requireMatch(
      acpCliSource,
      /import\s+\{[^}]*GatewayClient[^}]*buildGatewayConnectionDetails[^}]*\}\s+from\s+"\.\/([^"]+)"/,
      "GatewayClient/buildGatewayConnectionDetails bundle",
    ),
    connectionAuthBasename: requireMatch(
      acpCliSource,
      /import\s+\{[^}]*resolveGatewayConnectionAuth[^}]*\}\s+from\s+"\.\/([^"]+)"/,
      "resolveGatewayConnectionAuth bundle",
    ),
    messageChannelBasename: requireMatch(
      acpCliSource,
      /import\s+\{[^}]*GATEWAY_CLIENT_NAMES[^}]*GATEWAY_CLIENT_MODES[^}]*\}\s+from\s+"\.\/([^"]+)"/,
      "gateway client constants bundle",
    ),
  };
}

export function buildAcpCliCompatSource(acpCliSource) {
  if (!acpCliSource.includes("serveAcpGateway")) {
    throw new Error("Installed OpenClaw ACP bundle is missing serveAcpGateway.");
  }
  if (!acpCliSource.includes("AcpGatewayAgent")) {
    throw new Error("Installed OpenClaw ACP bundle is missing AcpGatewayAgent.");
  }
  return `${acpCliSource}\nexport { AcpGatewayAgent, serveAcpGateway };\n`;
}

export function buildCompatModuleSource(params) {
  return `import { loadConfig } from "./index.js";
import * as callModule from "./${params.callBasename}";
import * as connectionAuthModule from "./${params.connectionAuthBasename}";
import * as messageChannelModule from "./${params.messageChannelBasename}";
import {
  AcpGatewayAgent,
  registerAcpCli,
  serveAcpGateway,
} from "./${ACP_CLI_COMPAT_BASENAME}";

function pickNamedFunction(moduleNs, name) {
  for (const value of Object.values(moduleNs)) {
    if (typeof value === "function" && value.name === name) {
      return value;
    }
  }
  throw new Error(\`OpenClaw ACP compat export not found: \${name}\`);
}

function pickNamedObject(moduleNs, name) {
  for (const value of Object.values(moduleNs)) {
    if (value && typeof value === "object" && value.constructor === Object && name in value) {
      return value;
    }
  }
  throw new Error(\`OpenClaw ACP compat object not found: \${name}\`);
}

const GatewayClient = pickNamedFunction(callModule, "GatewayClient");
const buildGatewayConnectionDetails = pickNamedFunction(
  callModule,
  "buildGatewayConnectionDetails",
);
const resolveGatewayConnectionAuth = pickNamedFunction(
  connectionAuthModule,
  "resolveGatewayConnectionAuth",
);
const GATEWAY_CLIENT_NAMES = pickNamedObject(messageChannelModule, "CONTROL_UI");
const GATEWAY_CLIENT_MODES = pickNamedObject(messageChannelModule, "WEBCHAT");

export {
  AcpGatewayAgent,
  GatewayClient,
  GATEWAY_CLIENT_MODES,
  GATEWAY_CLIENT_NAMES,
  buildGatewayConnectionDetails,
  loadConfig,
  registerAcpCli,
  resolveGatewayConnectionAuth,
  serveAcpGateway,
};
`;
}

export function generateOpenclawAcpCompat(packageRoot) {
  const distDir = path.join(packageRoot, "dist");
  assertReadableFile(packageRoot, "OpenClaw package root");
  assertReadableFile(distDir, "OpenClaw dist directory");

  const acpCliBasename = selectAcpCliBundle(distDir);
  const acpCliPath = path.join(distDir, acpCliBasename);
  const acpCliSource = fs.readFileSync(acpCliPath, "utf8");
  const dependencies = resolveAcpCliDependencies(acpCliSource);

  const callPath = path.join(distDir, dependencies.callBasename);
  const connectionAuthPath = path.join(distDir, dependencies.connectionAuthBasename);
  const messageChannelPath = path.join(distDir, dependencies.messageChannelBasename);
  const indexPath = path.join(distDir, "index.js");

  assertReadableFile(callPath, "OpenClaw call bundle");
  assertReadableFile(connectionAuthPath, "OpenClaw connection-auth bundle");
  assertReadableFile(messageChannelPath, "OpenClaw message-channel bundle");
  assertReadableFile(indexPath, "OpenClaw dist index");

  const acpCliCompatPath = path.join(distDir, ACP_CLI_COMPAT_BASENAME);
  const compatPath = path.join(distDir, ACP_COMPAT_BASENAME);

  fs.writeFileSync(acpCliCompatPath, buildAcpCliCompatSource(acpCliSource));
  fs.writeFileSync(
    compatPath,
    buildCompatModuleSource({
      ...dependencies,
      acpCliBasename,
    }),
  );

  return {
    acpCliBasename,
    acpCliCompatPath,
    compatPath,
    ...dependencies,
  };
}

async function main() {
  const packageRoot = resolveOpenclawPackageRoot();
  const result = generateOpenclawAcpCompat(packageRoot);
  process.stdout.write(
    `${JSON.stringify({
      packageRoot,
      compatPath: result.compatPath,
      acpCliCompatPath: result.acpCliCompatPath,
      callBasename: result.callBasename,
      connectionAuthBasename: result.connectionAuthBasename,
      messageChannelBasename: result.messageChannelBasename,
    })}\n`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(String(error));
    process.exit(1);
  });
}
