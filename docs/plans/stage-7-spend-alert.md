# План: Этап 7 — алерты по порогу расхода (`stage-7/spend-alert`)

Реализация `docs/POST_MVP_PLAN.md §P3`.

## Цель

Дать пользователю опциональный порог месячного расхода. Если суммарный `CostUSD`
за окно строго больше порога — печатать ALERT-строку в stderr; с флагом
`--fail-on-alert` дополнительно вернуть ненулевой exit-код. По умолчанию (порог 0)
поведение `report`/`history` не меняется побайтово.

## Продуктовые решения (зафиксированы, не пересматривать)

- Конфиг: верхнеуровневый блок `alert.monthly_usd float64`.
- Порог отключён при `MonthlyUSD == 0`. Отрицательное значение — ошибка валидации.
- Алерт только при СТРОГО больше порога (равно — не алерт).
- ALERT-строка: `ALERT: total spend $%.2f exceeds monthly threshold $%.2f\n` в stderr.
- `--fail-on-alert` (bool, default false) у обеих команд: при превышении и флаге —
  вернуть ошибку `fmt.Errorf("monthly alert threshold exceeded")` ПОСЛЕ печати
  таблицы/графика и ALERT-строки. `Execute` сам печатает `error: ...` — не дублировать.
- `history --compare`: total считается от ТЕКУЩЕГО периода (`cur`), не прошлого.
- No-data-ветка: алерт не проверяется.
- Golden-файлы (`internal/report/testdata/*.golden`) — сравнивают stdout, алерт в
  stderr, поэтому не затрагиваются.

## Точки касания

- `internal/config/config.go`:
  - Тип `Config`: добавить поле
    `Alert struct { MonthlyUSD float64 \`yaml:"monthly_usd"\` } \`yaml:"alert"\``.
  - `validate(cfg)`: `if cfg.Alert.MonthlyUSD < 0 { return err }`.
- `internal/cli/report.go`:
  - Флаг `--fail-on-alert` в `reportCmd`; проброс в `runReport`.
  - После `report.Aggregate(all)` (в непустой ветке) посчитать total и вызвать
    общий хелпер алерта ПЕРЕД рендером таблицы для суммы, но печать ALERT и
    возврат ошибки — ПОСЛЕ рендера. Реализация: посчитать total, отрендерить
    таблицу, затем вызвать хелпер, который печатает ALERT и (если fail-on-alert)
    возвращает ошибку.
- `internal/cli/history.go`:
  - Флаг `--fail-on-alert` в `historyCmd`; проброс в `runHistory`.
  - Для обычной/by-model/chart ветки — total от `records`. Для `--compare` — total
    от текущих `records` (cur). Печать ALERT и возможная ошибка — после рендера.
- Общий хелпер (в `internal/cli/report.go`, рядом с `adminKeyHintFor`):

```go
// sumCost returns the total CostUSD across aggregated rows.
func sumCost(rows []report.Row) float64 {
    var t float64
    for _, r := range rows {
        t += r.CostUSD
    }
    return t
}

// checkAlert prints an ALERT line to stderr when total strictly exceeds the
// configured monthly threshold (threshold 0 = disabled). When failOnAlert is set
// and the threshold is exceeded, it returns a non-nil error so Execute exits
// non-zero. It must be called AFTER the table/chart is rendered.
func checkAlert(cmd *cobra.Command, threshold, total float64, failOnAlert bool) error {
    if threshold <= 0 || total <= threshold {
        return nil
    }
    fmt.Fprintf(cmd.ErrOrStderr(), "ALERT: total spend $%.2f exceeds monthly threshold $%.2f\n", total, threshold)
    if failOnAlert {
        return fmt.Errorf("monthly alert threshold exceeded")
    }
    return nil
}
```

`report.Row.CostUSD` подтверждён (`internal/report/report.go:21-25`).

## Тест-кейсы

`internal/config/config_test.go`:
- отрицательный `alert.monthly_usd` → `loadFrom` возвращает ошибку;
- отсутствие блока `alert` → `MonthlyUSD == 0`, `Load` не ломается;
- положительный `alert.monthly_usd` → парсится в `cfg.Alert.MonthlyUSD`.

`internal/cli/cli_test.go` (table-driven, где уместно):
- порог ниже total → нет ALERT в stderr, exit 0;
- порог равен total → нет ALERT (строго больше);
- порог выше... т.е. total выше порога → ALERT в stderr, без флага ошибки нет;
- total выше порога + `--fail-on-alert` → ALERT в stderr + `run` возвращает ошибку;
- порог не задан (0) → ни ALERT, ни изменения stdout (по образцу существующих);
- `history --compare` с порогом между cur и prev → алерт по cur (текущему).

## Критерий готовности

- `go build ./...`, `go vet ./...`, `go test ./...` зелёные; `gofmt -l .` пусто.
- Golden-файлы без изменений; новых зависимостей нет.
- `internal/provider/`, `internal/storage/`, `internal/report/` не тронуты.

## Документация (после зелёных тестов)

- `README.md`: пример блока `alert: monthly_usd: 200` + описание `--fail-on-alert`.
- `docs/POST_MVP_PLAN.md §P3`: отметить ✅, зафиксировать фактические решения.
- `docs/TECHNICAL_PLAN.md §6`: блок «Этап 7 — алерты по порогу расхода ✅».
