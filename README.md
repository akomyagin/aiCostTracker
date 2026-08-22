# aiCostTracker

CLI-инструмент на Go (бинарник `aicost`), который собирает расходы и
использование по нескольким AI-провайдерам (Anthropic, OpenAI и далее) в один
локальный дашборд в терминале. Один взгляд вместо обхода 3–5 биллинг-консолей.

Соло pet-проект с приоритетом обучения Go. Чистый CLI: без сервера, без
телеметрии, ≈ $0/мес. Данные о расходах наружу не уходят. История хранится
локально в SQLite для трендов по времени.

> Статус: **MVP (Фаза 1) завершён**, идёт Фаза 2 — Anthropic + OpenAI +
> OpenRouter, команды `report` / `history` / `version`, локальные снапшоты в
> SQLite, кросс-компиляция без CGO. Тренды в терминале готовы: `history --chart`
> (ASCII-график по дням), `--by-model`, `--compare`. Что дальше — в
> [`docs/POST_MVP_PLAN.md`](docs/POST_MVP_PLAN.md).

## Идея

У каждого провайдера свой usage/billing-API, и обычно он требует **отдельного
admin/org-level ключа** (не того, которым дёргают модели). `aicost` прячет эти
различия за портом `ProviderUsageSource` — каждый провайдер это адаптер, — тянет
usage/cost за период, нормализует в общую модель и печатает сводную таблицу.
Стоимость в USD провайдеры отдают готовой, так что клиентская таблица цен не
нужна.

## Установка

```bash
# Через go install (нужен Go 1.23+):
go install github.com/akomyagin/aiCostTracker/cmd/aicost@latest

# Или собрать из исходников:
git clone https://github.com/akomyagin/aiCostTracker
cd aiCostTracker
go build -o aicost ./cmd/aicost
./aicost version
```

Драйвер SQLite — `modernc.org/sqlite` (чистый Go), поэтому бинарник собирается
**без CGO** под все целевые платформы:

```bash
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o aicost ./cmd/aicost
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o aicost.exe ./cmd/aicost
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o aicost ./cmd/aicost
```

## Admin-ключ (обязательно) — это НЕ ключ для вызова моделей

Usage/cost-эндпоинты — это **organization/admin API**. Обычный ключ, которым вы
вызываете модели (`sk-ant-api...` у Anthropic, проектный `sk-...` у OpenAI), к
ним доступа **не имеет**. Нужен отдельный **admin-ключ**, который выпускает
владелец организации:

| Провайдер | Где взять | Вид ключа |
|---|---|---|
| **Anthropic** | console.anthropic.com → Settings → Organization → Admin Keys | `sk-ant-admin...` |
| **OpenAI** | platform.openai.com → Settings → Organization → Admin Keys | `sk-admin-...` |
| **OpenRouter** | openrouter.ai → Settings → Management Keys | management key (не inference-ключ) |

Задать ключ можно двумя способами (env переопределяет файл):

```bash
# Рекомендуется — через окружение, ключ не пишется на диск:
export AICOST_ANTHROPIC_ADMIN_KEY=sk-ant-admin-...
export AICOST_OPENAI_ADMIN_KEY=sk-admin-...
export AICOST_OPENROUTER_ADMIN_KEY=...
```

Ключ **никогда** не логируется, не печатается и не попадает ни в одно сообщение
об ошибке.

## Использование

```bash
# Отчёт по всем включённым провайдерам за последние 7 дней:
aicost report --period=7d

# Другие периоды:
aicost report                 # последние 30 дней (по умолчанию)
aicost report --period=month  # с 1-го числа текущего месяца по сегодня
aicost report --period=today  # только сегодня

# Показать сохранённую историю БЕЗ обращения к сети:
aicost history --period=month

# Тренды и разбивки поверх истории (сеть по-прежнему не трогается):
aicost history --period=month --chart   # ASCII-график расхода по дням
aicost history --by-model                # разбивка таблицы по (провайдер, модель)
aicost history --compare                 # этот период vs предыдущий такого же типа

# Разбивка по моделям есть и в report (тянет свежие данные из сети):
aicost report --period=month --by-model

# Алерт по порогу расхода (порог задаётся в config.yaml, см. ниже):
aicost report --period=month --fail-on-alert   # exit≠0, если расход превысил порог
aicost history --period=month --fail-on-alert

# Машиночитаемый вывод (для своих дашбордов):
aicost report --format=json                     # JSON вместо таблицы
aicost history --period=month --format=json

# Полное объяснение admin-ключей есть прямо в справке:
aicost --help
aicost report --help
```

Значение `--period` — одно из: `Nd` (например `7d`, `30d`), `month` или `today`.

Флаги `history`: `--chart` дорисовывает горизонтальный бар-чарт дневного расхода,
`--by-model` разбивает таблицу по парам (провайдер, модель), `--compare`
показывает текущий период рядом с предыдущим и дельту по итоговому расходу.
`--compare` нельзя сочетать с `--by-model`/`--chart`. Для `month` «предыдущий
период» — это полный прошлый календарный месяц (текущий — месяц-до-сегодня),
точные диапазоны дат печатаются в заголовках таблиц. `report` из новых флагов
поддерживает только `--by-model` (`--chart`/`--compare` потребовали бы второго
платного запроса за прошлый период — для этого и существует история).

Флаг `--format` (`table` по умолчанию, либо `json`) есть у обеих команд. При
`--format=json` печатается версионируемый документ
`{"schema_version":1,"rows":[…],"total":{…}}` — свёрнутые по провайдерам строки
(`provider`, `input_tokens`, `output_tokens`, `cost_usd`) плюс итог; на этом
контракте удобно строить свои дашборды без парсинга таблицы. Пока JSON
поддерживает только этот «голый» вид и **несовместим** с `--by-model` (обе
команды) и с `--chart`/`--compare` (`history`) — такая комбинация даёт явную
ошибку. При отсутствии данных за период JSON остаётся валидным: печатается пустой
документ (`"rows": []`, нулевой `total`), а не человекочитаемое сообщение «No
usage data…». Строка ALERT при `--format=json` по-прежнему идёт в stderr и вывод
JSON в stdout не искажает.

Обе команды понимают `--fail-on-alert`: если задан порог `alert.monthly_usd` в
конфиге и итоговый расход за период **строго больше** порога, в stderr печатается
строка `ALERT: total spend $X exceeds monthly threshold $Y`, а с флагом
`--fail-on-alert` команда ещё и завершается с ненулевым кодом (для CI/cron). Без
флага печатается только предупреждение, exit-код остаётся нулевым. Порог `0` (или
отсутствие блока `alert`) отключает проверку. У `history --compare` алерт считается
по **текущему** периоду, не по прошлому. Строка ALERT идёт в stderr и не влияет на
таблицу/график в stdout.

## Конфигурационный файл

Файл — `~/.config/aicost/config.yaml` (точный путь берётся через
`os.UserConfigDir()`, кросс-платформенно; на macOS/Windows каталог другой). Файла
может и не быть — тогда работаем только на env-переменных. Пример:

```yaml
# ~/.config/aicost/config.yaml
http_timeout: 30s
max_retries: 4
db_path: ""              # пусто = os.UserConfigDir()/aicost/history.db

alert:
  monthly_usd: 200       # порог расхода; 0 или отсутствие блока = алерт выключен

providers:
  anthropic:
    enabled: true
    admin_key: ""        # лучше через env AICOST_ANTHROPIC_ADMIN_KEY (admin/org-ключ, НЕ ключ модели!)
    base_url: ""         # пусто = дефолт провайдера; override для прокси/тестов
  openai:
    enabled: true
    admin_key: ""        # или env AICOST_OPENAI_ADMIN_KEY
    base_url: ""
  openrouter:
    enabled: true
    admin_key: ""        # или env AICOST_OPENROUTER_ADMIN_KEY (management key, НЕ inference-ключ!)
    base_url: ""
```

Файл БД истории git-ignored (личные данные о расходах).

## Дорожная карта

- **Фаза 1 (MVP, завершена):** Anthropic + OpenAI, команды `report`/`history`,
  локальные снапшоты в SQLite, кросс-компиляция без CGO.
- **Фаза 2 (в работе):** OpenRouter ✅; тренды/графики в терминале ✅
  (`--chart`/`--by-model`/`--compare` у `history`, `--by-model` у `report`);
  алерты по порогу расхода ✅ (`alert.monthly_usd` + `--fail-on-alert`); далее
  больше провайдеров (Google Gemini), TUI (bubbletea),
  `--format=json`. Полноценный релизный пайплайн (goreleaser/CI-артефакты)
  — тоже кандидат Фазы 2; для MVP достаточно `go build` с `GOOS`/`GOARCH`.
  См. [`docs/POST_MVP_PLAN.md`](docs/POST_MVP_PLAN.md).

## Документация

- [`docs/PLAN.md`](docs/PLAN.md) — видение и план верхнего уровня.
- [`docs/TECHNICAL_PLAN.md`](docs/TECHNICAL_PLAN.md) — стек, архитектура, порт-адаптер, Этапы.
- [`docs/API_NOTES.md`](docs/API_NOTES.md) — реальные форматы usage/cost API провайдеров (Anthropic, OpenAI, OpenRouter).
- [`docs/POST_MVP_PLAN.md`](docs/POST_MVP_PLAN.md) — Фаза 2 и далее.

## Лицензия

MIT — см. [`LICENSE`](LICENSE).
