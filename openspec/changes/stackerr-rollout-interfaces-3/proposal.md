## Why

Tier-3 синхронизирует внешние интерфейсы с новой формой ошибок, чтобы сохранить предсказуемые контракты для transport/cli.

## What Changes

- Выполнить этапную миграцию stackerr в рамках **stackerr-rollout-tier3-interfaces**.
- Применить единые правила boundary error handling в scope этого tier.
- Сохранить совместимость lifecycle/лог контрактов там, где это требуется.
- Добавить/обновить тесты по scope tier и зафиксировать критерии готовности.

## Capabilities

### New Capabilities
- `stackerr-tier3-interfaces`: Tiered rollout capability for internal/transport/http, internal/cli.

### Modified Capabilities
- `<existing-name>`: None.

## Impact

- Scope: internal/transport/http, internal/cli
- Основной риск: контрактные и тестовые регрессии при переходе формата ошибок.
- Ожидаемый результат: консистентный stack-enabled error flow в пределах tier.
