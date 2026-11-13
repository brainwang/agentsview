## Why

Exporting a session (or insight) as HTML currently gives zero user
feedback — the file downloads silently to the browser's downloads
directory with no toast, banner, or dialog. If the export fails, the
error is only logged to the browser console, invisible to the user.
The user is left wondering whether the export worked at all. The
codebase already has a `showFlash` toast system used for save/clear
operations; export should use it too.

## What Changes

- Add a success toast (`showFlash` with `tone: "success"`) when a
  session HTML export or insight HTML export completes successfully.
- Add an error toast (`showFlash` with `tone: "danger"`) when an export
  fails, replacing the current `console.error`-only handling.
- Add localized message keys for both messages and sync across all 7
  locale files (en, zh-CN, zh-TW, ja, ko, fr, az).

## Capabilities

### New Capabilities

- `export-feedback`: User-facing feedback (toast) on HTML export
  success and failure for sessions and insights.

### Modified Capabilities

None.

## Impact

- `frontend/src/lib/components/layout/AppHeader.svelte` — import
  `showFlash`; wrap `handleExport` with success and error toasts.
- `frontend/src/lib/components/recall/GeneratedInsightsPanel.svelte` —
  import `showFlash`; wrap `handleInsightExport` with success and error
  toasts.
- `frontend/messages/*.json` (7 files) — add `export_downloaded` and
  `export_failed` keys with translated copy in each locale.
