## Why

Tier-2 приводит persistence-слой к той же error policy, чтобы исключить потерю причинности и стека на границе repository/storage.

## What Changes

- Выполнить этапную миграцию stackerr в рамках **stackerr-rollout-tier2-persistence**.
- Применить единые правила boundary error handling в scope этого tier.
- Сохранить совместимость lifecycle/лог контрактов там, где это требуется.
- Добавить/обновить тесты по scope tier и зафиксировать критерии готовности.

## Capabilities

### New Capabilities
- `stackerr-tier2-persistence`: Tiered rollout capability for internal/repository/postgres, internal/storage.

### Modified Capabilities
- `<existing-name>`: None.

## Impact

- Scope: internal/repository/postgres, internal/storage
- Основной риск: контрактные и тестовые регрессии при переходе формата ошибок.
- Ожидаемый результат: консистентный stack-enabled error flow в пределах tier.
