## Context

Текущий umbrella rollout разбит на независимые этапы. Этот change покрывает scope: **internal/app, internal/worker, internal/browser, internal/config**.

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

## Tier-1 Implementation Notes (Closeout)

### File-level migration checklist

- [x] `internal/app/bootstrap.go` — boundary returns переведены на `stackerr.WithStack`.
- [x] `internal/app/app.go` — boundary returns переведены на `stackerr.WithStack`.
- [x] `internal/app/operational_metrics_refresher.go` — boundary returns stack-enabled; WARN logging переведён на lifecycle-compatible shape.
- [x] `internal/app/runtime_dependencies.go` — bootstrap WARN logging переведён на lifecycle-compatible shape (`error_code`, `diagnostic_message`, structured `error`).
- [x] `internal/worker/quota_service.go` — boundary returns переведены на `stackerr.WithStack`.
- [x] `internal/worker/account_guard.go` — boundary returns stack-enabled; error logging дополнен compatibility fields и structured `error`.
- [x] `internal/worker/screenshot_payload.go` — boundary return переведён на `stackerr.WithStack`.
- [x] `internal/worker/execution_service.go` — artifact persist failure path stack-enabled в joined cleanup flow.
- [x] `internal/browser/session_restorer.go` — boundary returns/join paths переведены на stack-enabled policy.
- [x] `internal/browser/follow_flow.go` — boundary returns stack-enabled включая normalize paths.
- [x] `internal/browser/verify_flow.go` — boundary returns stack-enabled включая normalize paths.
- [x] `internal/browser/warmup_flow.go` — boundary returns stack-enabled.
- [x] `internal/browser/bootstrap_login_flow.go` — runtime/boundary returns stack-enabled.
- [x] `internal/browser/playwright_follow_adapter.go` — runtime/boundary returns stack-enabled.
- [x] `internal/browser/playwright_verify_adapter.go` — runtime/boundary returns stack-enabled.
- [x] `internal/config/env.go` — boundary override path stack-enabled.
- [x] `internal/config/loader.go` — read/parse paths переведены на `stackerr.Wrap`, downstream boundary returns stack-enabled.
- [x] `internal/config/validate.go` — validation aggregate error переведён на `stackerr.New`.

### Boundary points + lifecycle compatibility confirmation

- [x] Boundary policy: все целевые Tier-1 return paths с `err` переведены на `stackerr.WithStack` либо `stackerr.Wrap` при добавлении контекста.
- [x] Lifecycle compatibility: WARN/ERROR callsites в `internal/app`/`internal/worker` сохраняют `error_code` и `diagnostic_message` при наличии error object и добавляют structured `error`.

### Validation evidence

- [x] LSP diagnostics: `lsp_diagnostics /root/follower-bmad/internal --severity error` → **0 errors**.
- [x] Tier-1 tests: `go test ./internal/app ./internal/worker ./internal/browser ./internal/config` → **PASS**.
- [x] Full regression: `go test ./...` → **PASS**.
- [x] Build: `go build -buildvcs=false ./...` → **PASS**.
