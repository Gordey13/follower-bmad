## Context

После внедрения `stackerr` в hotspot-пути остаётся длинный хвост пакетов с неоднородной обработкой ошибок: часть кода всё ещё использует `%w`/`return err` без stack boundary, а часть логов не эмитит структурированный `error`. Это создаёт неполную наблюдаемость и усложняет пост-инцидентную диагностику.

Анализ тестов показал высокий риск регрессий в местах, где есть проверка:
- санитизации (`credentials`, `secret`, токены),
- shape логов/JSON-ответов,
- lifecycle-полей (`error_code`, `diagnostic_message`).

## Goals / Non-Goals

**Goals:**
- Довести `stackerr`-подход до консистентного repo-wide стандарта.
- Установить единый boundary rule: в граничных точках возврата/логирования ошибки должны быть stack-enabled.
- Сохранить существующие lifecycle-поля и sanitization-поведение.
- Закрыть тестовым покрытием не только hotspot, но и residual-пакеты.

**Non-Goals:**
- Полный редизайн domain error model.
- Внедрение внешних error/logging библиотек.
- Нефункциональные рефакторинги, не связанные с обработкой ошибок.

## Decisions

1. **Package-by-package rollout с coverage matrix.**
   - Почему: снижает риск массовых регрессий и позволяет поэтапно валидировать контракты.
   - Альтернатива: одномоментная миграция всего дерева.
   - Почему отклонена: слишком высокий blast radius.

2. **Boundary-first migration rule.**
   - Применять `stackerr.WithStack` для входящих ошибок на boundary return paths.
   - Применять `stackerr.Wrap` при добавлении контекста вместо `%w` там, где это безопасно и уместно.

3. **Lifecycle compatibility strict mode.**
   - В lifecycle логах обязательно сохранять `error_code` и `diagnostic_message`.
   - Структурированный `error` добавлять дополнительно, через helper-паттерн.

4. **Sanitization-first logging policy.**
   - В логах запрещено выводить сырой текст с чувствительными значениями.
   - Для error payload использовать stack-enabled объект + существующие sanitizer-хелперы.

5. **Test gate как обязательный критерий по этапам.**
   - Для каждого этапа: unit + package tests + full `go test ./...` перед переходом дальше.

## Risks / Trade-offs

- **[Риск] Непреднамеренное раскрытие чувствительных данных через `error.msg`/`cause`.**  
  → **Mitigation:** санитизация в lifecycle helpers, целевые тесты на redaction, запрет на прямой вывод необработанных секретов.

- **[Риск] Поломка потребителей, ожидающих строковый `error`.**  
  → **Mitigation:** staged rollout, explicit change notes, контрактные тесты в transport/cli.

- **[Риск] Частичная миграция оставит «серые зоны».**  
  → **Mitigation:** coverage matrix по пакетам + checklist completion criteria.

- **[Риск] Рост времени CI из-за расширения тестов.**  
  → **Mitigation:** пакетные прогоны по этапам + финальный full-suite только на merge gate.

## Migration Plan

1. Собрать остаточную карту миграции по пакетам (residual map + priority tiers).
2. Мигрировать boundary/wrap paths в пакетах Tier-1 (worker/browser/config/repository).
3. Мигрировать Tier-2 (storage/transport/http/cli/aux helpers).
4. Обновить/добавить тесты по каждому пакету сразу после миграции.
5. Прогнать полный test suite и верифицировать лог-контракты.
6. Выпустить change notes и финальный rollout report.

Rollback:
- Возможен package-scoped revert (поэтапно) без отката всего change.
- При критической несовместимости временно оставить stack-enabled только в уже стабильных пакетах.

## Open Questions

- Нужен ли feature flag для частичного включения structured `error` в transport/cli ответах?
- Где провести формальную границу «boundary return path» для helper-функций нижнего уровня?
- Нужна ли автоматизированная статическая проверка (`%w`/`return err`) как follow-up lint rule?
