package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

type installTargetPickerOption struct {
	Name               string
	ImageURL           string
	OwnerLabel         string
	EncodedPresetInput string
}

type installTargetPickerPageData struct {
	RequestID  string
	FormAction string
	StateToken string
	Options    []installTargetPickerOption
}

var installTargetPickerTemplate = template.Must(template.New("install-target-picker").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Choose an install target</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f4f1ea;
        --surface: #fffdf9;
        --border: #ddd5c8;
        --text: #1f1b16;
        --muted: #675d50;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: linear-gradient(180deg, var(--bg), #ebe4d7);
        color: var(--text);
        display: grid;
        place-items: center;
        padding: 24px;
      }
      main {
        width: min(680px, 100%);
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: 24px;
        padding: 28px;
        box-shadow: 0 24px 80px rgba(31, 27, 22, 0.08);
      }
      h1 {
        margin: 0 0 10px;
        font-size: 30px;
        line-height: 1.1;
      }
      p {
        margin: 0 0 22px;
        color: var(--muted);
        line-height: 1.6;
      }
      form {
        display: grid;
        gap: 12px;
      }
      label {
        display: grid;
        grid-template-columns: auto 1fr;
        gap: 14px;
        align-items: center;
        padding: 14px 16px;
        border: 1px solid var(--border);
        border-radius: 18px;
        cursor: pointer;
      }
      input[type="radio"] {
        margin: 0;
      }
      .row {
        display: flex;
        align-items: center;
        gap: 12px;
      }
      .avatar {
        width: 40px;
        height: 40px;
        border-radius: 50%;
        overflow: hidden;
        background: #ede6da;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        font-size: 14px;
        color: var(--muted);
      }
      .avatar img {
        width: 100%;
        height: 100%;
        object-fit: cover;
        display: block;
      }
      .name {
        font-weight: 600;
      }
      .owner {
        color: var(--muted);
        font-size: 14px;
      }
      button {
        margin-top: 8px;
        border: 0;
        border-radius: 999px;
        padding: 12px 18px;
        background: var(--text);
        color: #fff;
        font-weight: 600;
        cursor: pointer;
      }
      .meta {
        margin-top: 18px;
        padding-top: 18px;
        border-top: 1px solid var(--border);
        color: var(--muted);
        font-size: 13px;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>Choose an install target</h1>
      <p>Select which target this Slack workspace should use.</p>
      <form method="post" action="{{ .FormAction }}">
        <input type="hidden" name="state" value="{{ .StateToken }}">
        <input type="hidden" name="requestId" value="{{ .RequestID }}">
        {{ range $index, $option := .Options }}
        <label>
          <input type="radio" name="target" value="{{ $option.EncodedPresetInput }}" {{ if eq $index 0 }}checked{{ end }}>
          <span class="row">
            <span class="avatar">
              {{ if $option.ImageURL }}
              <img src="{{ $option.ImageURL }}" alt="">
              {{ else }}
              {{ slice $option.Name 0 1 }}
              {{ end }}
            </span>
            <span>
              <div class="name">{{ $option.Name }}</div>
              {{ if $option.OwnerLabel }}
              <div class="owner">{{ $option.OwnerLabel }}</div>
              {{ end }}
            </span>
          </span>
        </label>
        {{ end }}
        <button type="submit">Continue</button>
      </form>
      {{ if .RequestID }}
      <div class="meta">Request ID: <code>{{ .RequestID }}</code></div>
      {{ end }}
    </main>
  </body>
</html>`))

func encodeInstallTargetSelection(presetInputs map[string]any) (string, error) {
	if len(presetInputs) == 0 {
		return "", fmt.Errorf("preset inputs are required")
	}
	encoded, err := json.Marshal(presetInputs)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeInstallTargetSelection(raw string) (map[string]any, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	var presetInputs map[string]any
	if err := json.Unmarshal(decoded, &presetInputs); err != nil {
		return nil, err
	}
	if len(presetInputs) == 0 {
		return nil, fmt.Errorf("preset inputs are required")
	}
	return presetInputs, nil
}

func (g *slackGateway) renderInstallTargetPicker(w http.ResponseWriter, stateToken, requestID string, targets []backendInstallTarget) {
	options := make([]installTargetPickerOption, 0, len(targets))
	for _, target := range targets {
		encodedPresetInput, err := encodeInstallTargetSelection(target.PresetInputs)
		if err != nil {
			http.Error(w, "install target is invalid", http.StatusInternalServerError)
			return
		}
		options = append(options, installTargetPickerOption{
			Name:               strings.TrimSpace(target.Profile.Name),
			ImageURL:           strings.TrimSpace(target.Profile.ImageURL),
			OwnerLabel:         strings.TrimSpace(target.OwnerLabel),
			EncodedPresetInput: encodedPresetInput,
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = installTargetPickerTemplate.Execute(w, installTargetPickerPageData{
		RequestID:  requestID,
		FormAction: g.selectInstallTargetPath(),
		StateToken: stateToken,
		Options:    options,
	})
}
