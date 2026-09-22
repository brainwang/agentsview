## Purpose

The desktop build pipeline SHALL support injecting a version string at build time via environment variable, with a sentinel default that makes unintended stale versions impossible to miss.

## Requirements

### Requirement: Version can be injected via environment variable

The build system SHALL accept a version string through the `AGENTSVIEW_VERSION` environment variable. When set, this value SHALL take precedence over any other version source (e.g., `git describe`).

#### Scenario: AGENTSVIEW_VERSION is set

- **WHEN** `AGENTSVIEW_VERSION` is set to a valid semver string (e.g., `0.30.0`)
- **THEN** `prepare-sidecar.sh` SHALL patch `tauri.conf.json` with that version and the build SHALL succeed

#### Scenario: AGENTSVIEW_VERSION is set to an invalid value

- **WHEN** `AGENTSVIEW_VERSION` is set to a non-semver string (e.g., `main`, `abc`)
- **THEN** `prepare-sidecar.sh` SHALL exit with a non-zero status and print an error message

#### Scenario: AGENTSVIEW_VERSION is not set

- **WHEN** `AGENTSVIEW_VERSION` is not set
- **THEN** `prepare-sidecar.sh` SHALL attempt to resolve the version via `git describe --tags --always --dirty`

### Requirement: Default version acts as a sentinel

`tauri.conf.json` SHALL use a clearly invalid version string (`"unknown"`) as its default value, so that any build that skips version patching produces an obviously incorrect installer filename rather than a plausible but stale one.

#### Scenario: Build with sentinel version

- **WHEN** `tauri.conf.json` version is `"unknown"` and no patching occurs
- **THEN** the generated installer filename SHALL contain `unknown` (e.g., `AgentsView_unknown_x64-setup.exe`), making the error obvious

### Requirement: CI workflow requires explicit version

The `desktop-artifacts.yml` workflow SHALL only be triggerable via `workflow_dispatch` and SHALL require a `version` input. The input value SHALL be passed as `AGENTSVIEW_VERSION` in the workflow environment.

#### Scenario: Trigger without version

- **WHEN** the workflow is dispatched without a `version` input
- **THEN** GitHub Actions SHALL reject the dispatch because `version` is marked `required: true`

#### Scenario: Trigger with valid version

- **WHEN** the workflow is dispatched with version `0.30.0`
- **THEN** the `AGENTSVIEW_VERSION` environment variable SHALL be set to `0.30.0` and the build SHALL produce `AgentsView_0.30.0_x64-setup.exe`