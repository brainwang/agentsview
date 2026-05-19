## Why

The desktop CI workflow (`desktop-artifacts.yml`) produces installer files with the wrong version number (`AgentsView_0.12.1_x64-setup.exe`) regardless of the actual release. This happens because `tauri.conf.json` has a hardcoded, outdated version string `0.12.1`, and the `prepare-sidecar.sh` script's dynamic version-patching mechanism is bypassed in CI when git tags are unavailable, leaving the stale default untouched.

## What Changes

- **Desktop CI trigger**: Remove `pull_request` trigger from `desktop-artifacts.yml`, keep only `workflow_dispatch` with a `version` required input
- **Sentinel default version**: Change `tauri.conf.json` version from `"0.12.1"` to `"unknown"` so any build that fails to inject a real version fails loudly instead of silently using a stale value
- **Harden patch_tauri_version**: When `AGENTSVIEW_VERSION` is set but `version_to_semver()` returns empty (meaning the user provided an invalid version), `patch_tauri_version` should `exit 1` instead of silently skipping

## Capabilities

### New Capabilities

- `build-version-injection`: Define the mechanism for injecting the correct version into the desktop build, covering both CI (via `AGENTSVIEW_VERSION` env var) and local development (via git describe)

### Modified Capabilities

None. This change only affects the build pipeline—no user-facing requirements change.

## Impact

- `.github/workflows/desktop-artifacts.yml`: Only triggerable via `workflow_dispatch` with a `version` input
- `desktop/src-tauri/tauri.conf.json`: Default version becomes `"unknown"`; local builds without `AGENTSVIEW_VERSION` or a valid git tag will fail at build time
- `desktop/scripts/prepare-sidecar.sh`: `patch_tauri_version` becomes stricter, rejecting invalid version strings when `AGENTSVIEW_VERSION` is explicitly set