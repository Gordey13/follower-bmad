В AGENTS.md описан стандартный паттерн, как агенту использовать эти инструменты вместе:
Найти работу: Выполнить br ready --json, чтобы получить список готовых задач.
Забронировать файлы: Через MCP-команду file_reservation_paths забронировать нужные файлы. В параметре reason обязательно указать ID задачи, например, br-123.
Объявить о начале: Отправить сообщение send_message в общий чат с темой [br-123], чтобы другие агенты знали, что задача в работе.
Работа и обновления: Работать над задачей и периодически отправлять отчеты о прогрессе, отвечая в ту же ветку (reply в thread br-123).
Завершить и сдать: Закрыть задачу в br: br close 123 --reason "Completed", затем снять бронь с файлов: release_file_reservations. И отправить финальное письмо: [br-123] Completed.

Только по br:
- Шаблон карточек: description + design + acceptance_criteria + notes.
- Добавил source-of-truth ссылки артефакты OpenSpec в каждую задачу и эпик, чтобы агент не терял контекст.
- Перевёл декомпозицию в br на фактический execution flow из tasks.md
- Сразу строить задачи от tasks.md + design.md + spec.md + proposal.md и построил задачи от фактического execution flow tasks.md
- Финальный operational статус к работе (open) для всех 6 задач.
- Проставил рабочие зависимости между задачами, чтобы br ready показывал корректный следующий шаг.
- Сразу учитывать ограничение CLI: br create не поддерживает --design/--acceptance-criteria/--notes (надо создавать и тут же br update) — это можно делать одним заранее подготовленным двухшаговым шаблоном.
- Вынести шаблон OpenSpec→BR в готовую командную “болванку”, чтобы в следующих change не тратить время на ручное составление длинных update-команд.


OpenSpec → BR, чтобы запускать на любом change почти без ручной правки.
# Usage:
#   CHANGE_DIR=/root/follower-bmad/openspec/changes/stackerr-rollout-interfaces-3
#   EPIC_TITLE="[OpenSpec] stackerr-tier3-interfaces: rollout"
#   SPEC_REL="specs/stackerr-tier3-interfaces/spec.md"
#   ./openspec_to_br.sh
set -euo pipefail
: "${CHANGE_DIR:?set CHANGE_DIR}"
: "${EPIC_TITLE:?set EPIC_TITLE}"
: "${SPEC_REL:?set SPEC_REL}"
PROPOSAL="$CHANGE_DIR/proposal.md"
DESIGN="$CHANGE_DIR/design.md"
TASKS="$CHANGE_DIR/tasks.md"
SPEC="$CHANGE_DIR/$SPEC_REL"
# 1) Epic
EPIC=$(br create \
  --type epic \
  --priority 1 \
  --title "$EPIC_TITLE" \
  --description "OpenSpec rollout tracking issue." \
  --external-ref "${CHANGE_DIR#/root/follower-bmad/}/$SPEC_REL" \
  --labels "openspec,stackerr" \
  --silent)
# 2) 6 task skeletons from standard tasks.md structure
T11=$(br create --type task --priority 2 --parent "$EPIC" --title "1.1 Scope Preparation: file-level migration checklist" --description "Task 1.1 placeholder" --silent)
T12=$(br create --type task --priority 2 --parent "$EPIC" --title "1.2 Scope Preparation: boundary/lifecycle confirmation" --description "Task 1.2 placeholder" --silent)
T21=$(br create --type task --priority 1 --parent "$EPIC" --title "2.1 Implementation: stackerr policy migration" --description "Task 2.1 placeholder" --silent)
T22=$(br create --type task --priority 1 --parent "$EPIC" --title "2.2 Implementation: tests under new error/log shape" --description "Task 2.2 placeholder" --silent)
T31=$(br create --type task --priority 1 --parent "$EPIC" --title "3.1 Validation: scope tests and evidence capture" --description "Task 3.1 placeholder" --silent)
T32=$(br create --type task --priority 2 --parent "$EPIC" --title "3.2 Closeout: change notes and DoD closure" --description "Task 3.2 placeholder" --silent)
# 3) Dependencies
br dep add "$T12" "$T11"
br dep add "$T21" "$T12"
br dep add "$T22" "$T21"
br dep add "$T31" "$T22"
br dep add "$T32" "$T31"
# 4) Enrich epic/task fields (source-of-truth)
br update "$EPIC" \
  --description "OpenSpec source-of-truth bundle. proposal=$PROPOSAL ; design=$DESIGN ; tasks=$TASKS ; spec=$SPEC" \
  --design "Use design.md as implementation authority." \
  --acceptance-criteria "All requirements/scenarios from spec.md satisfied; all checklist items in tasks.md 1.1..3.2 satisfied." \
  --notes "Read proposal+design+tasks+spec before implementation."
for T in "$T11" "$T12" "$T21" "$T22" "$T31" "$T32"; do
  br update "$T" \
    --description "Sources: proposal=$PROPOSAL ; design=$DESIGN ; tasks=$TASKS ; spec=$SPEC" \
    --design "Anchor to relevant design.md section." \
    --acceptance-criteria "Must satisfy corresponding tasks.md item and spec scenario." \
    --notes "Execution source: tasks.md"
done
# 5) Verify operational state
br ready --json
br list --status open --json



Только по Mail Agent:
- Корректно зарегистрировал проект и агента (ensure_project, register_agent).
- Быстро выявил, что self-message — плохой путь в текущем состоянии сервера.
- Переключился на правильную схему: создал второго агента (BoldSeal) и сделал contact handshake.
- После handshake отправка прошла успешно в нужный thread (br-follower-bmad-nsr).
ЧМСЛ (только по Mail Agent):
- Сразу избегать self-recipient в первом же send (сэкономило бы несколько фейлов).
- Раньше запускать macro_contact_handshake перед первой отправкой новому получателю.
- При socket-ошибках быстрее переходить к “короткий payload + non-self recipient + handshake”, а не повторять одинаковые ретраи.
- Ввести микро-чеклист перед send: recipient != sender → contact approved → send.

