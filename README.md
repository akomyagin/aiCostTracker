# aiCostTracker

CLI-инструмент на Go (`aicost`), который собирает расходы и использование по
нескольким AI-провайдерам (Anthropic, OpenAI и далее) в один локальный дашборд
в терминале. Один взгляд вместо обхода 3–5 биллинг-консолей.

Соло pet-проект с приоритетом обучения Go. Чистый CLI: без сервера, без
телеметрии, ≈ $0/мес. История расходов хранится локально в SQLite для трендов
по времени.

> Статус: **Этап 0 (bootstrap)** — скелет собран, `go build ./...` зелёный.
> Реальная выборка usage начинается с Этапа 1. План — в [`docs/`](docs/).

## Идея

У каждого провайдера свой usage/billing-API, и обычно он требует **отдельного
admin/org-level ключа** (не того, которым дёргают модели). `aicost` прячет эти
различия за портом `ProviderUsageSource` — каждый провайдер это адаптер, — тянет
usage за период, нормализует в общую модель и печатает сводную таблицу.

## Установка и запуск

```bash
export PATH="$HOME/sdk/go/bin:$PATH"   # если go не в PATH

go build ./...
go run ./cmd/aicost --version
go run ./cmd/aicost                    # Этап 0: заглушка
# Этап 1+: go run ./cmd/aicost report --period=this-month
```

## Конфигурация (с Этапа 1)

Файл `~/.config/aicost/config.yaml` (точный путь — через `os.UserConfigDir()`).
Admin-ключи лучше задавать через окружение, чтобы не писать на диск:

```yaml
providers:
  anthropic:
    enabled: true
    admin_key: ""   # или env AICOST_ANTHROPIC_ADMIN_KEY (admin/org-ключ, не ключ модели!)
  openai:
    enabled: true
    admin_key: ""   # или env AICOST_OPENAI_ADMIN_KEY
```

## Дорожная карта

- **Фаза 1 (MVP):** Anthropic + OpenAI, команда `report`, локальные снапшоты в SQLite.
- **Фаза 2:** больше провайдеров, тренды/графики в терминале, алерты по порогу,
  `--format=json`. См. [`docs/POST_MVP_PLAN.md`](docs/POST_MVP_PLAN.md).

## Документация

- [`docs/PLAN.md`](docs/PLAN.md) — видение и план верхнего уровня.
- [`docs/TECHNICAL_PLAN.md`](docs/TECHNICAL_PLAN.md) — стек, архитектура, порт-адаптер, Этапы.
- [`docs/POST_MVP_PLAN.md`](docs/POST_MVP_PLAN.md) — Фаза 2 и далее.

## Лицензия

MIT — см. [`LICENSE`](LICENSE).
