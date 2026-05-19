## 1. Update CI workflow

- [x] 1.1 Remove `pull_request` trigger from `desktop-artifacts.yml` (lines 5-12)
- [x] 1.2 Add `workflow_dispatch` with required `version` string input to `desktop-artifacts.yml`
- [x] 1.3 Pass `AGENTSVIEW_VERSION: ${{ inputs.version }}` as an environment variable at the job level in `desktop-artifacts.yml`

## 2. Set sentinel version in tauri.conf.json

- [x] 2.1 Change `"version": "0.12.1"` to `"version": "unknown"` in `desktop/src-tauri/tauri.conf.json`

## 3. Harden patch_tauri_version in prepare-sidecar.sh

- [x] 3.1 In `patch_tauri_version()` in `desktop/scripts/prepare-sidecar.sh`, after the `if [ -z "$semver" ]` check: if `AGENTSVIEW_VERSION` is set, print an error and `exit 1` instead of silently returning 0
- [x] 3.2 Ensure the error message includes the invalid version string so the user knows what was rejected