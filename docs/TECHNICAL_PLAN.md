# TECHNICAL_PLAN — детальный технический план `aiCostTracker`

> Технический спутник [`PLAN.md`](./PLAN.md). Терминология: «Этап N», где
> Этап 0 = bootstrap. Post-MVP — в [`POST_MVP_PLAN.md`](./POST_MVP_PLAN.md).
> Конвенции написания кода — в
> [`../.claude/skills/go-cost-tracker-dev/SKILL.md`](../.claude/skills/go-cost-tracker-dev/SKILL.md).

---

## 1. Вводные и ограничения

- Соло pet-проект, приоритет — обучение Go и написание кода AI-ассистентом.
- Расходы на инфраструктуру ≈ **$0/мес**: чистый CLI, без сервера. Обращения к
  provider usage API — только по явной команде, не по расписанию/крону.
- Единственное локальное состояние — SQLite-файл истории (git-ignored).
- Секреты (admin-ключи) — только локально: конфиг/окружение, никогда в
  git/логах/выводе.

## 2. Технический стек

| Слой | Выбор | Обоснование |
|---|---|---|
| Язык | **Go 1.23+** | Приоритет обучения; упор на stdlib |
| CLI-фреймворк | `spf13/cobra` + `spf13/viper` (с Этапа 1) | Тот же выбор, что в `gitl`; в Этапе 0 — только `flag` из stdlib, чтобы `go build` был зелёным без внешних модулей |
| HTTP к provider API | ручной `net/http`, **без SDK провайдеров** | Осознанно: тренировка retry/backoff/обработки ошибок; SDK каждого провайдера тянул бы разнородные зависимости |
| Локальное хранилище | **SQLite** | Тренды по времени; драйвер — см. §2.1 |
| Рендер таблицы | `text/tabwriter` (stdlib) на Фазе 1 | Без зависимостей; TUI/графики — Фаза 2 |
| Тесты | стандартный `testing`, table-driven + golden + `httptest` | Как в `gitl` |
| Логи | `log/slog` (stdlib) | `--verbose` поднимает уровень до debug |

### 2.1 Драйвер SQLite — решение отложено до Этапа 2

Два кандидата, выбор фиксируется в начале Этапа 2:

- `modernc.org/sqlite` — **чистый Go, без CGO**. Проще кросс-компиляция,
  предпочтителен для pet-CLI, который потом релизится под несколько платформ.
- `mattn/go-sqlite3` — CGO-биндинг, быстрее, но усложняет кросс-компиляцию.

**Решено (Этап 2, подтверждено в Этапе 4):** взят `modernc.org/sqlite`
(чистый Go). Кросс-компиляция проверена в Этапе 4 с `CGO_ENABLED=0` для
linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 — все
собираются без CGO-боли. Причин против не всплыло.

## 3. Архитектура и структура проекта

Ports & adapters (гексагональная), как в `gitl` / KnowledgeVault. Ядро знает
про **порт** `ProviderUsageSource`, а не про конкретные провайдеры.

```
cmd/aicost/main.go        # тонкий: build-метаданные, signal.NotifyContext, cli.Execute
internal/
├── cli/         # сборка команд (report, history, version); Этап 0 — flag, Этап 1 — cobra
├── config/      # загрузка конфига os.UserConfigDir()/aicost/config.yaml + env; admin-ключи
├── provider/    # ПОРТ ProviderUsageSource + адаптеры anthropic.go, openai.go, openrouter.go (…google — Фаза 2)
├── storage/     # ПОРТ Store (SQLite + in-memory fake для тестов); снапшоты истории
└── report/      # агрегация UsageRecord → Row, рендер таблицы (golden-тесты)
```

- **`pkg/` намеренно нет** — всё в `internal/`, чтобы компилятор запрещал внешний
  импорт незрелого API. Публичный API — только при спросе.
- Тонкий `main.go`: никакой бизнес-логики, только wiring.
- По файлу на провайдер в `internal/provider/`; общий порт — в `provider.go`.

### 3.1 Порт `ProviderUsageSource`

```go
type ProviderUsageSource interface {
    ID() string                                        // "anthropic", "openai", …
    Fetch(ctx context.Context, w Window) (Snapshot, error)
}
```

Контракт (реализован в `internal/provider/provider.go`, заглушки адаптеров — там же):

- `ID()` — стабильный lowercase-идентификатор; ключ хранилища и селектор в CLI.
- `Fetch` — тянет usage за окно `Window` (полуинтервал `[Start, End)`, границы
  выровнены по UTC-дню); **обязан** уважать отмену `ctx`, ретраить ретраебельные
  ошибки (429/5xx/сеть) с экспоненциальным backoff+jitter, падать сразу на
  фатальных (400/401/403), и **никогда** не логировать ключ.
- Наружу отдаёт нормализованный `Snapshot{Provider, Window, []UsageRecord, FetchedAt}`,
  где `UsageRecord` — общая модель `(provider, day, model, in/out tokens, costUSD)`.
  Различия провайдеров нормализуются **внутри** адаптера и наружу не протекают.

Правило «интерфейс появляется на второй реализации» здесь выполнено буквально:
два провайдера (Anthropic + OpenAI) в scope с Этапа 1, поэтому порт оправдан сразу.

### 3.2 Поток данных (команда `report`)

```
config.Load → для каждого enabled-провайдера: provider.Fetch(ctx, window)
            → storage.Save(snapshot)                (идемпотентный upsert)
            → report.Aggregate(records) → report.Table(stdout)
```

## 4. Usage/cost API провайдеров — ✅ верифицировано в Этапе 1

**Факт (проверено 2026-07-08).** Эндпоинты, авторизация, пагинация и форма
ответа подтверждены по живой документации и зафиксированы в
[`API_NOTES.md`](./API_NOTES.md). Пометки `[ASSUMPTION]`/`[TODO]` сняты.

| Провайдер | Cost-эндпоинт (USD) | Usage-эндпоинт (токены) | Ключ / авторизация |
|---|---|---|---|
| **Anthropic** | `GET /v1/organizations/cost_report` | `GET /v1/organizations/usage_report/messages` | Admin key (`sk-ant-admin…`), `x-api-key` + `anthropic-version: 2023-06-01`; окно — RFC 3339 |
| **OpenAI** | `GET /v1/organization/costs` | `GET /v1/organization/usage/completions` | Admin key (`sk-admin-…`), `Authorization: Bearer`; окно — Unix-секунды |
| **OpenRouter** (Этап 5, проверено 2026-08-22) | `POST /api/v1/analytics/query` (единый эндпоинт: cost **и** токены) | тот же запрос | Management key, `Authorization: Bearer`; окно — RFC 3339, грануляция `day`, без курсорной пагинации (`limit` + `truncated`) |

Оба usage/cost-эндпоинта требуют **admin/org-level ключа**, а не ключа для вызова
моделей — это заложено в схему конфига (§5, поле `admin_key`) и в UX (в
`--help`/README явно объясняем, где взять admin-ключ).

Ответы по `[TODO §4]` (детали и форма ответа — в [`API_NOTES.md`](./API_NOTES.md)):
1. Пути эндпоинтов — см. таблицу выше.
2. Авторизация — Anthropic `x-api-key`+`anthropic-version`, OpenAI `Bearer`.
3. Гранулярность — день (`bucket_width=1d`) по модели; курсорная пагинация
   `has_more`/`next_page`.
4. **Готовую стоимость в USD отдают оба** (Anthropic `cost_report.amount` строкой,
   OpenAI `costs.amount.value` числом) — клиентская таблица цен для MVP не нужна.

Каждый адаптер (`anthropic.go`, `openai.go`) тянет **стоимость из cost-эндпоинта**
и **токены из usage-эндпоинта**, мёржит по `(day, model)` в `[]UsageRecord`.

## 5. Схема конфигурации

Файл — `os.UserConfigDir()/aicost/config.yaml` (никогда не хардкодить `~/.config`).
Admin-ключи можно задать и через окружение, чтобы не писать их на диск; env
переопределяет файл. Секреты не логируются.

```yaml
# ~/.config/aicost/config.yaml
http_timeout: 30s
max_retries: 4
db_path: ""              # пусто = os.UserConfigDir()/aicost/history.db

providers:
  anthropic:
    enabled: true
    admin_key: ""        # admin/org-ключ; лучше через env AICOST_ANTHROPIC_ADMIN_KEY
    base_url: ""         # пусто = дефолт провайдера; override для прокси/тестов
  openai:
    enabled: true
    admin_key: ""        # через env AICOST_OPENAI_ADMIN_KEY
    base_url: ""
```

Соответствует типам в `internal/config/config.go`. Ключ `providers.<id>` совпадает
с `ProviderUsageSource.ID()`.

## 6. Разбивка по Этапам

### Этап 0 — Bootstrap ✅ *(готово)*
- `go mod init github.com/akomyagin/aiCostTracker`.
- Скелет `cmd/aicost/main.go` (тонкий) + заглушки `internal/{provider,storage,config,report,cli}`.
- Порт `ProviderUsageSource` объявлен; адаптеры-заглушки Anthropic/OpenAI.
- `go build ./...` и `go vet ./...` — **зелёные**; `aicost --version` запускается.

### Этап 1 — Порт + адаптеры Anthropic и OpenAI + конфиг + retry/backoff ✅ *(готово)*
- **Сначала** верифицировать usage-API обоих провайдеров по живой документации,
  снять пометки `[ASSUMPTION]`/`[TODO]` из §4, зафиксировать эндпоинты в коде.
- `internal/config`: загрузка YAML + env-override admin-ключей; валидация.
- `internal/provider`: реализовать `Fetch` для Anthropic и OpenAI на ручном
  `net/http` с retry (экспоненциальный backoff + jitter, типизированная
  классификация ошибок), нормализацией в `UsageRecord`. Ключ не логируется.
- Тесты: `httptest.Server` для сценариев 429→200 и фатальных ошибок; проверка,
  что ключа нет в stdout/stderr; table-driven на нормализацию ответа.
- Перевод CLI на cobra+viper.

### Этап 2 — Хранилище SQLite ✅ *(готово)*
- Драйвер выбран — `modernc.org/sqlite` (§2.1, чистый Go, без CGO).
- `internal/storage`: схема таблицы снапшотов, `Save` c **идемпотентным upsert**
  по `(provider, day, model)`, `Query` по провайдеру/окну, `Close`.
- In-memory fake `Store` для тестов остального кода без диска.
- Тесты на идемпотентность (двойной `Save` не удваивает историю).

### Этап 3 — Команда `report` + агрегация + таблица ✅ *(готово)*
- `report.Aggregate`: свёртка per-day записей в одну строку **на провайдера**
  (разбивка по модели — Фаза 2, POST_MVP §P2; `UsageRecord.Model` уже пишется в
  историю, но в таблицу пока не выводится).
- `report.Table`: выравненная таблица через `text/tabwriter`.
- Команда `report --period=...`: fetch → save → aggregate → table.
- Golden-тесты рендера (`testdata/`, обновление флагом `-update`, сравнение байт-в-байт).

### Этап 4 — Хардненинг + UX + кросс-компиляция — **КОНЕЦ MVP (Фаза 1)** ✅ *(готово)*
- `--help`/UX: admin-ключи объяснены в `Long`/`Example` root- и `report`-команд
  (общий текст `adminKeyHelp` в `internal/cli/help.go`) — где взять, чем отличается
  от ключа модели, как задать (env / config). Плюс `adminKeyHintFor`: при 401/403
  или пустом ключе в ошибку `report` добавляется компактная подсказка про admin-ключ
  (текст — фиксированная константа, `err` не интерполируется → секрет не утечёт).
- README переписан под завершённый MVP: установка (`go install`/сборка), таблица
  «где взять admin-ключ», примеры `report`/`history`, пример `config.yaml`.
- **Кросс-компиляция подтверждена** (`CGO_ENABLED=0 go build ./...`): linux/amd64,
  linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 — все зелёные без CGO
  (драйвер `modernc.org/sqlite` — чистый Go; §2.1).
- Проход по безопасности: ключ не попадает в вывод ни при одном пути ошибки
  (конфиг/парсинг/сеть/БД). `redactSecrets` в httpclient чистит `Authorization`
  и `x-api-key` из `StatusError.Body`; ключи живут только в HTTP-заголовках, не в
  URL; сообщения об ошибках admin-ключа не содержат самого ключа. Покрыто тестами
  (`httpclient_test.go` — редакция отражённого ключа; `cli_test.go` — подсказка без
  утечки секрета).
- **Не делаем в MVP:** полноценный релизный пайплайн (goreleaser / CI-артефакты).
  Для проверки кросс-компиляции достаточно `go build` с `GOOS`/`GOARCH`; релизный
  пайплайн — кандидат Фазы 2 (POST_MVP_PLAN).

### Этапы 5+ — Фаза 2
- **Этап 5 — адаптер OpenRouter ✅** (ветка `stage-5/openrouter-provider`):
  третий провайдер за портом `ProviderUsageSource`, единый
  `POST /api/v1/analytics/query`, management key. Детали — `API_NOTES.md §3`.

Остальное — см. [`POST_MVP_PLAN.md`](./POST_MVP_PLAN.md).

## 7. Docker Compose — решение: НЕ заводим

`aiCostTracker` — чистый CLI-бинарник без сервиса, БД-сервера и внешних
контейнеризируемых зависимостей: SQLite — это файл в процессе, а provider API —
внешние облачные эндпоинты. Локальный сервер поднимать нечего, значит
`docker-compose.yml` не даёт ценности и не заводится.

Отличие от `gitl`, где compose всё же есть: там он держит dev-зависимость
`ollama` для теста локальной мультипровайдерности LLM. Здесь аналога нет — все
провайдеры облачные, а для тестов используется `httptest.Server` в самом Go, не
контейнер. Если в Фазе 2 понадобится воспроизводимый mock-сервер usage-API для
интеграционных прогонов, его проще поднять тем же `httptest`/лёгким Go-стабом,
чем контейнером.

**Итог:** Docker/Compose в проекте нет и не планируется. Пересмотреть только
если появится компонент, который реально надо контейнеризировать.

## 8. Тестирование (сводка; детали — SKILL.md §4)

- Всё, кроме самого сетевого вызова, тестируется; сеть — за портом, в тестах
  подменяется `httptest`/fake.
- Table-driven — нормализация ответов провайдеров, агрегация, границы окна.
- Golden — рендер таблицы (и JSON в Фазе 2).
- `httptest.Server` — retry-логика (429→200) и классификация фатальных ошибок.
- Обязательный тест: admin-ключ отсутствует в любом выводе/логе при ошибке.
- Где есть конкурентность (параллельный fetch нескольких провайдеров, Фаза 2) —
  прогонять с `-race`.
