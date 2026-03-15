#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const ACP_CLI_COMPAT_BASENAME = "spritz-acp-cli-compat.js";
export const ACP_COMPAT_BASENAME = "spritz-acp-compat.js";

function parseNamedImports(source) {
  return [...source.matchAll(/import\s+\{([\s\S]*?)\}\s+from\s+["']\.\/([^"']+)["']/g)].map(
    (match) => ({
      basename: match[2],
      specifiers: match[1]
        .split(",")
        .map((part) => part.trim())
        .filter(Boolean)
        .map((part) => {
          const aliasMatch = part.match(/^(.+?)\s+as\s+([A-Za-z_$][\w$]*)$/);
          if (aliasMatch) {
            return {
              imported: aliasMatch[1].trim(),
              local: aliasMatch[2].trim(),
            };
          }
          return {
            imported: part,
            local: part,
          };
        }),
    }),
  );
}

function findImportBasename(namedImports, predicate) {
  for (const entry of namedImports) {
    const locals = new Set(entry.specifiers.map((specifier) => specifier.local));
    if (predicate(locals)) {
      return entry.basename;
    }
  }
  return null;
}

function requireImportBasename(namedImports, localNames, label) {
  const basename = findImportBasename(namedImports, (locals) =>
    localNames.every((localName) => locals.has(localName)),
  );
  if (!basename) {
    throw new Error(`Failed to resolve ${label} from the installed OpenClaw ACP bundle.`);
  }
  return basename;
}

function findImportBasenameByLocalName(namedImports, localName) {
  return findImportBasename(namedImports, (locals) => locals.has(localName));
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
  const namedImports = parseNamedImports(acpCliSource);
  return {
    callBasename: requireImportBasename(
      namedImports,
      ["GatewayClient", "buildGatewayConnectionDetails"],
      "GatewayClient/buildGatewayConnectionDetails bundle",
    ),
    connectionAuthBasename: requireImportBasename(
      namedImports,
      ["resolveGatewayConnectionAuth"],
      "resolveGatewayConnectionAuth bundle",
    ),
    gatewayConstantsBasename: requireImportBasename(
      namedImports,
      ["GATEWAY_CLIENT_NAMES", "GATEWAY_CLIENT_MODES"],
      "gateway client constants bundle",
    ),
    loadConfigBasename: findImportBasenameByLocalName(namedImports, "loadConfig") ?? "index.js",
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
  return `import * as configModule from "./${params.loadConfigBasename}";
import * as callModule from "./${params.callBasename}";
import * as connectionAuthModule from "./${params.connectionAuthBasename}";
import * as gatewayConstantsModule from "./${params.gatewayConstantsBasename}";
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

const loadConfig = pickNamedFunction(configModule, "loadConfig");
const GatewayClient = pickNamedFunction(callModule, "GatewayClient");
const buildGatewayConnectionDetails = pickNamedFunction(
  callModule,
  "buildGatewayConnectionDetails",
);
const resolveGatewayConnectionAuth = pickNamedFunction(
  connectionAuthModule,
  "resolveGatewayConnectionAuth",
);
const GATEWAY_CLIENT_NAMES = pickNamedObject(gatewayConstantsModule, "CONTROL_UI");
const GATEWAY_CLIENT_MODES = pickNamedObject(gatewayConstantsModule, "WEBCHAT");

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
  const gatewayConstantsPath = path.join(distDir, dependencies.gatewayConstantsBasename);
  const loadConfigPath = path.join(distDir, dependencies.loadConfigBasename);

  assertReadableFile(callPath, "OpenClaw call bundle");
  assertReadableFile(connectionAuthPath, "OpenClaw connection-auth bundle");
  assertReadableFile(gatewayConstantsPath, "OpenClaw gateway-constants bundle");
  assertReadableFile(loadConfigPath, "OpenClaw loadConfig bundle");

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
      gatewayConstantsBasename: result.gatewayConstantsBasename,
      loadConfigBasename: result.loadConfigBasename,
    })}\n`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(String(error));
    process.exit(1);
  });
}
