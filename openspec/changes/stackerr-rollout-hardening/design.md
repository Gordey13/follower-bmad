## Context

Текущий umbrella rollout разбит на независимые этапы. Этот change покрывает scope: **residual sweep across repo + policy enforcement + documentation/reporting**.

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
