# План: Этап 6 — тренды и графики в терминале (`stage-6/history-trends`)

> План для исполняющего агента. Ветка `stage-6/history-trends` уже создана и
> выбрана — **не переключаться, git-коммиты не делать** (commit/push/PR — вне
> зоны исполнителя). Язык: код/идентификаторы/комментарии — английский;
> документация — русский. Конвенции кода — `.claude/skills/go-cost-tracker-dev/SKILL.md`
> (table-driven + golden + `-update`, `text/tabwriter`, stdlib-first).

## 1. Цель

Реализовать `docs/POST_MVP_PLAN.md §P2` целиком, три возможности одним этапом:

1. **ASCII-график** динамики расхода по дням поверх команды `history`
   (флаг `--chart`), без внешних зависимостей.
2. **Разбивка по моделям** (флаг `--by-model` у `report` и `history`):
   сейчас `report.Aggregate` сворачивает всё в одну строку на провайдера,
   хотя `UsageRecord.Model` уже пишется в SQLite-историю.
3. **Сравнение периодов** («этот месяц vs прошлый», флаг `--compare` у
   `history`): две таблицы + дельта по итоговому TOTAL.

TUI (bubbletea) — **НЕ в этом этапе** (отдельный кандидат §P2). Новых внешних
зависимостей не добавлять: рендер — `text/tabwriter` + Unicode-блок `█`.

## 2. Границы и инварианты (что НЕ трогать)

- **`internal/provider/`** — не менять. `UsageRecord{Provider, Day, Model,
  InputTokens, OutputTokens, CostUSD}` и `Window{Start, End}` уже содержат всё
  нужное (`Day` — UTC-полночь, окно полуоткрытое `[Start, End)`).
- **`internal/storage/`** — не менять. `Store.Query(ctx, providerID, window)`
  уже отдаёт все записи с `Model`/`Day`; вся новая агрегация ложится в
  `internal/report`. Новые методы хранилищу не нужны.
- **`internal/config/`** — не менять (новых полей конфига нет; всё — CLI-флаги).
- **Обратная совместимость дефолтного вывода**: существующие сигнатуры
  `report.Row`, `report.Aggregate([]provider.UsageRecord) []Row`,
  `report.Table(io.Writer, []Row) error` и golden-файл
  `internal/report/testdata/report_table.golden` — **не менять ни на байт**.
  Без новых флагов `report`/`history` печатают ровно то же, что сейчас,
  включая no-data-сообщения (`internal/cli/report.go:104`,
  `internal/cli/history.go:62` — тексты не менять).
- `history` по-прежнему **не ходит в сеть** ни при каком флаге.
  `report --compare`/`report --chart` **не делаем**: сравнение потребовало бы
  второй платный fetch за прошлый период — сознательно вне scope, истории
  для этого и существуют (см. §P2: «именно ради этого история пишется в SQLite»).

## 3. Принятые архитектурные решения (не пересматривать по ходу)

1. **График — горизонтальный бар-чарт, по строке на день**, а не однострочный
   спарклайн: спарклайн требует 8 уровней partial-блоков и не оставляет места
   подписям дат/сумм; помесячный (30 строк) вертикальный список читается в
   любом терминале и golden-тестируется побайтово. Рендер — новая функция в
   `internal/report/chart.go`, масштаб — относительный к максимуму окна,
   ширина бара ≤ 40 рун `█` (U+2588).
2. **Разбивка по моделям — отдельная пара функций** `AggregateByModel`/
   `ModelTable` с собственным типом `ModelRow`, не расширение `Row` (иначе
   ломаются сигнатура и golden существующей таблицы). Пустой `Model` («провайдер
   не сообщил») агрегируется под ключом `""` и рендерится как `(unknown)`.
3. **Сравнение — «kind-aware» прошлое окно**: `previousWindow(period, now)` в
   `internal/cli/period.go` парсит период заново, а не сдвигает готовый
   `Window`, потому что для `month` «прошлый период» = **прошлый календарный
   месяц целиком** (`[1-е прошлого месяца, 1-е текущего)`), а не «столько же
   дней назад». Асимметрия «месяц-to-date vs полный прошлый месяц» осознанна и
   видна пользователю: в заголовках таблиц печатаются точные диапазоны дат.
4. **Дельта — только по итоговому TOTAL COST**, абсолютная и процентная.
   Per-provider дельты не делаем (раздувают вывод и матрицу тестов; кандидат
   на потом). При нулевом прошлом TOTAL процент — `n/a`.
5. **Матрица флагов**: `--compare` **несовместим** с `--by-model` и `--chart`
   (явная ошибка) — иначе 4 формата вывода на одну команду; `--chart` +
   `--by-model` совместимы (график всегда по дневным итогам, таблица — по
   моделям). При пустых записях текущего окна любой набор флагов даёт
   существующее no-data-сообщение и выход без таблиц/графиков.

## 4. Изменения по файлам

### 4.1 `internal/report/report.go` — разбивка по моделям

Добавить (существующее не трогать):

```go
// ModelRow is one aggregated line of a per-model report: total spend/usage for
// a (provider, model) pair over the reported window.
type ModelRow struct {
    Provider     string
    Model        string // "" if the provider did not report a model
    InputTokens  int64
    OutputTokens int64
    CostUSD      float64
}

// AggregateByModel collapses raw per-day records into one ModelRow per
// (provider, model) pair. Rows are sorted by provider id, then model, for
// deterministic output. Records with an empty provider are ignored; an empty
// model is kept as its own bucket (rendered as "(unknown)" by ModelTable).
func AggregateByModel(records []provider.UsageRecord) []ModelRow

// ModelTable writes per-model rows as an aligned text table with a TOTAL footer.
func ModelTable(w io.Writer, rows []ModelRow) error
```

- `AggregateByModel` — по образцу `Aggregate` (map по составному ключу
  `provider + "\x00" + model` или `map[[2]string]`), сортировка
  `sort.Slice` по `(Provider, Model)`.
- `ModelTable` — `text/tabwriter` c теми же параметрами
  (`NewWriter(w, 0, 0, 2, ' ', 0)`), колонки:
  `PROVIDER\tMODEL\tINPUT TOKENS\tOUTPUT TOKENS\tCOST (USD)`; строка TOTAL с
  пустой колонкой MODEL: `TOTAL\t\t<in>\t<out>\t<cost>`. Пустой `Model`
  печатать как `(unknown)`. Суммы — `formatUSD` (уже есть).

Ожидаемый вид:

```
PROVIDER   MODEL      INPUT TOKENS  OUTPUT TOKENS  COST (USD)
anthropic  claude     250           100            $2.50
openai     (unknown)  10            5              $0.10
openai     gpt-4o     100           40             $1.00
TOTAL                 360           145            $3.60
```

### 4.2 `internal/report/chart.go` — новый файл, ASCII-график

```go
// DayTotal is total spend across all providers/models for one UTC day.
type DayTotal struct {
    Day     time.Time // UTC midnight
    CostUSD float64
}

// AggregateByDay sums records into one DayTotal per UTC day of the window,
// including zero-spend days, so charts have no gaps. Records outside
// [w.Start, w.End) are ignored. Result is ordered chronologically.
func AggregateByDay(records []provider.UsageRecord, w provider.Window) []DayTotal

// BarChart renders day totals as an aligned horizontal bar chart. Bars are
// scaled to the maximum day (longest bar = chartWidth runes); any non-zero
// day gets at least one block so small spend stays visible.
func BarChart(w io.Writer, days []DayTotal) error
```

Детали реализации:

- `const chartWidth = 40` (рун `█`).
- `AggregateByDay`: перечислить дни циклом `d := w.Start; d.Before(w.End);
  d = d.AddDate(0, 0, 1)`; суммы собрать в `map[time.Time]float64` по
  `r.Day` (записи с `Day` вне окна игнорировать — защитная мера, хотя
  `Store.Query` уже фильтрует). Нормализация `Day` не нужна — storage хранит
  UTC-полночь; ключевать по `r.Day.UTC().Truncate(24*time.Hour)` для
  устойчивости.
- `BarChart`: `text/tabwriter` с теми же параметрами, заголовок
  `DAY\tCOST (USD)\tCHART`, строка: `2025-08-14\t$1.25\t██████` (дата —
  `Day.Format("2006-01-02")`). Длина бара:
  `round(cost / max * chartWidth)`, для `cost > 0` минимум 1 блок; при
  `max == 0` (все дни нулевые) — пустая колонка CHART у всех строк, деления
  на ноль нет. `strings.Repeat("█", n)`.
- Пустой вход (`len(days) == 0`) — заголовок + Flush, без паники (в CLI до
  этого не дойдёт из-за no-data-ветки, но функция должна быть тотальной).

Ожидаемый вид (`--period=7d`, максимум $2.00):

```
DAY         COST (USD)  CHART
2025-08-09  $0.00       
2025-08-10  $0.50       ██████████
2025-08-11  $2.00       ████████████████████████████████████████
2025-08-12  $0.00       
2025-08-13  $0.02       █
2025-08-14  $1.00       ████████████████████
2025-08-15  $0.00       
```

(Примечание: у нулевых дней tabwriter оставляет хвостовые пробелы колонки —
это нормально, golden фиксирует фактические байты.)

### 4.3 `internal/report/compare.go` — новый файл, сравнение периодов

```go
// CompareTables renders two aggregated tables (current and previous period)
// with window labels, followed by a one-line TOTAL cost delta. Labels are
// human-readable date ranges supplied by the caller.
func CompareTables(w io.Writer, curLabel, prevLabel string, cur, prev []Row) error
```

- Формат вывода (точный контракт, фиксируется golden-тестом):

  ```
  CURRENT [2025-08-01..2025-08-15]
  <report.Table(cur)>

  PREVIOUS [2025-07-01..2025-07-31]
  <report.Table(prev)>

  TOTAL: $3.50 vs $2.00 (+$1.50, +75.0%)
  ```

  То есть: строка `CURRENT [<curLabel>]`, таблица через существующий
  `Table` (переиспользовать, не дублировать рендер), пустая строка, строка
  `PREVIOUS [<prevLabel>]`, таблица, пустая строка, строка дельты.
- Дельта: `delta = curTotal - prevTotal` (суммы `CostUSD` по строкам).
  Абсолютная часть — знак всегда явный: `+$1.50` / `-$0.75` / `+$0.00`
  (формат: знак + `formatUSD(abs)`). Процент — `fmt.Sprintf("%+.1f%%",
  delta/prevTotal*100)`; при `prevTotal == 0` — литерал `n/a`:
  `(+$1.50, n/a)`.
- Пустой `prev` (нет истории за прошлый период) — не ошибка: `Table(nil)`
  уже печатает заголовок и нулевой TOTAL (существующее поведение,
  `TestTable_EmptyRowsHasTotal`), дельта считается от нуля с `n/a`.

### 4.4 `internal/cli/period.go` — прошлое окно и подпись окна

```go
// previousWindow returns the comparison window immediately preceding the one
// parsePeriod builds for the same period string:
//   - "today"     -> yesterday, [today-1d, today)
//   - "month"     -> the full previous calendar month, [1st prev, 1st cur)
//   - "Nd"        -> the N days before the current window, [start-Nd, start)
// Note the deliberate asymmetry for "month": current is month-to-date while
// previous is the full month; the CLI prints both date ranges so this is
// visible to the user.
func previousWindow(period string, now time.Time) (provider.Window, error)

// formatWindow renders a half-open window as an inclusive date range,
// e.g. "2025-08-01..2025-08-15" (End is exclusive, so the last day is End-1d).
func formatWindow(w provider.Window) string
```

- `previousWindow` реализовать через `parsePeriod(period, now)` + свёртку:
  - тот же `switch` по нормализованному периоду (или вызвать `parsePeriod` и
    досчитать): для `Nd` — `length := w.End.Sub(w.Start)`;
    `{Start: w.Start.Add(-length), End: w.Start}`; для `today` это частный
    случай `1d`-логики (`[today-1d, today)`); для `month` —
    `first := <1-е текущего месяца>`; `{Start: first.AddDate(0, -1, 0),
    End: first}` (в UTC; `AddDate(0,-1,0)` от 1-го числа безопасен —
    переполнения дней нет).
  - Невалидный период возвращает ту же ошибку, что `parsePeriod` (валидация
    не дублируется — просто пробросить).
- `formatWindow`: `w.Start.Format("2006-01-02") + ".." +
  w.End.AddDate(0, 0, -1).Format("2006-01-02")`.

### 4.5 `internal/cli/history.go` — флаги `--chart`, `--by-model`, `--compare`

По образцу существующего `--period` (`cmd.Flags().StringVar`, тут —
`cmd.Flags().BoolVar`):

```go
var (
    period  string
    chart   bool
    byModel bool
    compare bool
)
// ...
cmd.Flags().StringVar(&period, "period", "30d", "period to show: Nd (e.g. 7d), month, or today")
cmd.Flags().BoolVar(&chart, "chart", false, "append an ASCII bar chart of daily spend")
cmd.Flags().BoolVar(&byModel, "by-model", false, "break the table down by (provider, model)")
cmd.Flags().BoolVar(&compare, "compare", false, "compare with the previous period of the same kind")
```

`runHistory(cmd *cobra.Command, period string, chart, byModel, compare bool) error`
(или маленькая структура `historyOpts` — на вкус исполнителя, но сигнатура
одна, без глобальных состояний). Логика после `store.Query` текущего окна:

1. Валидация комбинаций **до** запросов:
   `if compare && (byModel || chart) { return fmt.Errorf("--compare cannot be combined with --by-model or --chart") }`.
2. Существующая no-data-ветка (`len(report.Aggregate(records)) == 0` — точный
   текст сообщения не менять) срабатывает при любых флагах: сообщение и
   `return nil`, без таблиц/графика/сравнения.
3. `--compare`: `prevWin, err := previousWindow(period, a.Now())`, второй
   `store.Query(ctx, "", prevWin)`, затем
   `report.CompareTables(out, formatWindow(window), formatWindow(prevWin),
   report.Aggregate(records), report.Aggregate(prevRecords))`. Пустая история
   прошлого окна — не ошибка (нулевая таблица + `n/a`).
4. Иначе основная таблица: `byModel ? report.ModelTable(out,
   report.AggregateByModel(records)) : report.Table(out, report.Aggregate(records))`.
5. `--chart`: после таблицы — `fmt.Fprintln(out)` (пустая строка-разделитель)
   и `report.BarChart(out, report.AggregateByDay(records, window))`.

Обновить `Long` команды (упомянуть флаги) — по-прежнему на английском, кратко.

### 4.6 `internal/cli/report.go` — флаг `--by-model`

Только один новый флаг: `cmd.Flags().BoolVar(&byModel, "by-model", false,
"break the table down by (provider, model)")`; в `runReport` после сбора
`all` — та же развилка `AggregateByModel`/`ModelTable` vs `Aggregate`/`Table`.
No-data-ветка (существующий текст) — до развилки, как в history. Пример в
`Example` дополнить строкой `aicost report --period=month --by-model`.
`--chart`/`--compare` у `report` **не добавлять** (см. §2).

### 4.7 `internal/cli/cli.go`

Изменений в wiring нет (флаги локальны командам). Допустимо обновить
doc-comment пакета/`Example` root-команды одной строкой про
`history --chart/--compare`.

## 5. Тесты

Стиль — существующий: table-driven; golden в `testdata/` с флагом `-update` и
побайтовым сравнением (образец — `TestTable_Golden`,
`internal/report/report_test.go:51`); CLI — через `testApp`/`run` с
`storage.NewFake()` и фиксированным `Now` (2025-08-15, `cli_test.go:41`).

### 5.1 `internal/report` (report_test.go + новые chart_test.go, compare_test.go)

Unit (table-driven):

1. `TestAggregateByModel` — группировка по `(provider, model)`; сортировка
   provider→model; пустой `Provider` игнорируется; пустой `Model` — отдельный
   бакет; суммирование токенов/коста по нескольким дням одной модели.
2. `TestAggregateByModel_Empty` — `nil` → пустой срез.
3. `TestAggregateByDay` — заполнение нулевых дней на всём окне (окно 5 дней,
   записи в 2 из них → ровно 5 `DayTotal` по порядку); суммирование через
   провайдеров и моделей одного дня; запись с `Day` вне окна игнорируется.
4. `TestAggregateByDay_EmptyWindowOrRecords` — `nil` записи → все дни по $0.00;
   окно нулевой длины → пустой срез.
5. `TestBarChart_Scaling` — максимум = ровно 40 блоков; ноль → 0 блоков;
   маленький ненулевой (0.01 при max 100) → ровно 1 блок; все нули → ни
   одного `█` и нет паники/деления на ноль.
6. `TestCompareTables_Delta` — кейсы: рост (`+$…, +75.0%`), падение
   (`-$…, -…%`), равные суммы (`+$0.00, +0.0%`), `prev` пуст/нулевой →
   `n/a`.

Golden (побайтово, `-update`):

7. `TestModelTable_Golden` → `testdata/report_model_table.golden` —
   фиксированные `ModelRow` включая пустую модель (проверяет `(unknown)`),
   TOTAL-футер.
8. `TestBarChart_Golden` → `testdata/history_chart.golden` — фиксированные
   `DayTotal` (7 дней: нулевой день, максимум, минимальный ненулевой — как в
   примере §4.2).
9. `TestCompareTables_Golden` → `testdata/history_compare.golden` —
   фиксированные `cur`/`prev` строки + метки окон, полный формат из §4.3.

Существующие `TestTable_Golden`/`report_table.golden` — не трогать, они должны
проходить без `-update`.

### 5.2 `internal/cli/period_test.go`

10. `TestPreviousWindow` (table-driven, `now = 2025-08-15 13:30 UTC` — как в
    `TestParsePeriod`):
    - `"today"` → `[2025-08-14, 2025-08-15)`;
    - `"7d"` (текущее `[2025-08-09, 2025-08-16)`) → `[2025-08-02, 2025-08-09)`;
    - `"month"` → `[2025-07-01, 2025-08-01)`;
    - `"month"` при `now = 2025-01-10` → `[2024-12-01, 2025-01-01)` (граница
      года);
    - `"garbage"` → ошибка.
11. `TestFormatWindow` — `[2025-08-01, 2025-08-16)` → `"2025-08-01..2025-08-15"`.

### 5.3 `internal/cli/cli_test.go`

Через фейки (`storage.NewFake()` наполняется `Save`-ом снапшота с записями
разных дней/моделей; провайдер-фейк с `err`, чтобы поймать сетевые вызовы):

12. `TestHistoryCommand_Chart` — `history --period 7d --chart`: вывод содержит
    и `TOTAL` (таблица), и заголовок `CHART`, и `█`; провайдер не вызван.
13. `TestHistoryCommand_ByModel` — `history --by-model`: вывод содержит имя
    модели (например `gpt-4o`) и колонку `MODEL`; дефолтный `history` без
    флага модель **не** содержит (негативная проверка обратной совместимости).
14. `TestHistoryCommand_Compare` — стор с данными в текущем и прошлом окне
    (относительно фиксированного `Now`): вывод содержит `CURRENT [`,
    `PREVIOUS [`, `TOTAL: $… vs $…` и корректную дельту.
15. `TestHistoryCommand_CompareEmptyPrevious` — данные только в текущем окне:
    команда успешна, в дельте `n/a`.
16. `TestHistoryCommand_CompareConflictingFlags` — `--compare --by-model` и
    `--compare --chart` → ошибка с текстом `cannot be combined`.
17. `TestHistoryCommand_NoDataWithFlags` — пустой стор + `--chart` (и/или
    `--compare`): печатается существующее no-data-сообщение, `█`/`CURRENT`
    в выводе нет.
18. `TestReportCommand_ByModel` — `report --by-model` с фейк-провайдером,
    отдающим две модели: обе в выводе; без флага — по-прежнему одна строка
    провайдера (существующий `TestReportCommand_FetchesSavesAndPrints`
    должен пройти без правок).

## 6. Документация (после зелёных тестов)

- **README.md** (он на русском) — в разделе использования: примеры
  `aicost history --period=month --chart`, `aicost history --compare`,
  `aicost report --by-model` с короткими пояснениями; поправить статусную
  врезку вверху («Что дальше…»), если она противоречит факту Этапа 6.
- **docs/TECHNICAL_PLAN.md §6** — добавить блок «Этап 6 — тренды и графики ✅»
  (ветка `stage-6/history-trends`, состав: `--chart`/`--by-model`/`--compare`,
  решения из §3 этого плана в одну-две строки); в §2 таблицу стека можно не
  трогать (рендер остался `text/tabwriter`).
- **docs/POST_MVP_PLAN.md §P2** — отметить реализованное (ASCII-график,
  разбивка по моделям, сравнение периодов — ✅ Этап 6), явно оставить TUI
  (bubbletea) как нереализованного кандидата.

## 7. Критерий готовности (проверить фактическим прогоном, вывод — в отчёт)

1. `go build ./...`, `go vet ./...`, `go test ./...` — зелёные;
   `gofmt -l .` — пусто. (`export PATH="$HOME/sdk/go/bin:$PATH"` если go не
   находится.)
2. Все новые форматы вывода покрыты golden-тестами
   (`report_model_table.golden`, `history_chart.golden`,
   `history_compare.golden`), созданными через `-update` и проходящими без него.
3. Существующие тесты и golden (`report_table.golden`) проходят **без
   изменений** — дефолтный вывод `report`/`history` побайтово прежний.
4. `history` не делает сетевых вызовов ни с какими флагами (тесты 12–17
   используют провайдер-фейк с ошибкой «network should not be used»).
5. Новых записей в `go.mod` нет.
6. README / TECHNICAL_PLAN / POST_MVP_PLAN обновлены под факт (§6 плана).
7. Ручная проверка на живом бинаре не требуется в этой ветке (нет сетевых
   изменений), но smoke `go run ./cmd/aicost history --period=7d --chart` на
   пустой БД должен напечатать no-data-сообщение без паники.

## 8. Порядок работы

1. `internal/report`: `AggregateByModel` + `ModelTable` + тесты/golden.
2. `internal/report/chart.go` + тесты/golden.
3. `internal/report/compare.go` + тесты/golden.
4. `internal/cli/period.go`: `previousWindow` + `formatWindow` + тесты.
5. CLI-wiring: `report --by-model`; `history --by-model/--chart/--compare` +
   тесты `cli_test.go`.
6. Документация (§6), финальный прогон (§7).
