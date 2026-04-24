## Context

Текущий umbrella rollout разбит на независимые этапы. Этот change покрывает scope: **internal/repository/postgres, internal/storage**.

## Goals / Non-Goals

**Goals:**
- Реализовать stackerr policy в scope change.
- Сохранить compatibility поля и sanitization контракты.
- Закрыть DoD критерии данного tier.

**Non-Goals:**
- Миграция пакетов вне текущего scope.
- Внедрение внешних библиотек или custom slog handler.

## Decisions

1. Применять boundary-first миграцию (`WithStack` на границах, `Wrap` при добавлении контекста).
2. Сохранять `error_code` и `diagnostic_message` как стабильные поля, добавляя structured `error` где есть error object.
3. Проверять готовность через tier-local тесты и regression assertions.

## Risks / Trade-offs

- [Риск] Частичный rollout может оставить несогласованные зоны → [Mitigation] Чёткий tier scope и DoD.
- [Риск] Регрессия тестов shape/sanitization → [Mitigation] обновление assertions и пакетные прогоны.

## Migration Plan

1. Сформировать список целевых файлов в scope.
2. Выполнить миграцию error-paths по правилам stackerr.
3. Обновить lifecycle/log shape и тесты.
4. Прогнать тесты scope и зафиксировать результат.

## Open Questions

- Нужен ли дополнительный soft-compat режим для потребителей логов в этом tier?

## Tier-2 Implementation Notes (Closeout)

### File-level migration checklist

- [x] `internal/repository/postgres/audit_postgres.go` — `%w` wrappers переведены на `stackerr.Wrap`, boundary returns stack-enabled.
- [x] `internal/repository/postgres/account_postgres.go` — boundary returns (`return err`) переведены на `stackerr.WithStack`.
- [x] `internal/repository/postgres/session_postgres.go` — boundary returns переведены на `stackerr.WithStack`.
- [x] `internal/repository/postgres/result_postgres.go` — boundary returns переведены на `stackerr.WithStack`.
- [x] `internal/repository/postgres/task_postgres.go` — boundary returns stack-enabled; `%w` wrappers в audit helpers переведены на `stackerr.Wrap`.
- [x] `internal/storage/session_store.go` — boundary returns переведены на `stackerr.WithStack`.

### Boundary points + lifecycle compatibility confirmation

- [x] Boundary policy: в scope Tier-2 не осталось raw boundary возвратов `return err` без stack policy.
- [x] Context wrapping policy: случаи `fmt.Errorf(...%w...)` в scope Tier-2 заменены на `stackerr.Wrap`.
- [x] Lifecycle compatibility: в `internal/repository/postgres` и `internal/storage` отсутствуют собственные WARN/ERROR logger emissions; существующие compatibility поля (`error_code`) в persistence-моделях/SQL сохранены.

### Tests / assertions updates

- [x] `internal/storage/session_store_test.go` дополняет покрытие stack-enabled поведения для unexpected backend errors (`Save`/`Load`) с проверкой `errors.Is` + `errors.As(..., *stackerr.Error)`.

### Validation evidence

- [x] LSP diagnostics:
  - `lsp_diagnostics /root/follower-bmad/internal/repository/postgres --severity error` → 0 errors
  - `lsp_diagnostics /root/follower-bmad/internal/storage --severity error` → 0 errors
- [x] Tier-2 tests: `go test ./internal/repository/postgres ./internal/storage` → PASS.
- [x] Full regression: `go test ./...` → PASS.
- [x] Build: `go build -buildvcs=false ./...` → PASS.
