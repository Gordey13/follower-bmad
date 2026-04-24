## ADDED Requirements

### Requirement: Все package boundary error-paths ДОЛЖНЫ быть stack-enabled
Система ДОЛЖНА обеспечивать, что в граничных точках возврата ошибок (public/service boundaries) ошибки возвращаются в stack-enabled форме через `stackerr.WithStack(...)` либо `stackerr.Wrap(...)` при добавлении контекста.

#### Scenario: Возврат ошибки из package boundary
- **WHEN** функция на package boundary возвращает ошибку, пришедшую из вложенного вызова
- **THEN** ошибка возвращается в stack-enabled виде
- **THEN** цепочка `errors.Is`/`errors.As` сохраняется без регрессий

### Requirement: Wrapping через `%w` ДОЛЖЕН быть заменён на stackerr policy в целевых пакетах
В пакетах, включённых в repo-wide rollout, паттерн `fmt.Errorf("...: %w", err)` ДОЛЖЕН быть заменён на `stackerr.Wrap(err, "...")` там, где добавляется контекст ошибки.

#### Scenario: Контекстное оборачивание ошибки
- **WHEN** код добавляет контекст к исходной ошибке
- **THEN** используется `stackerr.Wrap(...)` вместо `%w`-обёртки в scope текущего rollout
- **THEN** в логах доступен структурированный `error` с `stack_trace`

### Requirement: Lifecycle logging ДОЛЖЕН сохранять совместимость полей
Lifecycle-логирование ДОЛЖНО сохранять поля `error_code` и `diagnostic_message` и одновременно эмитить структурированный `error`, если доступен объект ошибки.

#### Scenario: Lifecycle событие с ошибкой
- **WHEN** lifecycle path эмитит WARN/ERROR с ошибкой
- **THEN** лог содержит `error_code`
- **THEN** лог содержит `diagnostic_message`
- **THEN** лог содержит структурированный `error` с stack metadata

### Requirement: Санитизация и тестовые контракты ДОЛЖНЫ быть сохранены repo-wide
Repo-wide rollout ДОЛЖЕН сохранять существующие требования к редактированию чувствительных данных и обновить тесты, которые проверяют форму логов/JSON, чтобы они отражали объектный `error` формат.

#### Scenario: Санитизация чувствительных данных после миграции
- **WHEN** ошибка содержит чувствительные токены в текстовом сообщении
- **THEN** `diagnostic_message` остаётся санитизированным
- **THEN** тесты sanitization-sensitive пакетов проходят без утечки секретов

#### Scenario: Совместимость тестов лог-формата
- **WHEN** выполняются тесты, проверяющие error/log JSON shape
- **THEN** тесты подтверждают объектный формат `error` в rollout-путях
- **THEN** тесты подтверждают неизменность обязательных полей контракта
