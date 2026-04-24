## ADDED Requirements

### Requirement: stackerr-tier2-persistence SHALL enforce stackerr policy in tier scope
The system SHALL apply stack-enabled error handling consistently within scope `internal/repository/postgres, internal/storage`.

#### Scenario: Boundary error path in tier scope
- **WHEN** a boundary function in tier scope returns an error
- **THEN** the returned error is stack-enabled via `stackerr.WithStack` or contextual `stackerr.Wrap`

### Requirement: Tier compatibility fields SHALL be preserved
The system SHALL preserve compatibility fields used by lifecycle and diagnostics contracts while introducing structured errors.

#### Scenario: Lifecycle-compatible logging
- **WHEN** a WARN/ERROR event is emitted in tier scope with an error object
- **THEN** `error_code` remains present
- **THEN** `diagnostic_message` remains present
- **THEN** structured `error` is included when available

### Requirement: Tier DoD SHALL be verifiable by tests
The tier rollout SHALL be considered complete only when tier-specific tests and assertions pass.

#### Scenario: Tier done criteria validation
- **WHEN** rollout implementation for the tier is complete
- **THEN** all tier DoD checks pass:
- **THEN** `fmt.Errorf(...%w...)` заменены на `stackerr.Wrap` там, где добавляется контекст
- **THEN** граничные возвраты ошибок не остаются raw (`return err`) без stack policy
- **THEN** repository/storage тесты проходят
