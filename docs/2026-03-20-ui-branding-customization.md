---
date: 2026-03-20
author: Onur Solmaz <onur@textcortex.com>
title: Deployment-Wide UI Branding
tags: [spritz, ui, branding, helm]
---

## Overview

Spritz supports deployment-wide UI branding for the built-in web interface.

This guide documents the v1 white-label path aimed at operators who want a
simple setup:

- configure branding in Helm values
- deploy the chart
- let the UI pick up the brand at startup through runtime config

The branding model is deployment-wide. One Spritz install gets one brand.

## How branding works

The runtime path is:

1. Helm values
2. UI deployment environment variables
3. `config.js` generated at container startup
4. React app runtime config
5. CSS variables and document metadata applied in the browser

That means operators do not need to rebuild the UI image just to change colors,
product name, logo, favicon, or terminal colors.

## What v1 supports

- custom product name
- custom logo URL
- custom favicon URL
- one app theme palette
- one terminal color palette
- one shared UI radius base value

## What v1 does not support

- per-preset branding
- per-tenant or per-organization branding
- an in-app branding editor
- raw custom CSS injection
- light/dark theme switching

## 1. Add a branding values file

Create a values file such as `branding.values.yaml` and put your branding under
`ui.branding`:

```yaml
ui:
  branding:
    productName: "Example Console"
    logoUrl: "https://console.example.com/assets/logo.png"
    faviconUrl: "https://console.example.com/assets/favicon.ico"
    theme:
      background: "#f8fafc"
      foreground: "#0f172a"
      muted: "#e2e8f0"
      mutedForeground: "#475569"
      primary: "#1d4ed8"
      primaryForeground: "#eff6ff"
      border: "#cbd5e1"
      destructive: "#dc2626"
      radius: "1rem"
    terminal:
      background: "#101820"
      foreground: "#f5f5f5"
      cursor: "#ff6b00"
```

All fields are optional. If a field is omitted, Spritz keeps the built-in
default.

## 2. Deploy or upgrade the chart

Apply the override during install or upgrade:

```bash
helm upgrade --install spritz ./helm/spritz \
  --namespace spritz-system \
  --create-namespace \
  --set global.host=console.example.com \
  -f branding.values.yaml
```

If you already have a working install, this is usually enough.

If the running UI pod still serves older runtime config after a values change,
restart the UI deployment:

```bash
kubectl rollout restart deployment/spritz-ui -n spritz-system
kubectl rollout status deployment/spritz-ui -n spritz-system --timeout=180s
```

## 3. Verify the branding

Open the configured host and verify:

- the browser tab title uses `productName`
- the favicon uses `faviconUrl`
- the sidebar or app header shows the branded logo and product name
- the primary actions use `theme.primary`
- cards, buttons, inputs, and menus reflect `theme.radius`
- the terminal uses `terminal.background`, `terminal.foreground`, and `terminal.cursor`

If you want to inspect the generated runtime config directly:

```bash
curl -sS http://console.example.com/config.js
```

Look for `window.SPRITZ_CONFIG.branding`.

## 4. Understanding `theme.radius`

`theme.radius` is the base radius for the branded UI.

Spritz derives a small radius scale from that base and uses those tokens across
buttons, cards, inputs, menus, dialogs, and chat surfaces. This keeps the UI
proportionate instead of forcing every element to use the exact same corner
value.

As a practical rule:

- smaller values such as `0.5rem` produce a sharper product feel
- values around `0.75rem` to `1rem` feel balanced for most brands
- larger values such as `1.25rem` produce a softer, rounder feel

## 5. Local testing with kind

If you are using the local kind workflow from
[Local kind Development Guide](2026-03-14-local-kind-development-guide.md),
you can test branding by supplying a separate values file during Helm upgrade.

Example:

```bash
helm upgrade --install spritz ./helm/spritz \
  --namespace spritz-system \
  --create-namespace \
  --set global.host=console.example.com \
  -f branding.values.yaml
```

If you want to run a second branded local install at the same time, give it:

- a different Helm release name
- a different system namespace
- a different instance namespace
- a different hostname under your local `example.com` setup
- distinct operator watch namespaces and RBAC names

This avoids collisions between the two installs.

## 6. Runtime shape

The chart renders the values into `SPRITZ_CONFIG.branding` with this shape:

```ts
{
  productName: string
  logoUrl: string
  faviconUrl: string
  theme: {
    background: string
    foreground: string
    muted: string
    mutedForeground: string
    primary: string
    primaryForeground: string
    border: string
    destructive: string
    radius: string
  }
  terminal: {
    background: string
    foreground: string
    cursor: string
  }
}
```

## 7. Operator notes

- v1 expects `logoUrl` and `faviconUrl` to be externally hosted URLs.
- Keep assets on stable URLs so rollouts do not depend on rebuilding the UI image.
- Branding changes are UI-only. No backend API or CRD changes are required.
- Functional labels in the UI should stay generic. Use branding for product
  identity, not for instance lifecycle terminology.

## 8. Troubleshooting

### Branding values changed but the UI still looks old

- confirm your Helm values rendered as expected
- fetch `/config.js` and verify `window.SPRITZ_CONFIG.branding`
- restart `spritz-ui` so the runtime config is regenerated
- hard refresh the browser or open the page in a private window

### Logo looks cropped

Prefer an asset with transparent padding or a balanced square canvas when using
an icon-style logo.

### Colors changed but some surfaces still look slightly different

The UI uses semantic tokens and a derived radius scale. Some status surfaces,
tiny indicators, and circular loading dots intentionally remain special-case
elements instead of inheriting every brand value directly.

## Related docs

- [README](../README.md)
- [Local kind Development Guide](2026-03-14-local-kind-development-guide.md)
- [Simplest Spritz Deployment Spec](2026-02-24-simplest-spritz-deployment-spec.md)
