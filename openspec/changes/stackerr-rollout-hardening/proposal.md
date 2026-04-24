## Why

Hardening-этап нужен для закрытия residual-хвоста, формализации policy и финального подтверждения полноты миграции.

## What Changes

- Выполнить этапную миграцию stackerr в рамках **stackerr-rollout-hardening**.
- Применить единые правила boundary error handling в scope этого tier.
- Сохранить совместимость lifecycle/лог контрактов там, где это требуется.
- Добавить/обновить тесты по scope tier и зафиксировать критерии готовности.

## Capabilities

### New Capabilities
- `stackerr-hardening-and-policy`: Tiered rollout capability for residual sweep across repo + policy enforcement + documentation/reporting.

### Modified Capabilities
- `<existing-name>`: None.

## Impact

- Scope: residual sweep across repo + policy enforcement + documentation/reporting
- Основной риск: контрактные и тестовые регрессии при переходе формата ошибок.
- Ожидаемый результат: консистентный stack-enabled error flow в пределах tier.
