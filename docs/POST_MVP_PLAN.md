# POST_MVP_PLAN — Фаза 2 и далее для `aiCostTracker`

> Всё, что сознательно вынесено за пределы MVP (Фаза 1, Этапы 0–4 в
> [`TECHNICAL_PLAN.md`](./TECHNICAL_PLAN.md)). Порядок и состав — ориентир, не
> обязательство; приоритет расставляется по мере dogfooding'а.
>
> Метки `P1…P5` — стабильные идентификаторы пунктов, **не** порядок реализации;
> ниже они идут по приоритету, поэтому `P5` (технический долг) стоит перед `P4`.

---

## Фаза 2 — расширение агрегатора

### P1. Больше провайдеров

Добавляются как новые адаптеры за портом `ProviderUsageSource` — по файлу на
провайдера в `internal/provider/`, без изменений в CLI/storage/report.

**Точки, которые трогает добавление одного провайдера (по образцу
`anthropic.go`/`openai.go`):**

1. Новый файл `internal/provider/<id>.go` — тип-адаптер с полями
   `adminKey/baseURL/client`, конструктор `New<Name>(opts Options)`, метод
   `ID() string` (стабильный lowercase-`id`), метод `Fetch(ctx, Window)`,
   проверка `var _ ProviderUsageSource = (*<Name>)(nil)`. Пагинацию оборачивать
   в `maxPaginationPages`/`errTooManyPages` (уже есть в `provider.go`); мёрж
   cost+tokens — через `mergeCostsAndTokens` (`adapter.go`).
2. `internal/provider/<id>_test.go` — `httptest.Server`-тесты по образцу
   `anthropic_test.go`: нормализация ответа, сценарий 429→200, фатальная ошибка,
   отсутствие ключа в выводе, cap пагинации.
3. `internal/config/config.go` — добавить `id` в `var knownProviders`
   (строка ~54). Env-override `AICOST_<ID>_ADMIN_KEY` и валидация подхватятся
   автоматически.
4. `internal/cli/cli.go` — добавить `case "<id>": return provider.New<Name>(opts), nil`
   в фабрику `newProvider` (switch ~строка 67).
5. `internal/cli/help.go` — дописать строку провайдера в `adminKeyHelp` и
   `adminKeyHint` (где взять admin-ключ, вид ключа).
6. `docs/API_NOTES.md` — новый раздел с проверенными эндпоинтами/авторизацией.
7. README — строка в таблице «где взять admin-ключ» и `config.yaml`-пример.

CLI/storage/report при этом **не меняются** — это и есть проверяемый инвариант
порта.

Кандидаты (решение по конкретным — при реализации, после проверки их usage-API):

- **Google Gemini** — ⏸ **отложено, не вписывается в текущий порт без
  архитектурного исключения** (проверено по живой документации 2026-08-22).
  Факты:
  - **Gemini API** (`generativelanguage.googleapis.com`, ключ AI Studio) —
    программного usage/cost-API **нет вообще**: расход виден только в веб-консоли
    AI Studio (`Dashboard → Usage`). Источник:
    `https://ai.google.dev/gemini-api/docs/billing`.
  - **Vertex AI** (`aiplatform.googleapis.com`) биллится через обычный Google
    Cloud Billing. Cloud Billing REST API (`billingAccounts`/`services`/`skus`)
    отдаёт только метаданные и прайс-каталог, **не историю расходов**. Единственный
    путь к реальным цифрам — **экспорт биллинга в BigQuery**
    (`https://cloud.google.com/billing/docs/how-to/export-data-bigquery`): это
    SQL-запросы к таблице, а не REST GET; бэкфилл до 5 дней; авторизация —
    service-account + OAuth2/IAM (`roles/billing.viewer` на чтение,
    `roles/billing.admin` на настройку экспорта), а не строка `admin_key`.
  - Cloud Billing Budget API (кандидат на «может это проще?») — тоже не подходит:
    его `Budget`-ресурс содержит только конфиг порога/уведомлений, полей с
    фактическим расходом нет.
  - **Вывод**: это не «ещё один файл-адаптер по образцу», а другой класс
    интеграции — BigQuery-клиент вместо `net/http`, service-account JSON вместо
    `admin_key`-строки, обязательный ручной шаг настройки export'а в GCP-консоли
    вне контроля CLI, многодневная задержка данных. Несоразмерно сложности
    остальных провайдеров и масштабу pet-проекта ($0/мес, admin_key-конвенция).
  - Пересмотреть, только если появится однопользовательский REST-эндпоинт с
    историей расходов (сейчас такого нет ни у Gemini API, ни у Vertex AI) —
    либо если проект осознанно решит завести BigQuery как отдельное
    архитектурное исключение с собственной секцией конфига.
- **OpenRouter** — ✅ реализован (Этап 5, ветка `stage-5/openrouter-provider`).
  Единый `POST /api/v1/analytics/query` (cost в USD + токены), management key,
  без курсорной пагинации (`limit` + `truncated`). Эндпоинт и авторизация
  проверены по живой документации 2026-08-22, `[ASSUMPTION]` про наличие
  usage-эндпоинта снят — детали в `API_NOTES.md §3`. Привлекателен тем, что сам
  агрегирует много моделей.

Каждый новый провайдер проходит тот же чек-лист §4 TECHNICAL_PLAN: подтвердить
эндпоинт по живой документации, снять `[ASSUMPTION]`, добавить `httptest`-тесты.

### P2. Тренды и графики в терминале — ✅ Этап 6 (кроме TUI)

- Команда `history` **уже есть с MVP** (Этап 3, `internal/cli/history.go`): читает
  накопленные SQLite-снапшоты за период и печатает ту же агрегированную таблицу,
  что и `report`, но **без обращения к сети**. Этап 6 добавил поверх этого
  визуализацию динамики (ветка `stage-6/history-trends`, детали — TECHNICAL_PLAN §6).
- **ASCII-график ✅** — `history --chart`: горизонтальный бар-чарт дневного расхода
  без зависимостей (`report.chart.go`). Выбран бар-чарт, а не спарклайн (спарклайн
  требует partial-блоков и не оставляет места подписям дат/сумм).
- **Разбивка по моделям ✅** — флаг `--by-model` у `history` и `report`
  (`AggregateByModel`/`ModelTable`, тип `ModelRow`; пустая модель → `(unknown)`).
- **Сравнение периодов ✅** — `history --compare` («этот месяц vs прошлый»): две
  таблицы + дельта по итоговому TOTAL. Для `month` предыдущий период — полный
  прошлый календарный месяц. `--compare` несовместим с `--by-model`/`--chart`.
- **TUI (`charmbracelet/bubbletea`) — НЕ реализован**, остаётся кандидатом Фазы 2:
  полноценный интерактивный дашборд поверх той же истории. Per-provider дельты в
  сравнении и `report --chart/--compare` тоже сознательно вне Этапа 6.
- Именно ради этого история пишется в SQLite с Фазы 1, а не только текущий срез.

### P3. Алерты по порогу расхода ✅ (Этап 7, ветка `stage-7/spend-alert`)

Реализовано. Фактические решения (см. план `docs/plans/stage-7-spend-alert.md`):

- Конфиг: `Alert struct { MonthlyUSD float64 \`yaml:"monthly_usd"\` } \`yaml:"alert"\``,
  валидация `>= 0` в `validate`. `MonthlyUSD == 0` (или отсутствие блока `alert`) —
  алерт выключен, поведение `report`/`history` не меняется побайтово.
- Срабатывает **строго при total > threshold** (равно — не алерт).
- ALERT-строка в **stderr**: `ALERT: total spend $%.2f exceeds monthly threshold $%.2f`.
  stdout (таблица/график) и golden-файлы не затрагиваются.
- Флаг `--fail-on-alert` (bool, default false) у обеих команд: при превышении и
  флаге команда печатает таблицу/график, затем ALERT-строку и возвращает ошибку
  `monthly alert threshold exceeded` (ненулевой exit-код для CI/cron). Без флага —
  только предупреждение в stderr, exit 0.
- `history --compare`: алерт считается по **текущему** периоду (`cur`), не по прошлому.
- No-data-ветка алерт не проверяет.
- Общий хелпер `checkAlert`/`sumCost` в `internal/cli/report.go`, вызывается после
  рендера в `runReport`/`runHistory`.

Исходный план (для истории):

- Порог в конфиге. Плейсхолдер-имена полей (финализировать при реализации):
  верхнеуровневый блок `alert:` в `Config` (`internal/config/config.go`) с полем
  `monthly_usd float64` (yaml `monthly_usd`), напр.:
  ```yaml
  alert:
    monthly_usd: 200
  ```
  Добавить в тип `Config` поле `Alert struct { MonthlyUSD float64 \`yaml:"monthly_usd"\` } \`yaml:"alert"\``
  и валидацию `>= 0` в `validate`.
- Точки касания: в `internal/cli/report.go`/`history.go` после `report.Aggregate`
  посчитать суммарный `CostUSD` за окно и, если `cfg.Alert.MonthlyUSD > 0` и сумма
  его превышает, вывести предупреждение в `cmd.ErrOrStderr()`.
- Опционально — код возврата ≠ 0 при превышении, чтобы вешать в свой скрипт/cron
  на стороне пользователя (сам инструмент демоном не становится).
- Критерий готовности: table-driven-тест на порог (ниже/равно/выше) + тест, что
  при отсутствии `alert` в конфиге поведение не меняется.
- **Открытый продуктовый вопрос:** формат нотификации — только stderr/exit-code,
  или ещё локальные desktop-нотификации (кросс-платформенно — отдельная морока).
  По умолчанию для первой итерации — только stderr + опциональный exit-code.

### P5. Точность денежных сумм ✅ (Этап 9, ветка `stage-9/money-precision`)

Реализовано. Фактические решения (см. план `docs/plans/stage-9-money-precision.md`):

- **Сквозной `int64` микро-долларов**: `1 USD = 1_000_000` единиц. Денежная
  сумма представлена как `int64` от точки первого появления как `float64`
  (парсинг ответа провайдера) через весь путь накопления
  (`adapter.mergeCostsAndTokens` → `storage` → `report.Aggregate`/
  `AggregateByModel`/`AggregateByDay` → `compare.totalCost` → `cli.sumCost`) и
  обратно в `float64`-доллары только в точках финального рендера. Поле
  `CostUSD float64` переименовано в `CostMicros int64` в `provider.UsageRecord`,
  `report.Row`, `report.ModelRow`, `report.DayTotal`.
- **Хелперы конвертации** — `internal/provider/money.go`: `DollarsToMicros`
  (единственный санкционированный float→money, `math.Round`, half away from zero)
  и `MicrosToDollars` (для рендера). float→micros вызывается ровно один раз на
  значение (декодирование ответа провайдера + сравнение порога алерта);
  micros→dollars — ровно один раз в точке рендера (`formatUSD`, JSON-DTO,
  масштабирование бар-чарта, процент в compare).
- **Миграция SQLite `REAL → INTEGER`**: колонка `cost_usd REAL` →
  `cost_micros INTEGER`. Старая БД пересобирается в `migrate` (внутри одной
  транзакции: `CREATE … usage_records_new` с `CAST(ROUND(cost_usd*1e6) AS INTEGER)`,
  `DROP`, `RENAME`), история пользователя сохраняется без потерь; повторное
  открытие идемпотентно (детект по `PRAGMA table_info`).
- **JSON-контракт §P4 не изменён**: отдельная DTO `report.jsonRow`/`jsonTotal`
  по-прежнему отдаёт `cost_usd` как `float64`-доллары, `schema_version: 1`.
  `Row` больше не сериализуется напрямую (json-теги сняты).
- **`Alert.MonthlyUSD` осознанно остался `float64`**: это вводимые пользователем
  доллары, не накапливаемая сумма; конвертируется в micros один раз при каждом
  сравнении в `cli.checkAlert`. Зафиксировано комментарием у поля.
- Весь пользовательский вывод (таблицы, чарт, compare, JSON, ALERT-строка)
  байт-в-байт прежний; все 5 golden-файлов нетронуты. Regression-тесты на дрейф
  (`+=` многих значений, дрейфующих во float64, теперь точны) — в
  `provider`/`report`/`storage`.

Исходная формулировка долга (для истории):

Найдено независимым `/code-review` на Этапе 1: `Anthropic`/`OpenAI` парсят
`amount`/`amount.value` в `float64` и суммируют по `(day, model)`, а
`internal/storage` хранит `cost_usd` как SQLite `REAL`. Для дашборда текущая
точность достаточна, но накопительное суммирование float даёт дрейф на
больших объёмах записей. Не блокирует MVP — зафиксировано как долг, а не
исправлено сразу, чтобы не тащить смену типа через parsing/aggregation/storage/
report ради Этапа 1. Кандидат-фикс: хранить как целое число микро-долларов
(`int64`, `amount * 1_000_000`) вместо `float64`/`REAL`.

### P4. `--format=json` ✅ (Этап 8, ветка `stage-8/json-format`)

Реализовано. Фактические решения (см. план `docs/plans/stage-8-json-format.md`):

- Флаг `--format` (`table` по умолчанию, либо `json`) у `report` и `history`;
  невалидное значение → ошибка со списком допустимых (`table, json`), проверяется
  **до** любых сетевых/БД-обращений.
- `report.JSON(w, rows)` пишет документ
  `{"schema_version":1,"rows":[{provider,input_tokens,output_tokens,cost_usd}],"total":{…}}`
  (json-теги добавлены прямо к `Row`; `total` — отдельный маленький struct без
  `provider`). `schema_version` начинается с `1` и поднимается при будущем
  breaking-изменении контракта. `MarshalIndent` с 2-пробельным отступом +
  финальный `\n`, зафиксировано golden-ом `testdata/report.json.golden`.
- **Открытый продуктовый вопрос закрыт:** в JSON выводятся **только** свёрнутые
  `Row` (симметрично дефолтной таблице), per-day/per-model НЕ включаются —
  расширять при появлении реального потребителя.
- `--format=json` **несовместим** с `--by-model` (обе команды) и с
  `--chart`/`--compare` (`history`): JSON пока поддерживает только путь
  `Aggregate`→`[]Row`. Комбинация даёт явную ошибку `--format=json cannot be
  combined with …`.
- No-data при `--format=json`: печатается валидный пустой документ
  (`"rows": []`, нулевой `total`, `schema_version: 1`), а не человекочитаемый
  текст. Пустой вход сериализуется как `[]`, не `null`.
- Алерт (Этап 7) работает при `--format=json` идентично table: ALERT в stderr,
  exit-код при `--fail-on-alert`, JSON в stdout не искажается.
- Без новых зависимостей (`encoding/json` — stdlib). Существующие golden-файлы
  без изменений.

Исходный план (для истории):

- Машиночитаемый вывод `report`/`history` со `schema_version` (версионируемый
  контракт, как JSON-вывод в `gitl`). Golden-тесты на схему.
- Позволяет пользователю строить свои дашборды поверх без парсинга таблицы.

**Точки касания и критерии готовности:**

1. `internal/report/report.go` — добавить `func JSON(w io.Writer, rows []Row) error`
   рядом с `Table`. Обёртка со `schema_version` (начать с `1`), напр.
   `{"schema_version":1,"rows":[{"provider","input_tokens","output_tokens","cost_usd"}],"total":{…}}`.
   Поля `Row` уже подходят — добавить json-теги к типу `Row`.
2. `internal/cli/report.go` и `internal/cli/history.go` — флаг
   `--format` (`string`, default `"table"`, допустимые `table|json`); в `runReport`/
   `runHistory` после `report.Aggregate` ветвиться на `report.Table` vs
   `report.JSON`. Невалидное значение → явная ошибка со списком допустимых.
3. `internal/report/report_test.go` — golden-файл `testdata/report.json.golden`
   (обновляется тем же флагом `-update`, сравнение байт-в-байт). Тест на
   стабильность порядка полей.
4. README — упомянуть `--format=json` в разделе «Использование».

Открытый продуктовый вопрос: включать ли в JSON per-day/per-model строки (сырой
`[]UsageRecord`), а не только свёрнутые `Row`. По умолчанию — только `Row`
(симметрично таблице); расширять при появлении реального потребителя.

## Дальше (кандидаты, не запланированы)

- Прогноз расхода на конец месяца по текущему темпу.
- Бюджеты/теги по проектам (если провайдер отдаёт метки использования).
- Мультивалютность (если у кого-то биллинг не в USD).
- Экспорт истории в CSV.
- Кросс-платформенные релизы с подписью (goreleaser + cosign) — если проект
  начнут ставить другие; пока dogfooding локальной сборкой достаточно.

## Что остаётся за рамками принципиально

- Веб-интерфейс, сервер, «облако», хостинг ключей.
- Фоновый демон/непрерывный мониторинг — обращения к provider API только по
  явной команде (иначе теряется контроль над числом дорогих admin-API вызовов).
- Телеметрия/аналитика на автора данных о расходах.
