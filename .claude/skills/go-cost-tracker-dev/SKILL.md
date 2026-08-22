---
name: go-cost-tracker-dev
description: Конвенции проекта aiCostTracker — Go-CLI (aicost), агрегатор usage/cost по AI-провайдерам. Структура cmd/+internal/, порт ProviderUsageSource и адаптеры провайдеров, ручной net/http с retry+backoff (паттерн из gitl), SQLite для локальной истории, table-driven+golden тесты. Использовать при реализации любого этапа кодирования aiCostTracker.
---

# SKILL: go-cost-tracker-dev — конвенции проекта `aiCostTracker`

Конкретные конвенции **именно этого проекта** для написания Go-кода. Не общий
гайд «как писать Go», а специфика `aiCostTracker` (бинарник `aicost`). Применяй
при реализации любого этапа. Опорные документы:
[`../../../docs/TECHNICAL_PLAN.md`](../../../docs/TECHNICAL_PLAN.md),
[`../../../docs/PLAN.md`](../../../docs/PLAN.md).

---

## 1. Структура: `cmd/` + `internal/`, почему нет `pkg/`

- `cmd/aicost/main.go` — **тонкий**: build-метаданные (`version/commit/date` через
  ldflags), `signal.NotifyContext` для отмены по Ctrl-C, вызов `cli.Execute`.
  Никакой бизнес-логики в `main`.
- Всё ядро — в `internal/`, чтобы компилятор **запрещал** внешний импорт: это
  приложение, а не библиотека. Пакеты: `cli`, `config`, `provider`, `storage`,
  `report`.
- **`pkg/` не заводить.** Публичный API — только при реальном спросе.
- По файлу на провайдер в `internal/provider/` (`anthropic.go`, `openai.go`, …);
  сам порт — в `provider.go`. По файлу на cobra-команду в `internal/cli/`.
- Каждая заглушка `internal/*` из Этапа 0 помечена «реализация — Этап N»; при
  реализации **заменять содержимое**, а не плодить параллельные файлы.

## 2. Порт `ProviderUsageSource` — центральный паттерн

Главное архитектурное решение: разнородные usage-API провайдеров спрятаны за
единым портом (ports & adapters, как в `gitl`/KnowledgeVault).

```go
type ProviderUsageSource interface {
    ID() string                                        // "anthropic", "openai", …
    Fetch(ctx context.Context, w Window) (Snapshot, error)
}
```

- `ID()` — стабильный **lowercase** идентификатор; он же ключ в конфиге
  (`providers.<id>`), в хранилище и селектор в CLI. Константа на всю жизнь адаптера.
- `Fetch` — тянет usage за окно, нормализует **внутри адаптера** в общий
  `UsageRecord{Provider, Day, Model, InputTokens, OutputTokens, CostMicros}` и
  отдаёт `Snapshot`. `CostMicros` — целые микро-доллары (`int64`, `1 USD = 1e6`):
  сумма провайдера парсится во `float64` и **сразу** конвертируется через
  `provider.DollarsToMicros`, дальше только целочисленная арифметика (Этап 9,
  устранение дрейфа float). Различия провайдеров (пути, авторизация, форма
  ответа, пагинация) наружу **не протекают**.
- Добавить провайдера = добавить один файл-адаптер + строку в фабрику. CLI,
  storage, report не трогаются. Это проверяемый инвариант качества.
- **`var _ ProviderUsageSource = (*Anthropic)(nil)`** в каждом адаптере — чтобы
  несоответствие интерфейсу ловилось компилятором.

### admin/org-ключ ≠ ключ модели

Usage/cost-эндпоинты требуют **отдельного admin/org-level ключа**, не того,
которым дёргают модели. В конфиге поле называется `admin_key` (не `api_key`),
в UX/README это явно объясняется. Не путать эти два вида ключей в коде и текстах.

### Пока API не подтверждён — заглушка, а не выдумка

Точные эндпоинты Anthropic/OpenAI на Этапе 0 помечены `[ASSUMPTION]`/
`[TODO уточнить в Этапе 1]` (TECHNICAL_PLAN §4). **Не хардкодить выдуманные URL
как факт.** Заглушка `Fetch` возвращает ошибку «not implemented (Этап N)» —
сборка остаётся зелёной. Реальные эндпоинты вписываются только после проверки
по живой документации в Этапе 1.

## 3. HTTP-клиент к provider API: retry + backoff (ручной `net/http`, без SDK)

Переиспользуем паттерн из `gitl` (`internal/llm/client.go`) — осознанно без SDK
провайдеров (тренировка + однородность зависимостей).

- Таймаут на запрос + общий `context`; отмена (`signal.NotifyContext`) протянута
  насквозь через `Fetch(ctx, …)`.
- **Типизированная классификация ошибок**: свой `StatusError{StatusCode, Retryable}`,
  различать ретраебельные (429, 5xx, сетевые) vs фатальные (400, 401, 403).
  Использовать `errors.As`/типизированные ошибки, **не строковое сравнение**.
- **Retry с экспоненциальным backoff + jitter** (не фиксированная пауза — иначе
  thundering herd), ограничение по `max_retries` из конфига.
- **Секреты (`admin_key`) НИКОГДА не логировать**, даже в `--verbose`. Тест
  обязан проверять отсутствие ключа в stdout/stderr при ошибках.
- Каждый провайдер — свой auth-заголовок/endpoint внутри адаптера; выбор
  провайдера — по явному `ID()`, не угадыванием по URL.

## 4. Хранилище: SQLite для локальной истории

- `internal/storage` за портом `Store` (SQLite + in-memory fake для тестов).
- Драйвер — по умолчанию `modernc.org/sqlite` (чистый Go, без CGO → простая
  кросс-компиляция); финализируется в начале Этапа 2 (TECHNICAL_PLAN §2.1).
- `Save` — **идемпотентный upsert** по `(provider, day, model)`: повторный fetch
  того же периода не удваивает историю. Тест на это обязателен.
- Путь БД — через `os.UserConfigDir()`, **никогда** хардкод `~/.config`. Файл БД
  git-ignored (личные данные о расходах).
- SQLite нужен ради **трендов по времени**, а не только текущего среза — не
  сваливаться в «показываем только последний ответ API».

## 5. Тесты: table-driven + golden + httptest

- Всё, кроме самого сетевого вызова, тестируется; сеть за портом → в тестах
  `httptest.Server` или fake-адаптер.
- **Table-driven** — нормализация ответов провайдеров в `UsageRecord`, агрегация
  (`report.Aggregate`), границы окна (`Window`).
- **Golden** в `testdata/` — рендер таблицы (`report.Table`) и JSON (Фаза 2);
  обновление флагом `-update`, сравнение байт-в-байт.
- **`httptest.Server`** — retry-логика (сценарий 429→200) и фатальные ошибки.
- Обязательный секрет-тест: `admin_key` отсутствует в любом выводе/логе при ошибке.
- Конкурентность (параллельный fetch провайдеров, Фаза 2) — прогонять с `-race`.

## 6. Общие правила

- Go **1.23+**: `slices`, `maps`, `log/slog`, `errors.Join`, `errors.As`.
- Кросс-платформенность: `os.UserConfigDir()`/`os.UserCacheDir()`/`filepath.Join`.
- Логи — `log/slog`; `--verbose` поднимает уровень до debug.
- **Интерфейс вводится на второй реализации, не на первой** — исключения уже
  оправданы (`ProviderUsageSource` — 2 провайдера сразу; `Store` — SQLite + fake).
- Перед коммитом кода: `go build ./...`, `go vet ./...`, `go test ./...` зелёные;
  желателен `gofmt`/`go test -race`.
- Docker Compose в проекте **нет** и не планируется (чистый CLI; обоснование —
  TECHNICAL_PLAN §7).
