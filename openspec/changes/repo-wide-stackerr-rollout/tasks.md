## 1. Residual Scope Mapping and Rollout Matrix

- [ ] 1.1 Зафиксировать package-level coverage matrix по всем остаточным `fmt.Errorf %w`, `return err` и lifecycle error paths
- [ ] 1.2 Разбить residual-пакеты на rollout tiers (Tier-1/2/3) по риску и влиянию
- [ ] 1.3 Подтвердить boundary-rules для каждого пакета (где обязателен `WithStack`, где `Wrap`)

## 2. Repo-wide Migration in Application and Infrastructure Packages

- [ ] 2.1 Мигрировать оставшиеся error paths в `internal/config/*` и `internal/app/*` на stackerr policy
- [ ] 2.2 Мигрировать оставшиеся error paths в `internal/repository/postgres/*` и `internal/storage/*`
- [ ] 2.3 Мигрировать оставшиеся error paths в `internal/browser/*` и `internal/worker/*`, включая non-hotspot файлы

## 3. Logging and Lifecycle Compatibility Completion

- [ ] 3.1 Завершить унификацию lifecycle helpers/call sites, чтобы structured `error` присутствовал везде, где доступен объект ошибки
- [ ] 3.2 Проверить, что во всех rollout-путях сохраняются `error_code` и `diagnostic_message`
- [ ] 3.3 Обновить direct slog callsites вне lifecycle helpers до единой stackerr-compatible практики

## 4. Test Refactoring and Safety Gates

- [ ] 4.1 Обновить sanitization-sensitive тесты (worker/browser/observability/http) под repo-wide rollout
- [ ] 4.2 Обновить JSON/log shape assertions (transport/cli/client/admin) под объектный `error` формат в rollout-путях
- [ ] 4.3 Добавить regression tests для `errors.Is/As` и anti-dup stack semantics в residual пакетах
- [ ] 4.4 Прогнать поэтапные package test suites и финальный `go test ./...` без регрессий

## 5. Rollout Readiness and Documentation

- [ ] 5.1 Подготовить notes о полной миграции формата ошибок и совместимости потребителей логов
- [ ] 5.2 Проверить отсутствие новых внешних зависимостей и зафиксировать это в отчёте change
- [ ] 5.3 Сформировать финальный rollout report с закрытием coverage matrix по пакетам
