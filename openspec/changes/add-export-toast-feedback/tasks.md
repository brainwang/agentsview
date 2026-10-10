## 1. Localized message keys

- [x] 1.1 Add `export_downloaded` key to `frontend/messages/en.json`
  with value "HTML session record generated to the downloads directory"
- [x] 1.2 Add `export_failed` key to `frontend/messages/en.json`
  with value "Export failed"
- [x] 1.3 Sync `export_downloaded` and `export_failed` keys to all
  remaining locale files: `zh-CN.json`, `zh-TW.json`, `ja.json`,
  `ko.json`, `fr.json`, `az.json` — translate each value

## 2. Session export toast

- [x] 2.1 Import `showFlash` from `@kenn/io/kit-ui` in
  `frontend/src/lib/components/layout/AppHeader.svelte`
- [x] 2.2 Update `handleExport()` in `AppHeader.svelte` (line 134) —
  add `showFlash(m.export_downloaded(), { tone: "success" })` after
  the `await downloadExport(...)` call in the try block; add
  `showFlash(m.export_failed(), { tone: "danger" })` in the catch
  block before or replacing the `console.error`

## 3. Insight export toast

- [x] 3.1 Import `showFlash` from `@kenn/io/kit-ui` in
  `frontend/src/lib/components/recall/GeneratedInsightsPanel.svelte`
- [x] 3.2 Update `handleInsightExport()` in
  `GeneratedInsightsPanel.svelte` (line 165) — add
  `showFlash(m.export_downloaded(), { tone: "success" })` after the
  `await downloadInsightExport(...)` call in the try block; add
  `showFlash(m.export_failed(), { tone: "danger" })` in the catch
  block before or replacing the `console.error`

## 4. Verification

- [x] 4.1 Run `npm run i18n:compile` to compile the new message keys
- [x] 4.2 Run `npm run check` (type check + lint) to verify no errors
- [x] 4.3 Manually verify: trigger a session HTML export and confirm
  the success toast appears; simulate an export failure (e.g. stop
  the server) and confirm the error toast appears
