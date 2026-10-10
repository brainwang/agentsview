## Purpose

Provide user-facing toast feedback when HTML export operations (session
or insight) succeed or fail, so the user is not left wondering whether
the export worked.

## ADDED Requirements

### Requirement: Export success toast

The system SHALL display a success toast when a session HTML export or
insight HTML export completes successfully.

#### Scenario: Session export succeeds
- **WHEN** the user triggers a session HTML export and the download
  completes without error
- **THEN** a success toast SHALL appear indicating the HTML file has
  been generated to the downloads directory

#### Scenario: Insight export succeeds
- **WHEN** the user triggers an insight HTML export and the download
  completes without error
- **THEN** a success toast SHALL appear indicating the HTML file has
  been generated to the downloads directory

### Requirement: Export failure toast

The system SHALL display an error toast when a session HTML export or
insight HTML export fails, instead of logging the error only to the
browser console.

#### Scenario: Session export fails
- **WHEN** the user triggers a session HTML export and the request
  returns an error status or throws
- **THEN** an error toast SHALL appear indicating the export failed

#### Scenario: Insight export fails
- **WHEN** the user triggers an insight HTML export and the request
  returns an error status or throws
- **THEN** an error toast SHALL appear indicating the export failed
