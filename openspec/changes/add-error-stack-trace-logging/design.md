## Context

Проект использует `log/slog` и смешанный стиль логирования ошибок: часть call sites пишет `"error", err`, но значимая доля рантайм-логов в воркере проходит через `observability.ErrorLifecycleAttrs` и передаёт только `error_code`/`diagnostic_message` без объекта ошибки. В результате стек потока выполнения теряется, даже когда в коде есть оборачивание через `%w`.

Поисковый анализ показал ключевые места миграции:
- Формирование/оборачивание ошибок: `internal/app/runtime_dependencies.go`, `internal/worker/execution_service.go`, `internal/worker/claim_loop.go`.
- Эмиссия lifecycle-ошибок без объекта `error`: `internal/worker/claim_loop.go`, `internal/worker/execution_service.go`, `internal/browser/session_restorer.go`, `internal/observability/events.go`.

Ограничения:
- Без новых внешних зависимостей.
- Сохранить совместимость с `errors.Is`/`errors.As`.
- Минимизировать изменения в существующих вызовах `slog`.

## Goals / Non-Goals

**Goals:**
- Добавить внутренний тип ошибки со стеком, сериализуемый в `slog` через `LogValuer`.
- Гарантировать одноразовый захват стека для цепочки оборачивания.
- Обновить критические рантайм-пути так, чтобы в логи попадал и `error_code`, и структурированная ошибка со стеком.
- Зафиксировать тестируемый и стабильный JSON-формат `error`.

**Non-Goals:**
- Полная миграция всех пакетов за один change.
- Внедрение кастомного `slog.Handler`.
- Изменение доменной модели `ErrorCode` и существующего контракта `diagnostic_message`.

## Decisions

1. **Новый пакет `internal/stackerr` с API `New/Wrap/WithStack`.**
   - Почему: минимальный и явный контракт в местах формирования ошибок.
   - Альтернатива: расширять `domain.DomainError` стеком.
   - Почему отклонена: `DomainError` покрывает не все рантайм-ошибки, а change должен работать поперечно по слоям.

2. **Сериализация через `slog.LogValuer`, не через custom handler.**
   - Почему: zero/minimal change в инициализации логгера и в большинстве `slog.*` вызовов.
   - Альтернатива: custom `slog.Handler` с обходом атрибутов.
   - Почему отклонена: выше сложность, больше риск регрессий и format drift.

3. **Стек — массив кадров `{function,file,line}` с фильтрацией runtime/stackerr и лимитом глубины.**
   - Почему: машиночитаемо, пригодно для Loki/ELK, без утечек чувствительных данных.
   - Альтернатива: строковый stack dump.
   - Почему отклонена: хуже парсится и агрегируется.

4. **Обновить `ErrorLifecycleAttrs` и call sites, чтобы передавать объект ошибки.**
   - Почему: иначе основная доля runtime WARN/ERROR останется без стека.
   - Альтернатива: мигрировать только места с `"error", err`.
   - Почему отклонена: даёт неполное покрытие и фрагментированный формат логов.

5. **Staged rollout по hotspot-файлам.**
   - Порядок: `internal/app/runtime_dependencies.go` → `internal/worker/execution_service.go` → `internal/worker/claim_loop.go` → observability helpers и остаточные call sites.
   - Почему: максимальная диагностическая ценность при минимальном диффе.

## Risks / Trade-offs

- **[Риск] Изменение типа поля `error` (строка → объект) ломает внешние парсеры.**
  → **Mitigation:** explicit release note, аудит потребителей логов, staged rollout и smoke-проверка дашбордов.

- **[Риск] Увеличение размера логов при burst ошибок.**
  → **Mitigation:** лимит глубины стека (например, 32), фильтрация служебных кадров.

- **[Риск] Непоследовательная миграция (часть путей без stackerr).**
  → **Mitigation:** задачи по hotspot-миграции + статическая проверка в follow-up (lint/pattern check).

- **[Риск] Потеря причинности при неправильном wrapping.**
  → **Mitigation:** тесты `errors.Is/As`, отдельные кейсы на `WithStack` и многократный `Wrap`.

## Migration Plan

1. Реализовать `internal/stackerr` и unit-тесты (`Error`, `Unwrap`, `Is/As`, `LogValue`, anti-dup stack).
2. Обновить hotspot-файлы формирования ошибок (`runtime_dependencies`, `execution_service`, `claim_loop`).
3. Расширить `observability.ErrorLifecycleAttrs` (или sibling helper) для приёма `error` и передачи в `slog`.
4. Обновить lifecycle call sites в worker/browser, сохранив существующие поля (`error_code`, `diagnostic_message`).
5. Добавить/обновить тесты формата логов и интеграционные проверки в ключевом runtime потоке.
6. Rollout: deploy в dev/stage, верификация дашбордов/алертов, затем production.

Rollback:
- Вернуть предыдущую сериализацию, отключив передачу структурированной ошибки в lifecycle helper.
- При необходимости временно оставить `error_code`/`diagnostic_message` как единственные опорные поля.

## Open Questions

- Нужен ли флаг конфигурации для dual-mode (`error` как строка vs объект) на переходный период?
- Какой максимальный лимит стека оптимален по размеру/полезности (16, 32, 64)?
- Сохраняем ли абсолютный путь в `file`, или нормализуем до repo-relative для снижения noise?
