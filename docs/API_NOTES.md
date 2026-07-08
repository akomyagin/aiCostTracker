# API_NOTES — реальные usage/cost API провайдеров (Этап 1)

> Верификация допущений `[ASSUMPTION]`/`[TODO уточнить в Этапе 1]` из
> [`TECHNICAL_PLAN.md §4`](./TECHNICAL_PLAN.md). Проверено по живой документации
> **2026-07-08**. Здесь зафиксированы **факты**: пути эндпоинтов, авторизация,
> параметры, форма ответа. Адаптеры `internal/provider/{anthropic,openai}.go`
> реализованы по этим данным.

---

## 1. Anthropic — Usage & Cost Admin API

Официально: «Usage and Cost API» — часть Admin API организации. База —
`https://api.anthropic.com`.

Источники (проверено 2026-07-08):
- Get Cost Report — `https://platform.claude.com/docs/en/api/admin-api/usage-cost/get-cost-report`
- Get Messages Usage Report — `https://platform.claude.com/docs/en/api/admin-api/usage-cost/get-messages-usage-report`
- Usage & Cost API overview — `https://platform.claude.com/docs/en/manage-claude/usage-cost-api`

### 1.1 Авторизация

- **Admin API key** (`sk-ant-admin...`), выпускается владельцем организации в
  Console. Это **не** обычный ключ для вызова моделей (`sk-ant-api...`).
- Заголовок: `x-api-key: <admin_key>` (в примерах доков встречается и
  `Authorization: Bearer <oauth>` — но для нашего BYOK-случая используем
  `x-api-key`, как для всего Anthropic API).
- Обязателен `anthropic-version: 2023-06-01`.

### 1.2 Cost Report — **основной источник стоимости в USD**

`GET /v1/organizations/cost_report`

Query-параметры:
| Параметр | Тип | Примечание |
|---|---|---|
| `starting_at` | RFC 3339 string | обязателен; снапится к началу дня UTC |
| `ending_at` | RFC 3339 string | опционально; полуинтервал `[start, end)` |
| `bucket_width` | `"1d"` | у cost-репорта поддерживается **только `1d`** |
| `group_by[]` | `description` \| `workspace_id` | для нашего отчёта — `description` (даёт модель/тип) |
| `limit` | number | число бакетов |
| `page` | string | `next_page` из предыдущего ответа (пагинация) |

Форма ответа:
```json
{
  "data": [
    {
      "starting_at": "2025-08-01T00:00:00Z",
      "ending_at": "2025-08-02T00:00:00Z",
      "results": [
        {
          "amount": "123.78912",        // строка, в USD (НЕ в центах для cost_report)
          "currency": "USD",
          "cost_type": "tokens",         // tokens | web_search | code_execution | session_usage
          "description": "Claude Sonnet 4 Usage - Input Tokens",
          "model": "claude-opus-4-6",    // null если не группировать по description
          "token_type": "uncached_input_tokens",
          "context_window": "0-200k",
          "service_tier": "standard",
          "workspace_id": "wrkspc_..."
        }
      ]
    }
  ],
  "has_more": true,
  "next_page": "..."
}
```

Замечания реализации:
- `amount` — **строка-десятичная дробь в основных единицах валюты (USD-доллары,
  не центы)**. Парсим `strconv.ParseFloat`.
- Стоимость уже в USD — таблица цен не нужна (в отличие от usage-репорта). Это и
  снимает `[TODO §4.4]`: **Anthropic отдаёт готовую стоимость** через cost_report.
- `next_page` + `has_more` — курсорная пагинация; тянем страницы, пока
  `has_more == true`.

### 1.3 Messages Usage Report — токены (in/out), опционально

`GET /v1/organizations/usage_report/messages`

Ключевые query-параметры: `starting_at`, `ending_at`, `bucket_width`
(`1d`|`1h`|`1m`), `group_by[]` (`model`, `workspace_id`, `service_tier`, …),
`models[]`, `limit`, `page`.

Форма `results[]` (для токенов на день/модель):
```json
{
  "uncached_input_tokens": 1500,
  "cache_read_input_tokens": 200,
  "cache_creation": { "ephemeral_1h_input_tokens": 1000, "ephemeral_5m_input_tokens": 500 },
  "output_tokens": 500,
  "model": "claude-opus-4-6",
  "service_tier": "standard",
  "context_window": "0-200k"
}
```
Пагинация та же (`has_more`/`next_page`).

Наш `UsageRecord.InputTokens` = `uncached_input_tokens` (+ кэш-токены, если нужно
считать полный ввод); `OutputTokens` = `output_tokens`.

### 1.4 Как адаптер строит `Snapshot`

За окно `[Start, End)`:
1. `cost_report` с `group_by=description`, `bucket_width=1d` → стоимость USD по
   `(day, model)` (агрегируем `amount` всех `cost_type/token_type` строк с общей
   моделью в дне).
2. `usage_report/messages` с `group_by[]=model`, `bucket_width=1d` → токены по
   `(day, model)`.
3. Мёржим по ключу `(day, model)` в `[]UsageRecord`.

Если admin-ключа/сети нет — фатальная/ретраебельная ошибка по §3 плана.

---

## 2. OpenAI — Usage API + Costs API

База — `https://api.openai.com`. Обе — часть **Admin/Organization API**.

Источники (проверено 2026-07-08):
- Cookbook «Usage API and Cost API» — `https://developers.openai.com/cookbook/examples/completions_usage_api`
- Completions usage — `https://platform.openai.com/docs/api-reference/usage/completions`
- Costs — `https://developers.openai.com/api/reference/resources/admin/subresources/organization/subresources/usage/methods/costs`

### 2.1 Авторизация

- **Admin key** (`sk-admin-...`), создаётся в Organization settings → Admin keys.
  **Не** обычный `sk-...`-ключ проекта для вызова моделей.
- Заголовок: `Authorization: Bearer <admin_key>`.

### 2.2 Costs API — **основной источник стоимости в USD**

`GET /v1/organization/costs`

Query-параметры:
| Параметр | Тип | Примечание |
|---|---|---|
| `start_time` | **Unix seconds (int)** | обязателен; включительно |
| `end_time` | Unix seconds (int) | опционально |
| `bucket_width` | `"1d"` | у costs поддерживается только `1d` |
| `group_by[]` | `line_item` \| `project_id` | для отчёта — `line_item` (даёт модель/строку) |
| `limit` | int | число бакетов (для 1d по умолчанию 7) |
| `page` | string | курсор `next_page` |
| `project_ids[]` | string | опциональный фильтр |

Форма ответа:
```json
{
  "object": "page",
  "data": [
    {
      "object": "bucket",
      "start_time": 1730419200,
      "end_time": 1730505600,
      "results": [
        {
          "object": "organization.costs.result",
          "amount": { "value": 0.06, "currency": "usd" },  // value — число (доллары), currency lowercase
          "line_item": "gpt-4o-2024-08-06, input",          // null если не группировать
          "project_id": "proj_..."
        }
      ]
    }
  ],
  "has_more": true,
  "next_page": "..."
}
```
Замечания:
- `amount.value` — **число (float) в долларах USD**; `amount.currency` = `"usd"`.
  Готовая стоимость → таблица цен не нужна. Снимает `[TODO §4.4]` для OpenAI.
- Пагинация: `has_more` + `next_page` (курсор в `page`).
- `line_item` кодирует модель и тип токена (`"<model>, input"` / `", output"`) —
  из него достаём модель для группировки.

### 2.3 Usage / Completions API — токены (in/out), опционально

`GET /v1/organization/usage/completions`

Query-параметры: `start_time` (Unix s, обяз.), `end_time`, `bucket_width`
(`1m`|`1h`|`1d`, дефолт `1d`), `group_by[]` (`model`, `project_id`, `api_key_id`,
`user_id`, `batch`, `service_tier`, …), `models[]`, `limit`, `page`.

Форма `results[]`:
```json
{
  "object": "organization.usage.completions.result",
  "input_tokens": 1000,
  "output_tokens": 500,
  "input_cached_tokens": 200,
  "num_model_requests": 5,
  "model": "gpt-4o-2024-08-06"   // null если не группировать по model
}
```
Bucket: `{ "object": "bucket", "start_time", "end_time", "results": [...] }`;
конверт `{ "object": "page", "data": [...], "has_more", "next_page" }`.

Наш `UsageRecord.InputTokens` = `input_tokens`; `OutputTokens` = `output_tokens`.

### 2.4 Как адаптер строит `Snapshot`

Аналогично Anthropic: `costs` (group_by=line_item) → USD по `(day, model)`;
`usage/completions` (group_by=model) → токены по `(day, model)`; мёрж по ключу.
Границы окна конвертируем в **Unix-секунды** (`Window.Start.Unix()`).

---

## 3. Итог по снятым `[ASSUMPTION]`/`[TODO §4]`

| Вопрос из §4 | Ответ (факт) |
|---|---|
| Пути эндпоинтов | Anthropic: `/v1/organizations/cost_report`, `/v1/organizations/usage_report/messages`. OpenAI: `/v1/organization/costs`, `/v1/organization/usage/completions`. |
| Авторизация | Anthropic: `x-api-key: <admin>` + `anthropic-version: 2023-06-01`. OpenAI: `Authorization: Bearer <admin>`. Оба — admin/org-ключ, не ключ модели. |
| Гранулярность/пагинация | Оба: bucket по дню (`1d`), группировка по модели; курсорная пагинация `has_more`/`next_page`. Anthropic окно — RFC3339, OpenAI — Unix-секунды. |
| Готовая стоимость в USD? | **Да, оба.** Anthropic cost_report `amount` (строка, USD), OpenAI costs `amount.value` (float, USD). Таблица цен для MVP не нужна. |

Таким образом порт `ProviderUsageSource.Fetch` для обоих провайдеров опирается на
**cost-эндпоинт как источник стоимости** и **usage-эндпоинт как источник токенов**,
без выдуманных URL и без клиентской таблицы цен.
