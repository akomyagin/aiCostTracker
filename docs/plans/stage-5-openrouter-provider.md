# План: Этап 5 — провайдер OpenRouter (`stage-5/openrouter-provider`)

> План для исполняющего агента. Ветка `stage-5/openrouter-provider` уже создана
> и выбрана — **не переключаться, git-коммиты не делать** (commit/push/PR — вне
> зоны исполнителя). Язык: код/идентификаторы/комментарии — английский;
> документация — русский.

> **Исторический документ (заморожен).** Этап 5 реализован; план ниже фиксирует
> состояние **на момент написания**, включая карту стоимости
> `map[dayModel]float64` — на Этапе 9 (`docs/POST_MVP_PLAN.md §P5`) тип сменился
> на `int64` (микро-USD). Актуальное описание — `docs/API_NOTES.md §3.5`.

## 1. Цель

Добавить провайдера **OpenRouter** как третий адаптер за существующим портом
`ProviderUsageSource` (`internal/provider/provider.go`), по чек-листу
`docs/POST_MVP_PLAN.md §P1` (7 точек касания). CLI, storage и report **не
меняются** — это проверяемый инвариант порта; если по ходу кажется, что их надо
трогать, — остановиться и перепроверить дизайн, а не менять слои.

## 2. Факты об API OpenRouter (исследование выполнено, не перепроверять)

Верифицировано по живой документации
(`https://openrouter.ai/docs/cookbook/administration/analytics-cost-control`),
2026-08-22:

- **Эндпоинт данных**: `POST https://openrouter.ai/api/v1/analytics/query`.
  Существует также `GET /api/v1/analytics/meta` (схема метрик/измерений) — в
  адаптере в рантайме **не используется**, достаточно одного query-запроса.
- **Авторизация**: `Authorization: Bearer <management key>`. Management key
  создаётся отдельно (openrouter.ai → Settings → Management Keys) и **не равен**
  обычному inference-ключу; inference-ключ на этом эндпоинте получает **403**.
  Это прямой аналог admin-ключа Anthropic/OpenAI → существующее поле конфига
  `admin_key` подходит без изменений схемы.
- **Request body** (JSON):

  ```json
  {
    "metrics": ["..."],
    "dimensions": ["model"],
    "granularity": "day",
    "time_range": { "start": "ISO_8601", "end": "ISO_8601" },
    "limit": 1000
  }
  ```

  Ограничение: максимум **2 dimensions** (иначе 400). Нам достаточно одного —
  `"model"` (день даёт `granularity: "day"`). Поле `filters` для MVP не нужно —
  окно задаётся через `time_range`.
- **Response**:

  ```json
  {
    "data": {
      "data": [ /* rows */ ],
      "metadata": { "query_time_ms": 0, "row_count": 0, "truncated": false }
    }
  }
  ```

  Строки содержат группировочные поля (`model`, дата-поле по грануляции) и
  метрики. Стоимость — **уже в USD** (не центы, не кредиты). Классической
  пагинации (курсор/`next_page`) **нет**: только `limit` +
  `metadata.truncated`.
- **Открытые вопросы, помеченные в исследовании `[TODO уточнить в реализации]`**
  (решить по месту, см. §4.2 ниже, не блокироваться):
  1. Точное имя дата-поля в строке ответа (`date` vs `date__day` vs
     `created_at__day`).
  2. Есть ли отдельные метрики input/output токенов, либо только стоимость.

## 3. Точки касания (полный перечень, POST_MVP §P1)

| # | Файл | Что сделать |
|---|---|---|
| 1 | `internal/provider/openrouter.go` | **новый** — адаптер (§4) |
| 2 | `internal/provider/openrouter_test.go` | **новый** — httptest-тесты (§5) |
| 3 | `internal/config/config.go` | `"openrouter"` в `knownProviders` (~строка 54) |
| 4 | `internal/cli/cli.go` | `case "openrouter"` в `newProvider` (~строка 67) |
| 5 | `internal/cli/help.go` | строки OpenRouter в `adminKeyHelp` и `adminKeyHint` |
| 6 | `docs/API_NOTES.md` | новый раздел §3 OpenRouter (старый §3 «Итог» → §4) |
| 7 | `README.md` | строка в таблице admin-ключей, env-пример, блок в config.yaml, дорожная карта |
| + | `docs/TECHNICAL_PLAN.md` | §3 (дерево каталогов) и §4 (таблица провайдеров) |
| + | `docs/POST_MVP_PLAN.md` | §P1 — OpenRouter отметить реализованным |
| + | `internal/config/config_test.go` | env-override/EnabledProviders для openrouter |

## 4. `internal/provider/openrouter.go` — адаптер

Образец структуры и стиля — `internal/provider/openai.go` (тот же Bearer-auth,
JSON REST). Отличия: один POST-эндпоинт вместо двух GET, нет курсорной
пагинации.

### 4.1 Скелет

```go
// openrouterDefaultBaseURL is the production OpenRouter host. Overridable via
// Options.BaseURL for a proxy or an httptest server in tests.
const openrouterDefaultBaseURL = "https://openrouter.ai"

// OpenRouter is the ProviderUsageSource adapter for OpenRouter's Analytics API.
// Cost (USD) and token counts come from a single POST /api/v1/analytics/query
// grouped by model with day granularity. See docs/API_NOTES.md §3.
//
// The management key (Settings -> Management Keys) is distinct from a normal
// inference key (which gets 403 on this endpoint) and is never logged.
type OpenRouter struct {
    adminKey string // management key; never logged
    baseURL  string
    client   *retryClient
}

var _ ProviderUsageSource = (*OpenRouter)(nil)

func NewOpenRouter(opts Options) *OpenRouter { /* как NewOpenAI: TrimRight(orDefault(...)), newRetryClient(opts.httpDoer(), opts.MaxRetries) */ }

func (o *OpenRouter) ID() string { return "openrouter" }

func (o *OpenRouter) Fetch(ctx context.Context, w Window) (Snapshot, error)
```

Требования к `Fetch` (зеркально `OpenAI.Fetch`):

- Пустой ключ → `fmt.Errorf("provider openrouter: admin_key is empty (set AICOST_OPENROUTER_ADMIN_KEY or config)")`
  **до** любого HTTP-вызова.
- Ошибки оборачивать с префиксом `provider openrouter: analytics query: %w`.
- `Snapshot{Provider: o.ID(), Window: w, Records: records, FetchedAt: time.Now().UTC()}`.
- Записи строить через общий `mergeCostsAndTokens(o.ID(), costs, tokens)`
  (`internal/provider/adapter.go`) — он даёт детерминированную сортировку по
  `(day, model)`; из строк ответа наполнить обе мапы
  `map[dayModel]float64` / `map[dayModel]tokenCounts` и смёржить, даже если
  токены окажутся нулями.

### 4.2 Запрос и разбор ответа

**HTTP.** Использовать общий `retryClient.doJSON` (`internal/provider/httpclient.go`)
— **не изобретать новый клиент**. Тело запроса маршалить в `[]byte` один раз до
цикла, а в `buildReq`-замыкании на каждую попытку строить свежий
`http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))`
— иначе body не переживёт retry. Заголовки:
`Authorization: Bearer <adminKey>`, `Content-Type: application/json`,
`Accept: application/json`. Редакция ключа в диагностике уже покрыта
`redactSecrets` (он обрабатывает `Authorization`) — ничего добавлять не нужно.

**Тело запроса** (типизированной структурой, не map):

```go
type openrouterQueryRequest struct {
    Metrics     []string             `json:"metrics"`
    Dimensions  []string             `json:"dimensions"` // max 2 per API; we send ["model"]
    Granularity string               `json:"granularity"` // "day"
    TimeRange   openrouterTimeRange  `json:"time_range"`
    Limit       int                  `json:"limit"` // openrouterQueryLimit = 1000
}
type openrouterTimeRange struct {
    Start string `json:"start"` // w.Start.UTC().Format(time.RFC3339)
    End   string `json:"end"`   // w.End.UTC().Format(time.RFC3339)
}
```

Метрики — `[ASSUMPTION]`, зафиксировать комментарием в коде и в API_NOTES §3:
запрашиваем `"total_usage"` (суммарная стоимость в USD) и, в предположении их
существования, `"prompt_tokens"`, `"completion_tokens"`. Если живой API ответит
400 на неизвестную метрику — сузить до одной cost-метрики и оставить токены
нулевыми (известное ограничение адаптера, см. §7 «вне критерия готовности»);
httptest-фикстуры строятся по текущему предположению.

**Ответ**:

```go
type openrouterQueryResponse struct {
    Data struct {
        Data     []openrouterRow `json:"data"`
        Metadata struct {
            RowCount  int  `json:"row_count"`
            Truncated bool `json:"truncated"`
        } `json:"metadata"`
    } `json:"data"`
}

// openrouterRow tolerates the [ASSUMPTION] about the exact date-field name by
// declaring all documented candidates; day() picks the first non-empty one.
type openrouterRow struct {
    Date        string  `json:"date"`
    DateDay     string  `json:"date__day"`
    CreatedDay  string  `json:"created_at__day"`
    Model       string  `json:"model"`
    TotalUsage  float64 `json:"total_usage"`      // USD
    PromptToks  int64   `json:"prompt_tokens"`     // [ASSUMPTION] may be absent -> 0
    CompleteToks int64  `json:"completion_tokens"` // [ASSUMPTION] may be absent -> 0
}
```

Открытый вопрос №1 (имя дата-поля) решается хелпером: метод/функция
`func (r openrouterRow) day() (time.Time, error)` берёт первое непустое из
`Date`/`DateDay`/`CreatedDay` и парсит через существующий `parseUTCDay`
(`adapter.go`); если строка не RFC3339, а голая дата `YYYY-MM-DD` — сначала
попробовать `time.Parse("2006-01-02", s)`, затем RFC3339 (или наоборот; выбрать
одну последовательность и покрыть тестом). Все непустые кандидаты и порядок —
пометить `[ASSUMPTION]` в комментарии. Пустое дата-поле во всех кандидатах →
ошибка декодирования (не молчаливый пропуск строки).

**`truncated`.** Курсора нет, дотянуть «хвост» нечем, поэтому молча вернуть
неполные данные нельзя (отчёт занизит расходы). При
`metadata.truncated == true` вернуть явную ошибку вида
`fmt.Errorf("openrouter analytics response truncated at %d rows (limit %d): narrow the report period", rowCount, openrouterQueryLimit)`.
Это аналог `errTooManyPages` для беспагинационного API; сам
`maxPaginationPages` здесь не нужен (одиночный запрос, цикла нет) — зафиксировать
это в комментарии, чтобы ревью не искало «пропавший» cap.

### 4.3 Классификация ошибок

Ничего нового не писать: `retryClient` уже ретраит 429/5xx/сеть с экспоненциальным
backoff+jitter и падает сразу на 400/401/403 (`retryableStatus`,
`StatusError`). Специфику OpenRouter (403 = «это inference-ключ, а не management
key») отразить только в тексте `adminKeyHint`/`adminKeyHelp` (§6) и в тесте
(§5.4) — в самом адаптере ветвления по коду не нужно.

## 5. `internal/provider/openrouter_test.go` — тесты

Образец — `internal/provider/openai_test.go`: фикстуры-константы с реалистичными
телами (скопировать форму из §2 этого плана / API_NOTES §3), хелпер
`newOpenRouterTestAdapter(t, baseURL, maxRetries)` с
`AdminKey: "sk-or-mgmt-SECRET"`, `HTTPClient: &http.Client{Timeout: 5 * time.Second}`.
Для retry-тестов ставить `o.client.baseDelay = time.Millisecond`.

Обязательные кейсы:

1. **`TestOpenRouterFetch_NormalizesRows`** — httptest-сервер отдаёт фикстуру с
   2 днями × 2 моделями (4 строки: `date`, `model`, `total_usage`,
   `prompt_tokens`, `completion_tokens`). Проверить: `len(Records) == 4`,
   сортировка по `(Day, Model)`, `Provider == "openrouter"`, `Day` — UTC-полночь,
   значения токенов и `CostUSD` перенесены верно. Внутри handler'а дополнительно
   проверить сам запрос: метод `POST`, путь `/api/v1/analytics/query`, заголовок
   `Authorization == "Bearer sk-or-mgmt-SECRET"`, тело декодируется и содержит
   `granularity == "day"`, `dimensions == ["model"]` (длина ≤ 2),
   `time_range.start/end` == RFC3339-границы окна, `limit == 1000`.
2. **`TestOpenRouterFetch_RetriesOn429`** — первый запрос → 429, второй → 200 с
   фикстурой; `maxRetries: 3`. Проверить успех и ровно 2 обращения (счётчик в
   handler). Это подтверждает переиспользование общего retry-клиента.
3. **`TestOpenRouterFetch_FatalForbiddenNoRetry`** — сервер всегда отвечает 403
   (кейс «подсунули inference-ключ вместо management key» — специфика
   OpenRouter). `maxRetries: 3`, но обращение должно быть ровно **одно** (fatal
   не ретраится), ошибка не nil и содержит `403` (проверять через
   `errors.As(&StatusError{})` или подстроку статуса — как удобнее, но не
   «строковое сравнение вместо типизации» для ветвления).
4. **`TestOpenRouterFetch_KeyNotLeaked`** — сервер отвечает 400, причём тело
   ответа **эхом содержит ключ** (`sk-or-mgmt-SECRET`) — жёстче, чем в
   openai_test, задействует `redactSecrets`. Убедиться, что `err.Error()` не
   содержит ключ.
5. **`TestOpenRouterFetch_TruncatedIsError`** — фикстура с
   `"metadata": {"row_count": 1000, "truncated": true}` → ошибка, в тексте есть
   `truncated`; ключ в ошибке отсутствует.
6. **`TestOpenRouterFetch_EmptyKey`** — `AdminKey: ""`, httptest-сервер со
   счётчиком: ошибка упоминает `AICOST_OPENROUTER_ADMIN_KEY`, обращений к
   серверу — 0.
7. **`TestOpenRouter_ID`** — `(&OpenRouter{}).ID() == "openrouter"`.
8. **`TestOpenRouterRowDay`** — table-driven на хелпер выбора дата-поля: строки
   с `date` / `date__day` / `created_at__day` (RFC3339 и `YYYY-MM-DD`), все
   пустые → ошибка.

Дополнительно в существующих файлах:

- `internal/config/config_test.go` — кейс: env `AICOST_OPENROUTER_ADMIN_KEY`
  включает провайдера (`Enabled == true`, ключ подхвачен,
  `EnabledProviders()` содержит `openrouter`) — по образцу существующего
  env-override-теста для openai (~строка 55).
- В `internal/cli/` новых тестов не требуется (фабрика `newProvider` для
  anthropic/openai прямых тестов не имеет — сохранять симметрию, не добавлять).

## 6. Правки существующих файлов (точечные)

1. **`internal/config/config.go`** (~строка 54):
   `var knownProviders = []string{"anthropic", "openai", "openrouter"}` —
   env-override `AICOST_OPENROUTER_ADMIN_KEY` и валидация подхватятся
   автоматически, больше в пакете ничего не менять.
2. **`internal/cli/cli.go`** — в switch `newProvider` (~строка 67) добавить:
   `case "openrouter": return provider.NewOpenRouter(opts), nil`.
3. **`internal/cli/help.go`**:
   - в `adminKeyHelp` — строка по образцу соседних:
     `OpenRouter: openrouter.ai -> Settings -> Management Keys` с пометкой, что
     нужен **management key**, а НЕ обычный inference-ключ (иначе 403); плюс
     строка `export AICOST_OPENROUTER_ADMIN_KEY=...` в блок примеров env;
   - в `adminKeyHint` — дописать
     `OpenRouter: openrouter.ai -> Settings -> Management Keys (management key, not an inference key)`
     и `AICOST_OPENROUTER_ADMIN_KEY` в перечень env-переменных.

## 7. Документация

1. **`docs/API_NOTES.md`** — новый раздел `## 3. OpenRouter — Analytics API`
   (существующий `## 3. Итог…` перенумеровать в `## 4`). Структура — как у
   §1/§2: источник и дата проверки (2026-08-22,
   `openrouter.ai/docs/cookbook/administration/analytics-cost-control`),
   подразделы: авторизация (management key, Bearer, 403 на inference-ключ),
   эндпоинты (`POST /api/v1/analytics/query`; `GET /api/v1/analytics/meta` —
   только для ручной сверки схемы), request body, форма ответа, «как адаптер
   строит Snapshot» (один запрос вместо cost+usage-пары; `limit`+`truncated`
   вместо курсора → truncated = ошибка). Явно перечислить `[ASSUMPTION]`:
   имя дата-поля (кандидаты и порядок выбора), имена токен-метрик
   `prompt_tokens`/`completion_tokens`, имя cost-метрики `total_usage`.
2. **`README.md`**:
   - таблица admin-ключей (~строка 53): строка
     `| **OpenRouter** | openrouter.ai → Settings → Management Keys | management key (не inference-ключ) |`;
   - env-пример (~строка 60): `export AICOST_OPENROUTER_ADMIN_KEY=...`;
   - пример `config.yaml` (~строка 96): блок `openrouter:` с
     `enabled/admin_key/base_url` по образцу `openai`;
   - дорожная карта (~строка 119): OpenRouter из «Фаза 2 (планы)» перенести в
     реализованное (напр. «Фаза 2 (в работе): OpenRouter ✅; далее Google
     Gemini, тренды/графики…»).
3. **`docs/TECHNICAL_PLAN.md`**:
   - §3, дерево каталогов (~строка 54): `адаптеры anthropic.go, openai.go,
     openrouter.go (…google — Фаза 2)`;
   - §4, таблица провайдеров (~строка 101): строка OpenRouter —
     `POST /api/v1/analytics/query` (единый эндпоинт: cost **и** токены),
     management key / `Authorization: Bearer`, окно — RFC 3339, грануляция day,
     без курсорной пагинации (`limit` + `truncated`); проверено 2026-08-22;
   - §6 (~строка 202): под «Этапы 5+ — Фаза 2» отметить
     «Этап 5 — адаптер OpenRouter ✅».
4. **`docs/POST_MVP_PLAN.md` §P1** — в списке кандидатов: OpenRouter отметить
   реализованным (Этап 5, ветка `stage-5/openrouter-provider`; снять
   `[ASSUMPTION]`-формулировку, сослаться на API_NOTES §3), **Google Gemini
   оставить кандидатом без изменений**.

## 8. Что НЕ трогать

- `internal/cli/report.go`, `history.go`, `period.go`, `version.go`,
  `internal/storage/*`, `internal/report/*`, `cmd/aicost/main.go` — инвариант
  порта: добавление провайдера их не меняет.
- `internal/provider/httpclient.go`, `adapter.go`, `provider.go`,
  `anthropic.go`, `openai.go` — переиспользовать как есть; общий код не
  рефакторить «заодно».
- Схему `ProviderConfig`/`Config` — `admin_key` покрывает management key без
  новых полей.
- git: не коммитить, не переключать ветку.

## 9. Критерий готовности

Все пункты обязательны:

1. `go build ./...`, `go vet ./...`, `go test ./...` — зелёные;
   `gofmt -l .` — пустой вывод. (`export PATH="$HOME/sdk/go/bin:$PATH"`, если
   `go` не находится.)
2. Тест-кейсы §5 реализованы и проходят, включая: нормализация ответа,
   retry 429→200 через общий httpclient, фатальный 403 без retry
   (OpenRouter-специфика «не тот тип ключа»), отсутствие admin-ключа в любом
   выводе ошибок (в т.ч. при эхе ключа в теле ответа), `truncated` → явная
   ошибка, пустой ключ → ошибка без сетевого вызова.
3. `var _ ProviderUsageSource = (*OpenRouter)(nil)` присутствует; `ID()` —
   строго `"openrouter"` (lowercase, совпадает с ключом конфига и env-именем).
4. CLI/storage/report не изменены (git diff этих каталогов пуст, кроме
   `internal/cli/cli.go` — один case — и `internal/cli/help.go` — текст).
5. Все 7 точек POST_MVP §P1 закрыты + обновлены TECHNICAL_PLAN §3/§4/§6,
   POST_MVP §P1, README.
6. Открытые вопросы API (имя дата-поля, токен-метрики) задокументированы как
   `[ASSUMPTION]` в коде и в API_NOTES §3 — не выданы за проверенный факт.

Вне критерия готовности (зафиксировать, не делать): живой прогон против
настоящего OpenRouter-аккаунта — выполняется пользователем при dogfooding; если
реальный ответ разойдётся с `[ASSUMPTION]` (дата-поле/метрики), правка сведётся
к json-тегам `openrouterRow`/списку `Metrics` и фикстурам.
