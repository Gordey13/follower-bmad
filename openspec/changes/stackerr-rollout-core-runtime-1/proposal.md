## Why

Tier-1 закрывает самый высокий риск: core runtime ошибки в app/worker/browser/config, которые чаще всего определяют скорость диагностики инцидентов.

## What Changes

- Выполнить этапную миграцию stackerr в рамках **stackerr-rollout-tier1-core-runtime**.
- Применить единые правила boundary error handling в scope этого tier.
- Сохранить совместимость lifecycle/лог контрактов там, где это требуется.
- Добавить/обновить тесты по scope tier и зафиксировать критерии готовности.

## Capabilities

### New Capabilities
- `stackerr-tier1-core-runtime`: Tiered rollout capability for internal/app, internal/worker, internal/browser, internal/config.

### Modified Capabilities
- `<existing-name>`: None.

## Impact

- Scope: internal/app, internal/worker, internal/browser, internal/config
- Основной риск: контрактные и тестовые регрессии при переходе формата ошибок.
- Ожидаемый результат: консистентный stack-enabled error flow в пределах tier.
