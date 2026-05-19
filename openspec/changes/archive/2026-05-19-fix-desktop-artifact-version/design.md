## Context

The desktop build pipeline has two independent paths for determining the version:

1. **CI** (`desktop-artifacts.yml`): Runs `npm run prepare-sidecar`, which calls `prepare-sidecar.sh`. The script's `resolve_version()` tries `AGENTSVIEW_VERSION` env var first, then falls back to `git describe`. In CI, `actions/checkout` defaults to a shallow clone without tags, so `git describe` returns a short hash, which `version_to_semver()` rejects as empty. `patch_tauri_version` then silently skips, leaving the hardcoded `"0.12.1"` from `tauri.conf.json` untouched.

2. **Local development**: `prepare-sidecar.sh` runs with git tags available, so `git describe` typically returns a valid tag like `v0.29.0-3-g227f09c`, which `version_to_semver` converts to `0.29.0-dev.3`.

The proposed fix makes CI explicitly require a version string as input, and uses a sentinel default (`"unknown"`) in `tauri.conf.json` so that any code path that skips patching will fail at build time rather than silently using stale data.

## Goals / Non-Goals

**Goals:**

- CI builds always use the version explicitly provided by the workflow dispatch trigger
- Invalid or missing version strings cause an immediate build failure (not silent fallback to old data)
- Local development continues to work via `git describe`

**Non-Goals:**

- No change to the update/upgrade mechanism
- No change to how the Go sidecar binary receives its version via ldflags (that path already works correctly)
- No change to the macOS or Linux build matrix entries

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| CI trigger model | `workflow_dispatch` only with `version` required input | PR builds of desktop changes don't need installer artifacts; manual dispatch with explicit version is simpler and more reliable than inferring from git tags |
| Sentinel value | `"unknown"` in `tauri.conf.json` | If `patch_tauri_version` skips (e.g. invalid input), Tauri will attempt to build with `"unknown"` as the version, producing an obviously wrong filename rather than silently producing `0.12.1` |
| Hardening approach | Fail in script, not in workflow | The `patch_tauri_version` function already exists and is the single point of version injection; adding an `exit 1` there when `AGENTSVIEW_VERSION` is set but semver is empty catches ALL callers, not just the CI workflow |
| No PR trigger | Removed | The original PR trigger was useful for smoke-testing desktop builds, but the `version` input has no meaningful value during PRs; the workflow can still be triggered manually on any branch with a user-supplied version |

## Risks / Trade-offs

- **[Manual input error]**: User types `0.30` instead of `0.30.0` → `version_to_semver` rejects it → `patch_tauri_version` sees `AGENTSVIEW_VERSION` is set but semver is empty → `exit 1`. The workflow fails immediately with an error message. Mitigation: the error message should indicate the invalid version string.
- **[Local dev confusion]**: A developer runs `npm run prepare-sidecar` without `AGENTSVIEW_VERSION` on a shallow clone without tags. Previously this silently used `0.12.1`; now `tauri.conf.json` has `"unknown"` and `version_to_semver("dev")` returns empty. Since `AGENTSVIEW_VERSION` is unset, the current `patch_tauri_version` skips (returns 0), and the `"unknown"` remains. The `tauri build` step will fail. Mitigation: developers should ensure tags are fetched (`git fetch --tags`).
- **[Shell script error message]**: The `patch_tauri_version` hardening needs a clear error message so the user knows why it failed.