## Why

Текущий change `add-error-stack-trace-logging` закрыл приоритетные runtime-hotspots, но по репозиторию всё ещё остаются пакеты с `fmt.Errorf(...%w...)`, «сырыми» `return err`, и lifecycle/transport точками, где структурированный `error` не эмитится единообразно. Нужен отдельный repo-wide rollout, чтобы довести политику stack-enabled ошибок до консистентного уровня во всех пакетах и убрать фрагментацию формата логов.

## What Changes

- Выполнить полный sweep по `internal/*` и связанным entrypoints для миграции ошибок на `stackerr.Wrap` / `stackerr.WithStack` в граничных точках возврата/логирования.
- Расширить использование lifecycle helper-подхода на все релевантные error-пути, сохраняя `error_code` и `diagnostic_message` как обязательные поля.
- Устранить оставшиеся разрывы между строковым и объектным представлением `error` в логах и JSON-ответах, где это применимо по контрактам.
- Обновить тесты, чувствительные к санитизации и форме ошибок (worker/browser/observability/http/cli), чтобы зафиксировать безопасный и стабильный формат.
- Добавить финальный rollout checklist по пакетам (coverage matrix) для проверки полноты миграции.

## Capabilities

### New Capabilities
- `repo-wide-error-stack-rollout`: Репозиторий ДОЛЖЕН использовать единый stackerr-подход для формирования, оборачивания и логирования ошибок во всех пакетах, а не только в hotspot-файлах.

### Modified Capabilities
- `<existing-name>`: None.

## Impact

- Затронутые области: `internal/browser`, `internal/worker`, `internal/config`, `internal/repository/postgres`, `internal/storage`, `internal/transport/http`, `internal/cli`, `internal/observability`, частично `cmd/*`.
- Лог-контракты: унификация объектного `error` (stack-enabled) при сохранении текущих полей жизненного цикла (`error_code`, `diagnostic_message`).
- Тесты: значительное обновление assert-логики и sanitization-покрытия в нескольких пакетах.
- Зависимости: новых внешних библиотек не требуется.
