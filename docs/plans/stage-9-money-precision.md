# Этап 9 — Точность денежных сумм (устранение техдолга §P5)

Ветка: `stage-9/money-precision` (уже создана и выбрана). Git-коммиты НЕ делать —
commit/push/PR выполняет основная сессия после ревью.

## 1. Цель

Устранить технический долг `docs/POST_MVP_PLAN.md §P5`, найденный независимым
`/code-review` на Этапе 1:

> `Anthropic`/`OpenAI` парсят `amount`/`amount.value` в `float64` и суммируют по
> `(day, model)`, а `internal/storage` хранит `cost_usd` как SQLite `REAL`.
> Накопительное суммирование float даёт дрейф на больших объёмах записей.
> Кандидат-фикс: хранить как целое число микро-долларов (`int64`,
> `amount * 1_000_000`).

**Принятое продуктовое решение (утверждено пользователем, не пересматривать):**
миграция **сквозная**, а не только на уровне хранения. Денежная сумма
представляется как `int64` микро-долларов (`1 USD = 1_000_000` единиц) с точки,
где число ВПЕРВЫЕ появляется как float (парсинг ответа провайдера), через весь
путь накопления (`adapter.mergeCostsAndTokens` → `storage` →
`report.Aggregate`/`AggregateByModel`/`AggregateByDay` → `compare.totalCost` →
`cli.sumCost`), и обратно в `float64`-доллары **только** в точках финального
рендера (форматирование строки `$%.2f`, JSON-сериализация, масштабирование
бар-чарта).

Обоснование сквозной миграции: долг сформулирован именно как «накопительное
суммирование float даёт дрейф» — риск в операциях `+=`, а они происходят на
нескольких уровнях: (1) мёрж пагинации/дублей по одному `(day, model)` в
адаптерах (`costs[k] += …`), (2) агрегация в `report.Aggregate`/
`AggregateByModel`/`AggregateByDay`, которая на практике суммирует куда больше
записей, чем один провайдерский ответ (месяц × провайдеры × модели).
Половинчатый фикс (только колонка в SQLite) не убрал бы ни один из этих `+=`.

## 2. Жёсткие ограничения (инварианты)

1. **Все существующие golden-файлы остаются байт-в-байт без изменений** —
   это внешний контракт вывода, внутреннее представление его не меняет:
   - `internal/report/testdata/report_table.golden`
   - `internal/report/testdata/report_model_table.golden`
   - `internal/report/testdata/history_chart.golden`
   - `internal/report/testdata/history_compare.golden`
   - `internal/report/testdata/report.json.golden`

   `git diff --stat internal/report/testdata/` в конце должен быть пуст.
   Флаг `-update` НЕ использовать: golden сравнивает ВЫВОД, а не входные данные
   тестов, поэтому перевод фикстур на `CostMicros` не требует regen.
2. **JSON-контракт §P4 не ломать**: `report.JSON` по-прежнему отдаёт
   `cost_usd` в **долларах** (`float64`), `schema_version: 1` не поднимается —
   для потребителя JSON ничего не меняется.
3. **Миграция существующей локальной SQLite БД обязательна** — история
   пользователя не теряется (см. §5).
4. Новых зависимостей в `go.mod` нет.
5. Не трогать: `internal/cli/cli.go`, `internal/cli/help.go`,
   `internal/cli/period.go` (не связаны с деньгами), `internal/config/config.go`
   (см. §7), `internal/provider/httpclient.go`.
6. Язык: код/идентификаторы/комментарии — английский; доки — русский.

## 3. Ключевые решения

### 3.1 Тип и имя поля

`CostUSD float64` → **`CostMicros int64`** во всех четырёх типах:
`provider.UsageRecord`, `report.Row`, `report.ModelRow`, `report.DayTotal`.
Старое имя оставить нельзя: `CostUSD` звучит как доллары, а хранит
микро-доллары — гарантированный источник ошибок на следующих этапах.
Комментарий к полю в `UsageRecord`: `// cost in integer micro-USD (1 USD =
1_000_000); exact under accumulation, unlike float64 dollars`.

### 3.2 Хелперы конвертации — `internal/provider/money.go` (новый файл)

Место: пакет `provider`, рядом с `UsageRecord` — это точка первого появления
суммы, и `report`, `storage`, `cli` уже импортируют `provider`
(кросс-импорт существует), поэтому **реэкспорт из `provider`, без
дублирования копий** в других пакетах.

```go
package provider

import "math"

// microsPerDollar is the fixed scale of the integer money representation:
// 1 USD == 1_000_000 micro-dollars.
const microsPerDollar = 1_000_000

// DollarsToMicros converts a floating-point USD amount (as decoded from a
// provider response or user config) into integer micro-dollars, rounding to
// the nearest micro (math.Round: half away from zero). This is the ONLY
// sanctioned float->money conversion; call it at the point where a dollar
// amount first appears as float64 and never accumulate floats afterwards.
func DollarsToMicros(v float64) int64 {
    return int64(math.Round(v * microsPerDollar))
}

// MicrosToDollars converts integer micro-dollars back to float64 USD for
// rendering (table/JSON/chart scaling). Exact for |m| <= 2^53 micros (~$9e9).
func MicrosToDollars(m int64) float64 {
    return float64(m) / microsPerDollar
}
```

Зафиксированное поведение округления: `math.Round` — half away from zero;
`DollarsToMicros(0.0000005) == 1` (не банковское округление). Для сумм
провайдеров с ≤6 знаками после запятой конвертация без потерь в обе стороны.

### 3.3 Правило конвертации

- **float → micros**: ровно один раз, в момент декодирования ответа провайдера
  (три адаптера, §4) и один раз при сравнении порога алерта (§6.3).
- **micros → dollars**: ровно один раз на значение, в момент финального рендера
  (`formatUSD`, JSON-DTO, деление при масштабировании бар-чарта, расчёт
  процента в compare).
- Между этими точками — только `int64`-арифметика.

### 3.4 `formatUSD` — сигнатура меняется на micros

`internal/report/report.go`:

```go
// formatUSD renders a micro-dollar amount with a leading $ and two decimals.
// Conversion goes through float64 + Sprintf("%.2f") deliberately: it keeps
// rounding byte-identical to the pre-micros renderer for every existing
// golden fixture.
func formatUSD(m int64) string {
    return fmt.Sprintf("$%.2f", provider.MicrosToDollars(m))
}
```

Обоснование (важно, не «упрощать»): чисто целочисленное округление до центов
(`(m + 5000) / 10000`) — это half-up, а `%.2f` — round-half-even по IEEE:
например `$0.125` (125_000 micros) даёт `$0.12` у `%.2f` и `$0.13` у half-up.
Конвертация через `MicrosToDollars` + прежний `Sprintf` воспроизводит старый
вывод байт-в-байт по построению (для сумм с ≤6 десятичными знаками
`float64(m)/1e6` даёт тот же `float64`, что и прежний путь через парсинг).
Конвертацию держать внутри `formatUSD`, а не на вызывающей стороне — одна
точка вместо шести call-site'ов.

## 4. Изменения по файлам — `internal/provider/`

### 4.1 `provider.go`

- `UsageRecord.CostUSD float64` → `CostMicros int64` (комментарий — §3.1).

### 4.2 `adapter.go`

- `mergeCostsAndTokens(providerID string, costs map[dayModel]int64, tokens map[dayModel]tokenCounts) []UsageRecord`
  — тип map-значения `float64` → `int64`; в теле `CostUSD: costs[k]` →
  `CostMicros: costs[k]`. Суммирование дублей ключа теперь целочисленное.

### 4.3 `anthropic.go` (точка появления float №1)

- `fetchCosts` возвращает `map[dayModel]int64`.
- Строка ~122–126: после `strconv.ParseFloat(r.Amount, 64)` сразу
  `out[dayModel{day: day, model: r.Model}] += DollarsToMicros(amt)`.
  Комментарий к полю `Amount string` (строка ~79) оставить, дополнить
  «converted to integer micro-USD immediately after parse».

### 4.4 `openai.go` (точка №2)

- `fetchCosts` возвращает `map[dayModel]int64`.
- Строка ~120: `out[…] += DollarsToMicros(r.Amount.Value)` — `amount.value`
  уже `float64` из JSON, конвертация сразу после декодирования.

### 4.5 `openrouter.go` (точка №3)

- `openrouterRow.TotalUsage float64` — оставить `float64` (это форма ответа
  API), но в цикле по строкам (строка ~171):
  `costs[k] += DollarsToMicros(row.TotalUsage)`; тип `costs` →
  `map[dayModel]int64`.

### 4.6 Новый `money.go` — см. §3.2.

## 5. Изменения — `internal/storage/`

### 5.1 `sqlite.go` — схема и миграция

Новая схема (в `migrate`), колонка обязана быть `INTEGER`:

```sql
CREATE TABLE IF NOT EXISTS usage_records (
    provider      TEXT    NOT NULL,
    day           TEXT    NOT NULL, -- YYYY-MM-DD, UTC
    model         TEXT    NOT NULL, -- "" when the provider does not break out models
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micros   INTEGER NOT NULL DEFAULT 0, -- integer micro-USD, 1 USD = 1e6
    fetched_at    TEXT    NOT NULL,
    PRIMARY KEY (provider, day, model)
);
```

Критично: `REAL` оставить нельзя — из-за SQLite type affinity `int64`,
записанный в REAL-колонку, приводится к REAL и выше 2^53 теряет точность;
фикс с REAL-колонкой был бы бессмысленным.

**Миграция старой БД** — в `migrate(ctx)`, ДО `CREATE TABLE IF NOT EXISTS`:

1. Определить старую схему:
   ```go
   rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(usage_records)`)
   ```
   Сканировать колонки (`cid, name, type, notnull, dflt_value, pk`), искать
   `name == "cost_usd"`. Таблицы нет → PRAGMA вернёт 0 строк → свежая БД,
   миграция не нужна. Колонка `cost_micros` уже есть → БД новая, пропустить.
2. Если найдена `cost_usd` — пересобрать таблицу внутри одной транзакции
   (`BeginTx` … `Commit`, `defer Rollback`):
   ```sql
   CREATE TABLE usage_records_new (
       -- полная новая схема из блока выше, имя usage_records_new
   );
   INSERT INTO usage_records_new
       (provider, day, model, input_tokens, output_tokens, cost_micros, fetched_at)
   SELECT provider, day, model, input_tokens, output_tokens,
          CAST(ROUND(cost_usd * 1000000) AS INTEGER),
          fetched_at
   FROM usage_records;
   DROP TABLE usage_records;
   ALTER TABLE usage_records_new RENAME TO usage_records;
   ```
   `ROUND` в SQLite — half away from zero, согласован с `math.Round` в
   `DollarsToMicros`. `db.SetMaxOpenConns(1)` уже стоит — конкурентных
   читателей во время пересборки нет.
3. Затем обычный `CREATE TABLE IF NOT EXISTS` с новой схемой (покрывает
   свежую БД; после миграции — no-op).

### 5.2 `sqlite.go` — Save/Query

- В upsert: `cost_usd` → `cost_micros` (и в списке колонок, и в
  `DO UPDATE SET`); аргумент `r.CostUSD` → `r.CostMicros`. Семантика upsert
  не меняется: перезапись строки, НЕ аккумуляция.
- В `Query`: `SELECT … cost_micros …`, `rows.Scan(…, &rec.CostMicros)`.

### 5.3 `fake.go`

Изменений не требует: хранит `provider.UsageRecord` как есть — тип поля
поменяется вместе с `UsageRecord`. Проверить компиляцию, не более.

### 5.4 `storage.go`

Интерфейс `Store` сигнатур не меняет; поправить только doc-комментарии, если
они упоминают `cost_usd`/доллары.

## 6. Изменения — `internal/report/` и `internal/cli/`

### 6.1 `report.go`

- `Row`: `CostUSD float64 \`json:"cost_usd"\`` → `CostMicros int64` —
  **json-теги с `Row` снять полностью** (со всех полей): `Row` больше не
  сериализуется напрямую, оставленный тег `cost_usd` на поле с микро-долларами
  — мина для контракта §P4.
- `ModelRow.CostUSD` → `CostMicros int64`.
- `Aggregate` / `AggregateByModel`: `row.CostMicros += r.CostMicros` —
  целочисленно; остальная логика без изменений.
- `Table` / `ModelTable`: локальный аккумулятор `totalCost float64` →
  `totalCost int64`; вызовы `formatUSD(r.CostMicros)` / `formatUSD(totalCost)`
  (сигнатура `formatUSD` — §3.4).
- **JSON — отдельная DTO** (контракт §P4: `cost_usd` — доллары):
  ```go
  // jsonRow is the wire shape of one row of the --format=json document.
  // It exists separately from Row because the JSON contract (§P4, schema_version 1)
  // exposes cost as float64 dollars under "cost_usd", while Row now carries
  // integer micro-dollars internally.
  type jsonRow struct {
      Provider     string  `json:"provider"`
      InputTokens  int64   `json:"input_tokens"`
      OutputTokens int64   `json:"output_tokens"`
      CostUSD      float64 `json:"cost_usd"`
  }
  ```
  `jsonTotal` остаётся с `CostUSD float64 \`json:"cost_usd"\``.
  `jsonDoc.Rows` → `[]jsonRow`.
  В `JSON(w, rows)`: построить `out := make([]jsonRow, 0, len(rows))`;
  для каждой `Row` — `jsonRow{…, CostUSD: provider.MicrosToDollars(r.CostMicros)}`;
  тотал **суммировать в `int64` micros** и конвертировать один раз:
  `total.CostUSD = provider.MicrosToDollars(totalMicros)` (не суммировать уже
  сконвертированные float — иначе вернём тот самый дрейф в тотал).
  Пустой вход по-прежнему сериализуется как `[]`, не `null`
  (`make(..., 0, ...)` сохраняет это свойство).
- `schema_version` остаётся `1` — форма и семантика документа не изменились.

### 6.2 `chart.go`

- `DayTotal.CostUSD` → `CostMicros int64`.
- `AggregateByDay`: `byDay map[time.Time]float64` → `map[time.Time]int64`;
  `byDay[d] += r.CostMicros`.
- `BarChart`: `var max float64` → `var max int64`; масштабирование —
  единственное место с float-делением:
  `n = int(math.Round(float64(d.CostMicros) / float64(max) * chartWidth))`
  (деление точных целых; поведение «non-zero day ≥ 1 блок» и «все нули → пустая
  колонка» не меняется); рендер — `formatUSD(d.CostMicros)`.

### 6.3 `compare.go`

- `totalCost(rows []Row) int64` — целочисленная сумма `CostMicros`.
- `CompareTables`: `curTotal`, `prevTotal`, `delta` — `int64` (вычитание
  точное). Snap-to-zero сохранить, переформулировав на micros:
  ```go
  // Snap to whole cents so a real sub-cent delta (e.g. -100 micros = -$0.0001)
  // can't surface as a spurious "-$0.00"/"-0.0%" negative zero. (The float
  // rounding residue this used to guard against no longer exists.)
  if math.Round(provider.MicrosToDollars(delta)*100) == 0 {
      delta = 0
  }
  ```
- Процент: `p := float64(delta) / float64(prevTotal) * 100` (масштаб micros
  сокращается; оба операнда — точные целые в float64). Snap
  `math.Round(p*10) == 0 → p = 0` сохранить как есть.
- `signedUSD(m int64) string` — тело то же, `formatUSD(-m)` / `formatUSD(m)`.
- Проверить существующий кейс `compare_test.go` «2.0001 residue»: в micros
  `2_000_100 - 2_000_000 = -100` → snap до 0 → вывод не меняется.

### 6.4 `internal/cli/report.go`

- `sumCost(rows []report.Row) int64` — сумма `CostMicros` (имя оставить,
  комментарий поправить: «total cost in micro-USD»).
- `checkAlert(cmd *cobra.Command, thresholdUSD float64, totalMicros int64, failOnAlert bool) error`:
  **порог конвертировать в micros один раз** и сравнивать `int64` с `int64`:
  ```go
  if thresholdUSD <= 0 || totalMicros <= provider.DollarsToMicros(thresholdUSD) {
      return nil
  }
  fmt.Fprintf(cmd.ErrOrStderr(),
      "ALERT: total spend $%.2f exceeds monthly threshold $%.2f\n",
      provider.MicrosToDollars(totalMicros), thresholdUSD)
  ```
  Обоснование выбора направления конвертации: порог — одно введённое
  пользователем число, его конвертация — единственное округление без
  накопления; обратный вариант (total → доллары для сравнения) прогонял бы
  точную накопленную сумму через float и мог бы перевернуть строгое
  неравенство на границе. Семантика «строго больше; равно — не алерт»
  сохраняется, формат ALERT-строки в stderr — байт-в-байт прежний.
  Импорт `provider` в `report.go` cli уже есть.
- `runReport`: `total := sumCost(rows)` — тип меняется прозрачно.

### 6.5 `internal/cli/history.go`

Только прозрачное изменение типа `total` через `sumCost`; правок кода не
требуется (сигнатуры вызовов не меняются). Проверить компиляцию.

## 7. Что сознательно НЕ трогаем

- **`internal/config/config.go` — `Alert.MonthlyUSD float64` остаётся.**
  Это конфиг, вводимый пользователем в долларах, не накапливаемая сумма —
  дрейфа нет; конверсия в micros происходит один раз при каждом сравнении
  (§6.3). Зафиксировать это комментарием у поля `MonthlyUSD`, чтобы следующий
  проход не «дочинил»: `// Stays float64 deliberately: user-entered dollars,
  never accumulated; converted once per comparison (see cli.checkAlert).`
- `internal/cli/cli.go`, `help.go`, `period.go`, `internal/provider/httpclient.go`.
- Формат любого пользовательского вывода (таблицы, чарт, compare, JSON,
  ALERT-строка) — байт-в-байт.

## 8. Тесты

### 8.1 Новый `internal/provider/money_test.go`

Table-driven:
- Round-trip `MicrosToDollars(DollarsToMicros(v)) == v` для типичных сумм:
  `1.25`, `0.005`, `0`, `2.5`, `0.9`, `123.456789`.
- `DollarsToMicros`: `1.25 → 1_250_000`, `0.005 → 5_000`, `0 → 0`,
  отрицательные (`-1.25 → -1_250_000`).
- Округление на неоднозначных случаях (фиксирует выбранный `math.Round`,
  half away from zero): `0.0000005 → 1`, `0.0000004 → 0`,
  `-0.0000005 → -1`, `2.0001 → 2_000_100`.

### 8.2 Адаптеры — `anthropic_test.go`, `openai_test.go`, `openrouter_test.go`, `adapter_test.go`

- Обновить литералы в ожидаемых `UsageRecord`/map'ах:
  `CostUSD: 1.0` → `CostMicros: 1_000_000`, `0.5 → 500_000` и т.д.
  (`adapter_test.go` строки ~43/47/51; аналогично по всем адаптерам).
- `anthropic_test.go` строка ~134: диапазонную проверку
  `rec.CostUSD < 2.24 || rec.CostUSD > 2.26` заменить точным равенством
  `rec.CostMicros != 2_250_000` — точность теперь гарантирована, допуск
  не нужен (это само по себе демонстрация ценности миграции).
- **Regression-тест на дрейф мёржа** (в `adapter_test.go` или в тесте
  пагинации одного адаптера): много слагаемых по одному `(day, model)`,
  которые во float дают классическую ошибку — например три страницы с
  `amount = "0.1"` (`0.1+0.1+0.1 != 0.3` в float64): проверить
  `CostMicros == 300_000` точно. Для Anthropic удобно сделать через
  httptest-пагинацию (несколько страниц cost_report с повторяющимся
  `(day, model)`).

### 8.3 `internal/storage/storage_test.go`

- Обновить литералы (`CostUSD: 1.5` → `CostMicros: 1_500_000` и т.д.) в
  `TestStore_SaveAndQuery`, `TestStore_IdempotentUpsert`,
  `TestStore_QueryFiltersByProviderAndWindow`.
- Round-trip Save/Query на micros: сохранить запись с
  `CostMicros: 1_234_567`, прочитать, сравнить точным равенством.
- Идемпотентность upsert с новым типом — покрыта существующим
  `TestStore_IdempotentUpsert` после обновления литералов.
- **Новый тест миграции** `TestOpen_MigratesLegacyRealSchema`:
  1. Во временной директории (`t.TempDir()`) руками открыть `database/sql`
     и создать таблицу СТАРОЙ схемы (`cost_usd REAL NOT NULL DEFAULT 0`,
     остальные колонки как в §5.1), вставить строки с дробными значениями,
     включая непредставимые точно во float-центах: `(…, cost_usd = 1.5)`,
     `(…, cost_usd = 0.005)`, `(…, cost_usd = 2.0001)`; закрыть.
  2. Открыть тот же путь новым `storage.Open`.
  3. `Query` за окно: записи на месте, `CostMicros` равны
     `1_500_000`, `5_000`, `2_000_100` точно; количество строк не изменилось.
  4. Повторно открыть тот же путь (`Open` второй раз) — миграция
     идемпотентна, данные не задвоены и не искажены.
  5. (Проверка схемы) `PRAGMA table_info` больше не содержит `cost_usd`,
     содержит `cost_micros` с типом `INTEGER`.

### 8.4 `internal/report/`

- Обновить литералы во всех фикстурах: `report_test.go`, `json_test.go`,
  `chart_test.go`, `compare_test.go` (`CostUSD: 2.5` → `CostMicros: 2_500_000`,
  `2.0001 → 2_000_100` в `compare_test.go:48` и т.д.); ожидания вида
  `rows[0].CostUSD != 2.5` → `rows[0].CostMicros != 2_500_000`.
- Golden-тесты (`TestTable_Golden`, `TestModelTable_Golden`,
  `TestBarChart_Golden`, `TestCompareTables_Golden`, `TestJSON_Golden`) —
  прогнать БЕЗ `-update`; обязаны пройти на нетронутых golden-файлах.
- **Regression-тест на дрейф агрегации** `TestAggregate_NoFloatDrift`
  (в `report_test.go`): 10 000 записей одного провайдера по
  `CostMicros: 10_000` ($0.01); `Aggregate` обязан вернуть ровно
  `CostMicros == 100_000_000` ($100.00 точно). Комментарий: во float-версии
  `10_000 × 0.01` давало `100.00000000000335…`. Аналогичную проверку одним
  подтестом — для `AggregateByModel` и `AggregateByDay` (можно меньшим
  объёмом, например 1 000 × $0.001 = точно $1.00 = 1_000_000 micros).
- `TestJSON_EmptyRowsIsEmptyArrayNotNull` — без изменений по сути (проверить,
  что проходит).

### 8.5 `internal/cli/cli_test.go`

- Обновить литералы `CostUSD:` → `CostMicros:` во всех фикстурах
  (`1.25 → 1_250_000`, `0.9 → 900_000`, `0.5 → 500_000`, `3.0 → 3_000_000`
  и т.д., строки ~84…789), включая хелпер `total` в алерт-тестах
  (`TestReportCommand_Alert` строка ~452, `TestHistoryCommand_Alert` ~529:
  фикстура «$5.00» → `CostMicros: 5_000_000`).
- Алерт-тесты (Этап 7): table-driven кейсы порога (ниже 4.0 / равно 5.0 /
  выше 6.0, `--fail-on-alert`, порог 0 = выключено) должны пройти без
  изменения ожиданий — `threshold` в кейсах остаётся `float64`-долларами
  (это конфиг), меняется только тип фикстурной суммы.
- **Локальные структуры декодирования JSON-вывода** (строки ~605–610:
  `CostUSD float64 \`json:"cost_usd"\``) — НЕ менять: они читают внешний
  JSON-контракт, где `cost_usd` — доллары; ожидания `doc.Rows[0].CostUSD !=
  1.25`, `doc.Total.CostUSD != 5.0` и т.п. остаются как есть. Их зелёный
  прогон — прямое подтверждение, что контракт §P4 не сломан.

## 9. Документация (в рамках этого же этапа)

- `docs/POST_MVP_PLAN.md §P5` — пометить ✅ (Этап 9, ветка
  `stage-9/money-precision`) по образцу §P3/§P4: краткая сводка фактических
  решений (сквозные micros `int64`, `1 USD = 1e6`, хелперы в
  `internal/provider/money.go`, миграция SQLite `REAL → INTEGER` с
  пересборкой таблицы, JSON-контракт не изменён, `Alert.MonthlyUSD` осознанно
  остался `float64`); исходную формулировку долга оставить «для истории».
- `.claude/skills/go-cost-tracker-dev/SKILL.md §2` — в описании нормализации
  заменить `UsageRecord{…, CostUSD}` на `UsageRecord{…, CostMicros}` c
  пометкой про целые микро-доллары.
- README трогать не нужно (пользовательский вывод и конфиг не изменились);
  если в README всё же упоминается `cost_usd`-колонка БД — поправить.

## 10. Критерий готовности

1. `go build ./...`, `go vet ./...`, `go test ./...` зелёные;
   `gofmt -l .` пусто. (Команды: `export PATH="$HOME/sdk/go/bin:$PATH"`.)
2. `git diff --stat internal/report/testdata/` пуст — все 5 golden-файлов
   байт-в-байт нетронуты.
3. `grep -rn "CostUSD" internal/ cmd/` находит поле только в JSON-DTO
   (`report.jsonRow`/`jsonTotal`) и в локальных декодерах JSON в
   `cli_test.go` — т.е. только там, где значение действительно доллары.
4. Новых зависимостей в `go.mod` нет (`git diff go.mod go.sum` пуст).
5. Тест миграции legacy-БД (§8.3) проходит: история с REAL-схемой читается
   новым кодом без потерь, повторное открытие идемпотентно.
6. Regression-тесты на дрейф (§8.2, §8.4) проходят с точными равенствами.
7. Git-коммитов агент не делает.
