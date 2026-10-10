## Context

The frontend already has a toast system: `showFlash(message, { tone })`
from `@kenn/io/kit-ui`, rendered by `<FlashBanner>` mounted in
`App.svelte:809`. It is used for save/clear/reclassify feedback in
`DataPage.svelte`, `ProjectSessionPreviewPane.svelte`, and
`SessionItem.svelte`. The session and insight HTML export handlers
(`AppHeader.svelte:134` and `GeneratedInsightsPanel.svelte:165`) do not
use it — success is silent and failure is `console.error`-only.

## Goals / Non-Goals

**Goals:**

- Surface a success toast on session and insight HTML export.
- Surface an error toast on export failure, replacing the console-only
  path.

**Non-Goals:**

- Returning the resolved filename from `downloadExport` /
  `downloadInsightExport` — the toast copy does not need the filename;
  it says "generated to the downloads directory" generically.
- A modal dialog — toast matches the existing pattern and is less
  intrusive for a routine download action.
- Feedback for markdown link copy or source path copy — those already
  have inline feedback (checkmark + label change).

## Decisions

### D1: Use `showFlash`, not a modal or inline banner

**Choice:** call `showFlash(message, { tone: "success" | "danger" })`
inside the `try`/`catch` blocks of `handleExport` and
`handleInsightExport`.

**Rationale:** matches the established pattern for all other async
action feedback in the codebase. A modal would be heavier than needed
for "your file downloaded." The `<FlashBanner>` is already mounted at
the app level.

### D2: Generic message copy, no filename

**Choice:** the success toast says "HTML session record generated to
the downloads directory" (localized). It does not include the
filename.

**Rationale:** extracting the filename from
`downloadAuthenticatedExport` (where it is resolved from
`Content-Disposition`) would require changing the return type of
`downloadExport` and `downloadInsightExport` from `Promise<void>` to
`Promise<string>`. The generic message is sufficient — the user knows
to look in their downloads folder. If the filename is wanted later,
it's a trivial follow-up.

### D3: Both success and error in the same handler

**Choice:** add `showFlash` in both the success path (after `await`)
and the `catch` block of each handler.

**Rationale:** the handlers already have `try/catch` structure.
Inserting one `showFlash` call in each branch is the minimal change.

## Risks / Trade-offs

- **[Toast on Tauri WebView2]** the toast relies on the DOM-based
  `<FlashBanner>`, which works inside Tauri's WebView2 (where
  `window.open` is blocked). The export download itself already uses
  the fetch+blob approach for this reason. → no additional risk.
- **[Double toast on rapid re-click]** if the user clicks export twice
  quickly, two toasts may stack. → acceptable; `showFlash` already
  handles this by replacing or stacking per its own logic, and the
  existing usage has no debounce.
