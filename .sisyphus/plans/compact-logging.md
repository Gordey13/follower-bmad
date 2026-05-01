# Compact Logging Handler

## TL;DR

> **Quick Summary**: Заменить JSON-логгер в production на компактный человекочитаемый формат с auto-truncation ID и conditional duration.
>
> **Deliverables**:
> - `internal/logger/compact_handler.go` — кастомный slog.Handler
> - `cmd/follower/main.go` — замена JSONHandler → CompactHandler
> - `internal/logger/compact_handler_test.go` — тесты формата
>
> **Estimated Effort**: Quick
> **Parallel Execution**: YES — 3 волны
> **Critical Path**: compact_handler.go → main.go integration → tests

---

## Context

### Original Request
Пользователь показал желаемый формат логов:
```
17:04:30 INFO  session.restored  task:…38980 status:valid (9ms)
17:04:30 INFO  execution_context.prepared task:…38980 (16ms)
17:04:38 INFO  follow.warmup.succeeded task:…38980 (4688ms)
```

### Interview Summary
**Key Decisions**:
- Вариант B — кастомный slog.Handler
- Только production (`cmd/follower/main.go`)
- Truncate ID: последние 8 символов с префиксом `…`
- Duration: показывать только при наличии `duration_ms` атрибута

**Current State**:
- `cmd/follower/main.go:16` — `slog.New(slog.NewJSONHandler(os.Stdout, nil))`
- Business-logic использует logger через DI — никаких изменений там не нужно

### Design Decisions

```
┌──────────────────────────────────────────────────────────────────┐
│                    CompactHandler Architecture                    │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  slog.Logger  ─────────┐                                         │
│                        │                                         │
│  ┌─────────────────────▼──────────────────────┐                 │
│  │        CompactHandler (slog.Handler)        │                 │
│  ├─────────────────────────────────────────────┤                 │
│  │  Enabled(level)     → level >= INFO          │                 │
│  │  Handle(record)     → format:                │                 │
│  │    HH:MM:SS LEVEL  event_name  attrs         │                 │
│  │                       │                      │                 │
│  │                       ├── task_id → task:…XXXX│                 │
│  │                       ├── account → acc:…YYYY│                 │
│  │                       ├── duration_ms → (Nms)│                 │
│  │                       └── other → key:val     │                 │
│  └──────────────────────────────────────────────┘                 │
│                                                                  │
│  Drop-in replacement: implements slog.Handler interface          │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

## Work Objectives

### Core Objective
Compact production logging: человекочитаемый формат, drop-in replacement JSONHandler.

### Concrete Deliverables
- `internal/logger/compact_handler.go` — ~80-120 строк
- `cmd/follower/main.go:16` — 1 строка изменения
- `internal/logger/compact_handler_test.go` — 5-8 тестов

### Definition of Done
- [ ] `go build -buildvcs=false ./...` — PASS
- [ ] `go test ./internal/logger/...` — PASS
- [ ] Логи в production выводят компактный формат

### Must Have
- Точный формат: `HH:MM:SS LEVEL  event.name  key:truncatedVal (Nms)`
- Truncate ID поля (task_id, account, etc.) до последних 8 символов
- Duration только при наличии

### Must NOT Have
- Изменения в business-logic (internal/browser, internal/worker, etc.)
- Внешние зависимости (только stdlib `log/slog`)
- Изменения в тестах business-logic

## Verification Strategy

### Test Decision
- **Infrastructure exists**: YES (Go testing, `go test`)
- **Automated tests**: Tests-after
- **Framework**: `go test ./internal/logger/...`
- **Agent-Executed QA**: Всегда (запустить приложение, проверить вывод логов)

### QA Policy
Каждый task включает agent-executed QA.

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately — compact handler impl):
└── Task 1: internal/logger/compact_handler.go [deep]

Wave 2 (After Wave 1 — integration + tests, parallel):
├── Task 2: cmd/follower/main.go integration [quick]
└── Task 3: internal/logger/compact_handler_test.go [quick]

Wave 3 (After Wave 2 — verification):
└── Task 4: Build + test + manual log output check [quick]

Critical Path: Task 1 → Task 2 → Task 4
Max Concurrent: 2 (Wave 2)
```

---

## TODOs

- [x] 1. Create `internal/logger/compact_handler.go`

  **What to do**:
  - Реализовать `CompactHandler` — struct с полями `mu sync.Mutex`, `out io.Writer`
  - Implement `slog.Handler` interface:
    - `Enabled(ctx context.Context, level slog.Level) bool` — возвращать `level >= slog.LevelInfo`
    - `Handle(ctx context.Context, r slog.Record) error` — форматирование записи
    - `WithAttrs(attrs []slog.Attr) slog.Handler` — return new handler with attrs
    - `WithGroup(name string) slog.Handler` — return new handler (group support)
  - Форматирование Handle():
    - Время: `r.Time.Format("15:04:05")`
    - Level: `r.Level.String()` → `INFO`, `WARN`, `ERROR`, `DEBUG`
    - Message: `r.Message` — заменить `.` на пробелы для выравнивания, или оставить как есть
    - Атрибуты: итерировать `r.Attrs()`, для каждого:
      - Если key == `task_id` → форматировать как `task:…` + последние 8 символов
      - Если key == `account` → форматировать как `acc:…` + последние 8 символов
      - Если key == `duration_ms` → форматировать как `(%dms)`
      - Иначе → `key:value`
    - Записать в `out` с newline

  **Must NOT do**:
  - Не менять business-logic
  - Не добавлять внешние зависимости

  **Recommended Agent Profile**:
  - **Category**: `deep`
  - **Skills**: `[]`
  - **Skills Evaluated but Omitted**:
    - `artistry`: задача стандартная, не требует нестандартного подхода

  **Parallelization**:
  - **Can Run In Parallel**: NO (foundational)
  - **Parallel Group**: Wave 1 (alone)
  - **Blocks**: Tasks 2, 3
  - **Blocked By**: None

  **References**:
  - `cmd/follower/main.go:16` — текущий JSONHandler, заменить
  - `internal/worker/account_guard.go:30` — пример использования slog.TextHandler
  - `https://pkg.go.dev/log/slog#Handler` — интерфейс slog.Handler
  - `https://pkg.go.dev/log/slog#Record` — структура slog.Record

  **Acceptance Criteria**:
  - [ ] `internal/logger/compact_handler.go` компилируется
  - [ ] Реализует `slog.Handler` (проверить через type assertion)
  - [ ] Вывод совпадает с ожидаемым форматом

  **QA Scenarios**:

  ```
  Scenario: Basic INFO log with task_id
    Tool: Bash (go test)
    Steps:
      1. Создать logger с CompactHandler
      2. logger.Info("session.restored", "task_id", "abc123def45678980")
      3. Проверить stdout содержит: `INFO  session.restored  task:…678980`
    Expected: task_id truncated to last 8 chars with `task:…` prefix
    Evidence: .sisyphus/evidence/task-1-basic-info.log

  Scenario: Duration display only when present
    Tool: Bash (go test)
    Steps:
      1. logger.Info("warmup.succeeded", "duration_ms", 4688)
      2. Проверить вывод содержит `(4688ms)`
      3. logger.Info("task.started") (без duration_ms)
      4. Проверить вывод НЕ содержит `()` или `(ms)`
    Expected: Duration shown only when duration_ms attribute present
    Evidence: .sisyphus/evidence/task-1-conditional-duration.log

  Scenario: Account truncation
    Tool: Bash (go test)
    Steps:
      1. logger.Info("account acquired", "account", "33e5fb53abc12345")
      2. Проверить вывод содержит `acc:…abc12345`
    Expected: account truncated to last 8 chars with `acc:…` prefix
    Evidence: .sisyphus/evidence/task-1-account-truncation.log
  ```

  **Evidence to Capture**:
  - [ ] Вывод тестовых сценариев в `.sisyphus/evidence/task-1-*.log`

  **Commit**: YES (1)
  - Message: `feat(logger): add compact slog handler`
  - Files: `internal/logger/compact_handler.go`
  - Pre-commit: `go build -buildvcs=false ./...`

---

- [x] 2. Integrate CompactHandler in `cmd/follower/main.go`

  **What to do**:
  - Заменить строку 16:
    ```go
    // Было:
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    // Стало:
    logger := slog.New(logger.NewCompactHandler(os.Stdout))
    ```
  - Добавить импорт `"follower/internal/logger"`

  **Must NOT do**:
  - Не менять остальную логику main.go
  - Не трогать error handling

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: NO
  - **Parallel Group**: Wave 2 (with Task 3)
  - **Blocks**: Task 4
  - **Blocked By**: Task 1

  **References**:
  - `cmd/follower/main.go:16` — единственная строка для замены

  **Acceptance Criteria**:
  - [ ] `go build -buildvcs=false ./cmd/follower` — PASS

  **QA Scenarios**:

  ```
  Scenario: Build succeeds after integration
    Tool: Bash
    Steps:
      1. go build -buildvcs=false ./cmd/follower
      2. Проверить exit code = 0
    Expected: Build completes without errors
    Evidence: .sisyphus/evidence/task-2-build.log
  ```

  **Commit**: NO (group with 1)

---

- [x] 3. Create `internal/logger/compact_handler_test.go`

  **What to do**:
  - Тесты для CompactHandler:
    - TestCompactHandler_Format (базовый формат)
    - TestCompactHandler_TruncateTaskID (truncation)
    - TestCompactHandler_TruncateAccount (account truncation)
    - TestCompactHandler_DurationOnlyWhenPresent (conditional duration)
    - TestCompactHandler_LevelFiltering (Enabled() check)
    - TestCompactHandler_WithAttrs (attr propagation)

  **Must NOT do**:
  - Не менять существующие тесты бизнес-логики

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: YES (with Task 2)
  - **Parallel Group**: Wave 2 (with Task 2)
  - **Blocks**: Task 4
  - **Blocked By**: Task 1

  **References**:
  - `internal/logger/compact_handler.go` — тестируемый модуль
  - `internal/browser/session_restorer_test.go:278` — пример slog тестов в проекте

  **Acceptance Criteria**:
  - [ ] `go test ./internal/logger/...` — PASS, 6+ тестов

  **QA Scenarios**:

  ```
  Scenario: All logger tests pass
    Tool: Bash
    Steps:
      1. go test -v ./internal/logger/...
      2. Проверить: N tests passed, 0 failures
    Expected: All compact handler tests pass
    Evidence: .sisyphus/evidence/task-3-tests.log
  ```

  **Commit**: NO (group with 1)

---

- [x] 4. Final verification: build + test + log output check

  **What to do**:
  - `go build -buildvcs=false ./...` — проверить полный build
  - `go test ./...` — проверить что все тесты проходят
  - Запустить `follower` на короткое время и проверить вывод логов

  **Recommended Agent Profile**:
  - **Category**: `quick`
  - **Skills**: `[]`

  **Parallelization**:
  - **Can Run In Parallel**: NO (final verification)
  - **Parallel Group**: Wave 3
  - **Blocks**: None
  - **Blocked By**: Tasks 2, 3

  **References**:
  - `cmd/follower/main.go` — точка запуска
  - `Makefile` — существующие build/test команды

  **Acceptance Criteria**:
  - [ ] `go build -buildvcs=false ./...` → PASS
  - [ ] `go test ./...` → PASS
  - [ ] Логи выводят компактный формат при запуске

  **QA Scenarios**:

  ```
  Scenario: Full build and test pass
    Tool: Bash
    Steps:
      1. go build -buildvcs=false ./...
      2. go test ./...
      3. Проверить exit codes = 0
    Expected: Build and all tests pass
    Evidence: .sisyphus/evidence/task-4-full-build.log

  Scenario: Production log output is compact
    Tool: Bash
    Steps:
      1. Запустить follower с timeout 5s (или unit test с mock)
      2. Проверить что логи НЕ в JSON формате
      3. Проверить формат: HH:MM:SS LEVEL  event  attrs
    Expected: Logs are human-readable compact format
    Evidence: .sisyphus/evidence/task-4-log-output.log
  ```

  **Commit**: NO (2)
  - Message: `feat(logger): integrate compact handler in production`
  - Files: `cmd/follower/main.go`, `internal/logger/compact_handler_test.go`
  - Pre-commit: `go build -buildvcs=false ./... && go test ./...`

---

## Final Verification Wave

- [x] F1. **Plan Compliance** — `oracle`
- [x] F2. **Code Quality** — `unspecified-high`
- [x] F3. **Manual QA** — `unspecified-high`
- [x] F4. **Scope Fidelity** — `deep`

---

## Commit Strategy

- **1**: `feat(logger): add compact slog handler` — `internal/logger/compact_handler.go`
- **2**: `feat(logger): integrate compact handler in production` — `cmd/follower/main.go`, `internal/logger/compact_handler_test.go`

---

## Success Criteria

### Verification Commands
```bash
go build -buildvcs=false ./...          # Expected: PASS
go test ./internal/logger/...            # Expected: 6+ tests PASS
go test ./...                            # Expected: ALL PASS
```

### Final Checklist
- [ ] CompactHandler implements slog.Handler
- [ ] ID truncation работает (task_id, account)
- [ ] Duration отображается только при наличии
- [ ] Build проходит без ошибок
- [ ] Все существующие тесты проходят
- [ ] Логи в production компактные и читаемые
