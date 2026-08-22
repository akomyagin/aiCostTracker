# Этап 8 — `--format=json` (POST_MVP §P4)

Ветка: `stage-8/json-format`.

## Цель

Машиночитаемый вывод команд `report`/`history` в JSON с версионируемым
контрактом `schema_version`. Позволяет строить свои дашборды поверх без парсинга
текстовой таблицы. Golden-тест фиксирует байтовый формат.

## Продуктовые решения (приняты, не пересматривать)

- В JSON выводятся только свёрнутые `Row` (провайдер → токены/стоимость) + `total`,
  симметрично дефолтной таблице. Per-day/per-model в JSON НЕ включаем (открытый
  вопрос из §P4 закрыт: расширять при появлении реального потребителя).
- `--format=json` несовместим с `--by-model` (обе команды) и с `--chart`/`--compare`
  (`history`) — JSON пока поддерживает только путь `Aggregate`→`[]Row`.
- `schema_version` начинается с `1`; при будущем breaking-изменении контракта
  поднимается версия, а не форма молча.
- No-data при `--format=json`: печатаем валидный пустой документ (`rows: []`,
  `total` — нули, `schema_version: 1`), а НЕ человекочитаемый текст.
- Алерт (Этап 7) работает при `--format=json` идентично table: ALERT в stderr,
  JSON в stdout не искажается.

## Точки касания

- `internal/report/report.go` — json-теги к `Row`; новая `func JSON(w io.Writer, rows []Row) error`.
- `internal/cli/report.go` — флаг `--format`, валидация, несовместимость, ветвление вывода, no-data-JSON.
- `internal/cli/history.go` — то же + несовместимость с `--chart`/`--compare`.
- Тесты: `internal/report/report_test.go` (или новый `json_test.go`), `internal/cli/cli_test.go`.
- Golden: `internal/report/testdata/report.json.golden` (новый).
- Доки: `README.md`, `docs/POST_MVP_PLAN.md §P4`, `docs/TECHNICAL_PLAN.md §6`.
- НЕ трогать: `internal/provider/`, `internal/storage/`, `ModelRow`/`ModelTable`,
  существующие golden-файлы.

## Сигнатуры

`internal/report/report.go`:

```go
type Row struct {
    Provider     string  `json:"provider"`
    InputTokens  int64   `json:"input_tokens"`
    OutputTokens int64   `json:"output_tokens"`
    CostUSD      float64 `json:"cost_usd"`
}

// JSON writes rows as a machine-readable schema_version-tagged document.
// schema_version starts at 1; bump it (not the shape) on any future breaking
// change to this contract, per docs/POST_MVP_PLAN.md §P4.
func JSON(w io.Writer, rows []Row) error
```

Внутренние типы документа (в report.go, unexported):

```go
type jsonTotal struct {
    InputTokens  int64   `json:"input_tokens"`
    OutputTokens int64   `json:"output_tokens"`
    CostUSD      float64 `json:"cost_usd"`
}
type jsonDoc struct {
    SchemaVersion int         `json:"schema_version"`
    Rows          []Row       `json:"rows"`
    Total         jsonTotal   `json:"total"`
}
```

Реализация `JSON`:
- `out := make([]Row, 0, len(rows))` затем `append(out, rows...)` — гарантия
  `[]` вместо `null` при пустом входе.
- сумма total по строкам.
- `json.MarshalIndent(doc, "", "  ")`, затем `w.Write(b)` и `w.Write([]byte("\n"))`.

CLI-сигнатуры (расширить новым `format string`):
- `runReport(cmd, period, byModel, failOnAlert, format)`
- `runHistory(cmd, period, chart, byModel, compare, failOnAlert, format)`

Общий валидатор в `internal/cli/report.go`:

```go
// validateFormat rejects unknown --format values before any network/DB work.
func validateFormat(format string) error
```

## Логика в CLI

Оба run-функции, в начале (до сети/БД):
1. `if err := validateFormat(format); err != nil { return err }`
2. несовместимость: `if format == "json" && byModel { return ... }`
   (у history добавить `|| chart || compare`). Оформить одной проверкой в стиле
   существующей `--compare`-проверки в history.
3. no-data-ветка: если `format == "json"` — `report.JSON(out, nil)` (пустой док),
   иначе прежний текст.
4. основной вывод: если `format == "json"` — `report.JSON(out, rows)`; иначе
   существующая логика table/model/chart/compare.
5. `checkAlert(...)` вызывается как раньше (в т.ч. в json-ветке).

## Формат вывода (фиксируется golden-ом)

```json
{
  "schema_version": 1,
  "rows": [
    {"provider": "anthropic", "input_tokens": 250, "output_tokens": 100, "cost_usd": 2.5}
  ],
  "total": {"input_tokens": 250, "output_tokens": 100, "cost_usd": 2.5}
}
```
(MarshalIndent с 2-пробельным отступом, финальный `\n`.)

## Тест-кейсы

`internal/report/json_test.go`:
- `TestJSON_Golden` — те же строки, что `TestTable_Golden` → `testdata/report.json.golden`, `-update`, побайтово.
- `TestJSON_EmptyRowsIsEmptyArrayNotNull` — `JSON(&buf, nil)`: содержит `"rows": []`, не `"rows": null`; `json.Unmarshal` в структуру проходит, `len(rows)==0`, `schema_version==1`.

`internal/cli/cli_test.go`:
- `TestReportCommand_FormatJSON` — `report --format=json`; `json.Unmarshal` вывода; проверить provider/значения/total/schema_version.
- `TestHistoryCommand_FormatJSON` — то же из store, без сети.
- `TestReportCommand_FormatJSONByModelConflict` / history вариант — `--format=json --by-model` → ошибка (`strings.Contains`).
- `TestHistoryCommand_FormatJSONChartCompareConflict` — `--chart` и `--compare` с json → ошибка.
- `TestReportCommand_FormatBogus` — `--format=bogus` → ошибка со списком (`table, json`).
- `TestReportCommand_FormatJSONNoData` / history — no-data + json → валидный пустой JSON, `rows` пуст, НЕ human-readable текст.
- `TestReportCommand_FormatJSONAlert` — алерт при json: ALERT в stderr, exit при `--fail-on-alert`, stdout — валидный JSON.

## Критерий готовности

- `go build ./...`, `go vet ./...`, `go test ./...` зелёные; `gofmt -l .` пусто.
- `git diff --stat internal/report/testdata/` — только новый `report.json.golden`.
- Нет новых зависимостей (`encoding/json` — stdlib).
- Существующие golden (`report_table.golden`, `report_model_table.golden`,
  `history_chart.golden`, `history_compare.golden`) без изменений.
</content>
</invoke>
