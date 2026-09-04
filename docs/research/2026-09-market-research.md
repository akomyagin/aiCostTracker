# Рыночное исследование: AI cost/usage tracking & observability

> Снимок на **2026-09-04**. Готовилось для соло pet-проекта `aiCostTracker`
> (see [`../PLAN.md`](../PLAN.md), [`../TECHNICAL_PLAN.md`](../TECHNICAL_PLAN.md))
> с целью (1) найти похожие продукты — от прямых self-hosted-аналогов до
> смежного SaaS/enterprise-ландшафта, (2) понять их плюсы/минусы, (3) оценить
> применимость каждой значимой найденной возможности именно к этому проекту,
> (4) подобрать официальные Claude Skills и провайдерские SDK, полезные для
> дальнейшей разработки.

## 0. Методология и её ограничения

Исследование собрано 4 независимыми поисковыми проходами (self-hosted
CLI/TUI-аналоги; LLM-шлюзы/прокси; LLM observability/FinOps-платформы;
официальные SDK и Claude Skills), каждый с перепроверкой ключевых фактов
(лицензия, актуальность, способ сбора данных) минимум по двум источникам, где
это было возможно — обычно официальный README/документация + вторичное
подтверждение (GitHub API, страница pricing, независимый обзор). Отдельная
попытка прогнать это же исследование через автоматизированный
adversarial-verification харнесс (fan-out на 100+ субагентов с 3-голосной
верификацией) дважды упёрлась в системный rate-limit провайдера и не довела
синтез до конца — эта попытка отброшена, а данные ниже собраны более
консервативным подходом (4 параллельных агента, каждый сам ищет и
перепроверяет), что соответствует общему правилу проекта не гнаться за широким
веером параллельных субагентов.

**Честно о неопределённости.** Там, где источник не удалось подтвердить или
факты противоречили друг другу (например, статус лицензии PromptLayer, точные
даты последних релизов ряда проектов), это отмечено прямо в тексте —
додумывать за источники агенты не должны были. Цены/тарифы SaaS-продуктов на
2026-09 могут измениться; для решений, зависящих от цены, стоит перепроверить
на момент реализации.

---

## 1. Резюме

`aiCostTracker` занимает **малозаселённую нишу**: локальный self-hosted CLI без
сервера/телеметрии, который берёт **реальный биллинговый cost** из
usage/admin API нескольких AI-провайдеров (не оценивает его по прайс-таблице)
и хранит историю в SQLite для трендов. Ключевые выводы:

1. **Многолюдная часть ниши — не про нас.** Подавляющее большинство активных
   open-source-инструментов (`ccusage` — 18k★, `ccost`, `phuryn/claude-usage` —
   2.2k★, `claude-usage-tracker`) — это трекеры **одного** клиента
   (Claude Code и подобных агентных CLI), которые **парсят локальные JSONL-логи**
   и **оценивают** cost по прайс-таблице, а не берут его с биллинга провайдера.
   Это другой инвариант точности: наш подход даёт цифры от самого провайдера,
   их подход — оценку, которая может разойтись со счётом.
2. **Ближайшие по архитектуре аналоги малочисленны и не идентичны.**
   `openusage` (Go, TUI, ★186, очень активен) ближе всего по духу, но собирает
   данные гибридно (парсинг логов + проба заголовков + прямые вызовы к части
   API) — не чисто через admin/usage API. `LLMeter` (веб-платформа, AGPL-3.0,
   PostgreSQL) архитектурно ближе всего (прямой pull биллинга нескольких
   провайдеров), но это веб-сервис с БД, а не локальный CLI. Ранее
   упоминавшийся `claude-cost-cli` (Node CLI поверх Anthropic Admin API) —
   **выбыл**: репозиторий отдаёт 404, npm-пакет unpublished с 2026-03.
3. **LLM-шлюзы/прокси (LiteLLM, Portkey, Cloudflare AI Gateway, Kong/Higress,
   TrueFoundry, OpenRouter-как-сервис) — не конкуренты, а соседняя задача.**
   Все они встают в тракт запросов и считают cost по собственным логам ×
   прайс-карте модели — real-time, но встроено в рантайм и без гарантии
   "как в счёте у провайдера". Полезное для заимствования — открытые
   прайс-карты моделей (LiteLLM, Portkey) как справочник цен, но не архитектура.
4. **LLM observability/FinOps SaaS-платформы (Langfuse, Helicone, Phoenix,
   LangSmith, Traceloop, Braintrust, W&B Weave, Keywords AI, PromptLayer,
   Datadog LLM Observability, OpenMeter) в основном инструментируют рантайм
   приложения** (SDK/декораторы/OTel) ради трейсинга и evals — cost там
   вторичен. Из всего списка только **Vantage** и **CloudZero** (обе — FinOps,
   закрытый SaaS) собирают данные тем же способом, что и `aiCostTracker`
   (pull из billing/usage API постфактум) — это независимо подтверждает
   архитектурный выбор проекта.
5. **Официальные Go SDK провайдеров подтверждают, а не отменяют, решение
   "ручной net/http без SDK".** У Anthropic официальный Go SDK **вообще не
   покрывает** usage/cost admin-эндпоинты — ручной HTTP-клиент необходим. У
   OpenAI официальный Go SDK usage/cost **покрывает** (`Admin.Organization.Usage.*`,
   включая `.Costs`) — то есть для OpenAI ручная реализация уже не техническая
   необходимость, а сознательный выбор ради единообразия порта
   `ProviderUsageSource` (стоит явно зафиксировать эту причину в
   `TECHNICAL_PLAN.md`, см. §4 ниже). У OpenRouter официального Go SDK нет
   вовсе (только TypeScript) — ручной клиент обоснован так же, как для
   Anthropic.
6. **Из полусотни изученных фич — реально применимых к духу проекта немного.**
   Раздел 4 разбирает каждую и даёт явную рекомендацию; кратко: несколько
   маленьких, дешёвых в реализации идей (Prometheus-текстовый экспорт,
   явное обоснование выбора net/http для OpenAI в доке) стоит взять в бэклог;
   большая часть возможностей конкурентов (evals, guardrails, prompt-management,
   billing/invoicing, мультиоблачный FinOps, любая инструментация рантайма
   через SDK/прокси, любая исходящая телеметрия) **не подходит** духу
   "простой локальный CLI без сервера и телеметрии для pet-проекта".

---

## 2. Каталог продуктов по категориям

### 2.1 Self-hosted CLI/TUI аналоги

Раздел охватывает open-source инструменты для локального трекинга usage/cost
AI-моделей, распространяемые как CLI/TUI (без обязательного сервера и
телеметрии) — то есть прямые аналоги `aiCostTracker`. Продукты сгруппированы
по главному архитектурному признаку — **как они добывают данные**, потому что
именно это отличает `aiCostTracker` от большинства конкурентов:

- **Через provider usage/admin API** (подход `aiCostTracker`) — данные с
  биллинга провайдера, покрывают все обращения к API независимо от клиента.
- **Парсинг локальных логов клиента** (доминирующий подход в нише) — читают
  JSONL-транскрипты, которые Claude Code / Codex / Cursor и другие агенты
  пишут на диск; видят только то, что прошло через эти конкретные клиенты,
  cost считается оценочно по прайс-таблице.
- **SDK-обёртка / инструментация / прокси** — перехватывают вызовы в коде
  приложения.

Важный вывод «сверху»: **подавляющее большинство активных инструментов в нише
— это трекеры расхода Claude Code (и подобных агентных CLI) через парсинг
локальных JSONL-логов, а не агрегаторы биллинга нескольких провайдеров через
их usage API.** Ниша, которую занимает `aiCostTracker` (мульти-провайдерный
агрегатор именно через admin/usage API), густо не заселена; ближайший по духу
активный проект — `openusage` (тоже Go, но гибридный сбор данных).

#### openusage (janekbaraniewski/openusage) — ближайший аналог

- **Тип:** TUI + headless-CLI подкоманды; есть фоновый демон-сборщик.
- **Язык:** Go (1.25+). **Лицензия:** MIT.
- **Активность:** очень высокая — последний push 2026-09-03, ~186 звёзд.
- **Провайдеры:** заявлено 36 интеграций — coding-агенты (Claude Code, Cursor,
  Copilot, Codex CLI, Gemini CLI, OpenCode, Ollama) и API-платформы (OpenAI,
  Anthropic, Azure OpenAI, OpenRouter, Groq, Mistral, DeepSeek, Moonshot,
  Perplexity, xAI, Z.AI, Google Gemini, Alibaba).
- **Как получает данные:** гибридно — парсинг локальных логов (`~/.claude`,
  локальная SQLite Cursor, `~/.codex`, `~/.gemini`), проба rate-limit через
  заголовки ответов OpenAI/Anthropic, прямые API-вызовы к
  OpenRouter/Groq/Mistral/DeepSeek, browser-cookies для Perplexity,
  опциональные hooks/plugins.
- **Метрики:** spend, квоты, rate limits, токены, burn rate, per-model,
  5-часовые billing-блоки, сессии, тренды day/week/month.
- **Вывод:** живой дашборд, JSON, CSV, Prometheus-метрики, интеграция в tmux
  status bar и Claude Code statusline.
- **История:** локальная SQLite, фоновый демон непрерывно собирает данные.
- **Плюсы против aiCostTracker:** тот же язык (Go), несопоставимо шире охват
  (36 источников), TUI, экспорт в Prometheus, статус-бар интеграции.
- **Минусы против aiCostTracker:** сбор данных гибридный и «грязный» (cookies,
  проба заголовков, парсинг логов клиентов) — не чистый provider-биллинг;
  привязка к тому, какие клиенты стоят на машине, оценочный cost вместо
  биллингового. `aiCostTracker` идейно чище: единый порт `ProviderUsageSource`
  поверх официальных usage/admin API.

#### ccusage (ryoppippi/ccusage) — самый популярный в нише

- **Тип:** CLI (`npx ccusage@latest`, без установки).
- **Язык:** монорепо, основной пакет TypeScript/Node. **Лицензия:** MIT
  (`apps/ccusage/LICENSE` и `package.json` — MIT; корневой GitHub-классификатор
  показывает NOASSERTION из-за структуры монорепо — это артефакт
  классификации, фактически MIT).
- **Активность:** очень высокая — push 2026-09-04, релиз v20.0.20 от
  2026-08-15, ~18 351 звезда. Де-факто эталон ниши.
- **Провайдеры:** 18+ coding-агентных CLI (Claude Code, Codex, OpenCode, Amp,
  Droid, Codebuff, Goose, OpenClaw, Kilo, Kimi, Qwen, GitHub Copilot CLI,
  Gemini CLI, Grok CLI и др.). **Это мульти-*клиентский* трекер, не
  мульти-провайдерный биллинг.**
- **Как получает данные:** только парсинг локальных JSONL-логов агентов; есть
  offline-режим с пред-кэшированным прайсингом; сетевых вызовов к провайдерам
  нет.
- **Метрики:** токены (input/output/cache-creation/cache-read раздельно), cost
  в USD, агрегации daily/weekly/monthly/session, 5-часовые billing-окна Claude
  Code, per-model breakdown, группировка по проектам.
- **Вывод:** цветные адаптивные таблицы, compact-режим, JSON-экспорт.
- **История:** отчёты строятся из сырых логов; собственной персистентной БД
  истории README явно не заявляет (в отличие от SQLite-снапшотов
  `aiCostTracker`).
- **Плюсы:** зрелость и экосистема (Raycast-расширение), zero-setup (`npx`),
  детальный per-token/cache-breakdown.
- **Минусы:** видит только локальные агентные CLI; cost оценочный по
  прайс-таблице, не биллинговый; не покрывает прямые вызовы API из
  собственного кода; истории трендов из БД не хранит.

#### aitoken-cli (brian-mwirigi/aitoken-cli)

- **Тип:** CLI + программный API. **Язык:** TypeScript/Node (18+).
  **Лицензия:** MIT.
- **Активность:** низкая — последний коммит 2026-03-07, ~17 коммитов,
  0 звёзд. Похоже на выходной-проект.
- **Провайдеры:** OpenAI, Anthropic, Google, Azure OpenAI, Cohere — 42 модели
  с нормализованным прайсингом.
- **Как получает данные:** **не** provider usage API, а инструментация в коде
  пользователя — wrapper-функции, middleware, drop-in SDK-расширения, плюс
  ручное логирование командой `at add`. Считает только то, что пользователь
  сам пропустил через обёртку.
- **Метрики:** суммарные запросы/токены/cost, разбивка по провайдерам, доля
  трат по провайдеру, фильтр по периодам.
- **Вывод:** ASCII-таблицы, JSON-экспорт, справочник цен.
- **История:** SQLite в `~/.token-tracker/usage.db`, local-first.
- **Плюсы:** мульти-провайдерная нормализация цен «из коробки», local-first
  SQLite, поддержка Cohere/Azure.
- **Минусы:** требует встраивания в код приложения — не подходит для
  пассивного аудита уже потраченного; не берёт данные с биллинга провайдера;
  практически заброшен.

#### ccost (carlosarraes/ccost)

- **Тип:** CLI single-binary + библиотека. **Язык:** Rust (edition 2024).
  **Лицензия:** README заявляет MIT, **но LICENSE-файла в репозитории нет**
  (GitHub API отдаёт `license: null`) — считать «MIT по README, юридически не
  подтверждён файлом».
- **Активность:** остановлена — последний коммит и релиз v0.2.0 от
  2025-06-21, ~9 звёзд. Неактивен больше года. (Есть форки-однофамильцы —
  другие репозитории, не путать.)
- **Провайдеры:** только Anthropic Claude.
- **Как получает данные:** парсинг локальных JSONL из `~/.claude/projects/`;
  cost по прайсингу LiteLLM; дедупликация стрим-записей по `requestId`.
- **Метрики:** input/output/cache-creation/cache-read токены, cost per
  project/day/model, статистика дублей, daily/weekly/monthly.
- **Вывод:** таблица, JSON, privacy-режим; мульти-валюта (USD/EUR/GBP/JPY/
  CNY/BRL) с курсами.
- **История:** SQLite-кэш в `~/.config/ccost/` (WAL), 24-часовое кэширование
  курсов и прайсинга (это кэш, не долгая история трендов).
- **Плюсы:** single-binary на Rust, мульти-валюта, аккуратная дедупликация,
  несколько форматов вывода.
- **Минусы:** один провайдер, парсинг логов вместо API, заброшен, лицензия
  файлом не закреплена.

#### phuryn/claude-usage

- **Тип:** local dashboard — web (localhost) + CLI + VS Code-расширение;
  есть Docker. **Язык:** Python 3.8+ (только stdlib). **Лицензия:** MIT.
- **Активность:** высокая — релиз v1.5.5 от 2026-07-10, ~2 198 звёзд.
- **Провайдеры:** только Claude Code.
- **Как получает данные:** парсинг локальных JSONL из `~/.claude/projects/`;
  инкрементальное сканирование по mtime.
- **Метрики:** input/output и cache-токены, cost по прайсингу Anthropic,
  per-session/per-project, daily/weekly/all-time; прогресс-бар против
  месячного лимита подписки Pro/Max.
- **Вывод:** web-дашборд (Chart.js), таблицы, фильтры по датам; CLI-команды.
- **История:** SQLite в `~/.claude/usage.db`.
- **Плюсы:** прогресс-бар квоты Pro/Max, богатый web-UI, VS Code-виджет.
- **Минусы:** один клиент, не CLI/TUI-first, cost оценочный, не
  мульти-провайдер.

#### 658jjh/claude-usage-tracker

- **Тип:** desktop-app (Electron/macOS) + browser-fallback + CLI-компонент.
  **Язык:** JavaScript/Node + Python. **Лицензия:** MIT.
- **Активность:** активен — push 2026-08-12, ~58 звёзд.
- **Провайдеры/клиенты:** Claude-экосистема и Codex.
- **Как получает данные:** парсинг локальных JSONL/log-файлов; API не
  используется.
- **Метрики:** spend day/week/month/all-time, cost by source/model/project,
  heatmap пиковых часов, timeline сессий, месячные прогнозы.
- **Вывод:** интерактивный HTML-дашборд.
- **История:** локально, «100% local, zero telemetry».
- **Плюсы:** широкий охват Claude-клиентов, богатая визуализация.
- **Минусы:** desktop-first, не терминальный; только парсинг логов;
  фактически один вендор.

#### openrouter-usage-monitor (mhd-medfa/openrouter-usage-monitor)

- **Тип:** CLI (один Python-файл). **Язык:** Python 3.10+. **Лицензия:** MIT.
- **Активность:** низкая — последний коммит 2026-02-04, 0 звёзд.
- **Провайдеры:** только OpenRouter.
- **Как получает данные:** прямой вызов `openrouter.ai/api/v1/auth/key` —
  **подход через API провайдера**, как у `aiCostTracker`.
- **Метрики:** потраченные кредиты, лимит, остаток, прогресс-бар, rate
  limits. Только текущий срез.
- **Вывод:** цветной терминальный вывод.
- **История:** нет.
- **Плюсы:** предельная простота, API-подход.
- **Минусы:** один провайдер, нет истории/трендов, нет per-model/per-day.

#### LLMeter (amedinat/LLMeter) — на грани ниши (web-платформа)

- **Тип:** web-платформа с опцией self-hosting (не CLI/TUI). **Язык:**
  TypeScript (Next.js). **Лицензия:** AGPL-3.0.
- **Активность:** активна — push 2026-08-31, ~7 звёзд.
- **Провайдеры:** OpenAI, Anthropic, Mistral, DeepSeek, OpenRouter (billing
  API); Google AI, Azure OpenAI, AWS Bedrock (SDK-обёртки).
- **Как получает данные:** подключается напрямую к usage/billing API
  провайдеров (фоновые cron-опросы, post-facto) — **та же идея, что у
  aiCostTracker**, но сразу на несколько провайдеров и на вебе.
- **Метрики:** cost, запросы, токены по провайдеру/модели; budget-алерты
  (email/Slack), anomaly detection (Pro), Prometheus/Grafana endpoint,
  CSV/PDF-экспорт (Pro+).
- **Вывод:** web-дашборд. **История:** PostgreSQL (Supabase), ключи
  шифруются AES-256-GCM.
- **Плюсы:** мульти-провайдерный биллинг-подход, budget-алерты, anomaly
  detection, Prometheus/Grafana.
- **Минусы:** веб-платформа с БД PostgreSQL/Supabase и платными tier-ами, не
  локальный терминальный CLI; AGPL-3.0 (сильный copyleft); тяжёлый стек.

#### Не подтверждено / выбыло

- **claude-cost-cli (cyberash-dev/claude-cost-cli)** — заявлялся как CLI
  поверх Anthropic Admin API — прямой архитектурный аналог. Проверить не
  удалось: GitHub-репозиторий отдаёт 404, npm-пакет unpublished (снят
  2026-03-09). Считать **исчезнувшим**. Есть одноимённый Claude Code *skill*
  (ClawHub/CodaOne) — это другое (навык, не самостоятельный трекер).
- **hassanazam/claude-cost** — Python, MIT, 0 звёзд, низкая активность (push
  2025-10-27); дублирует нишу ccusage/phuryn без отличий в подходе.
- **shreyasgm/anthropic-usage-skill** — это Claude Code *skill*, не
  самостоятельный CLI/TUI-инструмент.

#### Итоговое позиционирование aiCostTracker (self-hosted-сегмент)

Ниша разбита надвое:
1. **Многолюдная сторона — трекеры логов агентных CLI** (ccusage, ccost,
   phuryn/claude-usage, claude-usage-tracker): считают уже потраченное по
   локальным JSONL, cost оценочный, обычно один вендор (Claude).
2. **Разреженная сторона — агрегаторы биллинга через provider usage/admin
   API** (подход `aiCostTracker`): активны фактически только `openusage`
   (Go, гибридный сбор) и `LLMeter` (web-платформа на AGPL с PostgreSQL).
   Чистого локального терминального мульти-провайдерного трекера именно
   через usage/admin API с SQLite-историей и zero-cost/zero-telemetry —
   прямого совпадения не найдено; `aiCostTracker` занимает эту точку.

Уникальное сочетание **(a) provider usage/admin API как источник
(биллинговая точность) + (b) несколько провайдеров + (c) чисто локальный CLI
без сервера/БД-стека + (d) SQLite-история трендов + (e) zero-telemetry/
zero-cost** — ни один из проверенных активных проектов не даёт всё сразу.

---

### 2.2 LLM-шлюзы/прокси с cost-трекингом

Ключевое архитектурное отличие всей этой категории от `aiCostTracker`
вынесено в конец раздела как отдельный вывод.

#### LiteLLM

- **Тип:** self-hosted (open-source) и SaaS/Enterprise.
- **Лицензия и монетизация:** **open core**. Ядро (прокси-сервер, SDK) —
  открытый исходный код (BerriAI/litellm), бесплатно для self-host «навсегда».
  Одновременно в репозитории есть **LiteLLM Commercial License** для
  enterprise-функций — строго говоря это dual-license/open core, а не чистый
  MIT на всё.
  - **Бесплатно в OSS:** unified API, прокси-сервер, **spend tracking**,
    virtual keys, users & teams, load balancing, logging.
  - **Enterprise-only:** SSO+SCIM, OIDC/JWT, audit logs, org/team admin
    controls, secret managers, multi-region control plane, SLA-поддержка.
- **Провайдеры:** 100+ (заявлено до 140+, ~1892 модели).
- **Как считает cost:** по собственным прокси-логам запросов + карта цен
  моделей (`model_prices_and_context_window.json`), не через provider usage
  API.
- **Метрики:** spend per key/user/team/org/model/tag; бюджеты и капы;
  rate limiting. Данные — в реляционной БД.
- **Отчётность:** admin UI/дашборд, API, БД (SQL).
- **Плюсы:** самый широкий охват провайдеров; cost-трекинг в бесплатной
  OSS-версии; token-level стоимость; активное развитие.
- **Минусы:** архитектура «прокси перехватывает трафик»; часть функций и код
  закрыты коммерческой лицензией.

#### Portkey Gateway

- **Тип:** self-hosted (open-source ядро) и SaaS/Enterprise.
- **Лицензия:** ядро (`Portkey-AI/gateway`) — **MIT**. Модель open core:
  роутинг открыт, но **usage analytics/cost tracking — только в hosted и
  enterprise-версиях**, в OSS-ядре наблюдаемости по стоимости нет.
- **Провайдеры:** 1600+ моделей / 250+ LLM от 45+ провайдеров.
- **Как считает cost:** через собственные прокси-логи; открытая прайс-база на
  2300+ LLM у 35+ провайдеров.
- **Плюсы:** очень лёгкий и быстрый gateway (sub-1ms overhead, ~122 KB);
  MIT-ядро; открытая прайс-база моделей.
- **Минусы:** **нужная нам функция (cost/usage-аналитика) не входит в
  бесплатное self-host-ядро.**

#### Cloudflare AI Gateway

- **Тип:** только SaaS (управляемый сервис Cloudflare, не self-hosted, не
  open-source).
- **Лицензия и монетизация:** проприетарный; core-функции (аналитика, кэш,
  rate limiting, логи) бесплатны на всех тарифах; Unified Billing (2026) —
  комиссия 5% на покупаемые сторонним провайдерам кредиты, наценки на
  инференс нет.
- **Как считает cost:** по перехваченному трафику — token usage × прайс
  модели, spend суммируется в реальном времени.
- **Метрики:** spend limits как бюджеты (daily/weekly/monthly, fixed/rolling)
  с разрезом по model/provider/кастомным атрибутам.
- **Плюсы:** бесплатные core-функции, zero-markup на инференс, готовые
  бюджеты/лимиты.
- **Минусы:** полностью проприетарный SaaS, трафик и учёт живут в облаке.

#### TrueFoundry AI Gateway

- **Тип:** self-hostable (VPC/on-prem/air-gapped) и hosted, но
  **проприетарный** — не открыт для свободного запуска/аудита.
- **Лицензия:** сам gateway закрытый; в open-source у TrueFoundry только
  несвязанный компонент TrueForge (MIT).
- **Как считает cost:** через перехват трафика — real-time usage,
  token-level tracking, budget enforcement.
- **Плюсы:** granular token-level cost-трекинг, работа с self-hosted
  моделями.
- **Минусы:** не open-source (кроме несвязанного TrueForge), нужен
  коммерческий контракт, тяжёлая enterprise-архитектура.

#### Kong AI Gateway / Higress

**Kong AI Gateway** — не отдельный продукт, а набор функций в
Enterprise/Konnect; в OSS Kong Gateway — только базис. Нужные cost-функции —
за Enterprise-лицензией; учёт **по запросам, а не по токенам** (в отличие от
конкурентов).

**Higress** — self-hosted, **полностью open-source** (CNCF Sandbox, Apache
2.0), на базе Istio/Envoy. Учёт через перехват трафика — token rate
limiting/квоты; явного пересчёта в доллары в источниках не подчёркнуто —
учёт скорее **token-centric, чем cost-centric** (долларовый cost-трекинг не
подтверждён). Тяжёлая инфраструктура (Envoy/Istio).

#### OpenRouter (как сервис/gateway)

- **Тип:** только hosted SaaS, не open-source.
- **Монетизация:** без наценки на инференс; доход — комиссия на пополнение
  кредитов 5.5% (Stripe) / 5% (крипта), BYOK-fee 5% сверх порогов.
- **Как считает cost:** по собственным прокси-логам × прайс модели.
- **Метрики:** Activity-дашборд в реальном времени, spend per model/key,
  usage-алерты, **credits API** для программного получения баланса.
- **Плюсы:** мгновенный доступ к 500+ моделям, zero-markup на инференс,
  готовый usage-API и дашборд.
- **Минусы:** hosted-only, не open-source; комиссии на кредиты/BYOK.

#### Helicone (кратко — gateway-режим)

В основном это observability-платформа (см. §2.3), но есть и AI Gateway-режим
(OpenAI-совместимый шлюз, роутинг/кэш/fallback/cost-based routing). Учёт —
по перехваченным логам. **После приобретения Mintlify (март 2026) переведён
в maintenance mode** — новых фич не планируется, open-source self-host
остаётся.

#### Ключевой вывод: архитектурное отличие от aiCostTracker

Все продукты этого раздела построены на архитектуре **«прокси перехватывает
трафик»**: приложение шлёт запросы к моделям *через* шлюз, а тот считает
стоимость по **собственным логам запросов** (token usage × прайс-мап).

`aiCostTracker` намеренно занимает **противоположную нишу**: не встаёт в
тракт запросов, а **читает provider usage/admin API постфактум**. Следствия:

- **Ноль вмешательства в рантайм** — `aicost` не может ничего сломать в
  проде, т.к. вне тракта; прокси добавляет точку отказа и latency на каждый
  вызов.
- **Полнота охвата** — usage API провайдера видит **все** расходы аккаунта
  (включая вызовы из других приложений/консоли/чужих ключей); прокси видит
  только то, что прошло через него.
- **Источник истины по деньгам** — usage/admin API даёт цифры **от самого
  провайдера**; у прокси стоимость — оценка по прайс-мапе (риск рассинхрона
  со скидками/tier-ценами).
- **Задержка данных** — обратная сторона: прокси знает cost в реальном
  времени по запросу, usage API часто отдаёт данные с задержкой (агрегаты за
  период). `aicost` — не для мгновенного per-request контроля, а для трендов
  и сверки счетов.

Эти шлюзы — не конкуренты и не референс-архитектура, а решение соседней
задачи. Заимствовать у них стоит разве что **прайс-мапы моделей** как
справочник цен (см. §4) — но не саму прокси-модель сбора данных.

---

### 2.3 LLM observability / FinOps-for-AI платформы

> Почти все платформы этого раздела получают данные через **SDK-инструментацию
> / трейсинг вызовов LLM внутри приложения** (или через proxy/gateway на пути
> запроса). `aiCostTracker` работает иначе — тянет агрегированный usage/cost
> из admin/usage API провайдеров постфактум, не встраиваясь в рантайм
> приложения. Это разные ниши: они видят каждый промпт и его качество, мы
> видим счёт от провайдера.

#### Langfuse

- **Тип:** self-hosted open-source и SaaS (open-core).
- **Лицензия:** ядро — **MIT** (трейсинг, evals, prompt management, datasets,
  playground — без feature-gates при self-host); `ee/`-каталог — под
  коммерческой лицензией (SCIM, audit log, retention policies).
- **Актуальность:** активен; с января 2026 — часть **ClickHouse**, заявлено
  сохранение open-source и self-host.
- **Как получает данные:** SDK-инструментация приложения + приём OTLP-трейсов.
- **Фичи сверх cost:** трейсинг, evals (LLM-as-judge, feedback), prompt
  management с версиями, datasets/benchmarks, playground, per-project
  разбивка.
- **Плюсы для нас:** эталон open-core (MIT-ядро + платная обвязка); идея
  per-project разбивки и хранения истории для трендов созвучна нашему SQLite.
- **Минусы:** требует SDK-встраивания и тяжёлого стека (ClickHouse + сервер)
  — прямо противоречит «чистый CLI, $0/мес, без сервера»; evals/playground/
  prompt management вне scope cost-трекера.

#### Helicone

- **Тип:** self-hosted open-source и SaaS (SaaS сворачивается).
- **Лицензия:** **Apache 2.0**.
- **Актуальность:** **приобретён Mintlify, анонс 3 марта 2026**; переведён в
  **maintenance mode** (security-патчи и багфиксы продолжаются, новых фич
  нет); hosted-платформа сворачивается, open-source self-host остаётся.
- **Как получает данные:** AI Gateway (proxy) или асинхронное логирование
  (OpenLLMetry) — оба уровня приложения/сети, не usage-API провайдера.
- **Фичи сверх cost:** кэширование, rate limiting, fallback между
  провайдерами, prompt versioning, session/agent-трейсинг.
- **Плюсы:** архитектурно близкая ниша cost-visibility, простой онбординг —
  ориентир по UX; Apache 2.0 self-host.
- **Минусы:** **maintenance mode после acquisition** — предостережение против
  ставки на активное развитие; proxy-режим неприемлем для локального CLI.

#### Arize Phoenix

- **Тип:** self-hosted source-available (+ коммерческий Arize AX как
  отдельный SaaS).
- **Лицензия:** **Elastic License 2.0** — source-available, **не**
  OSI-одобренная (запрещена перепродажа как конкурирующего managed-сервиса).
- **Как получает данные:** OTel/OpenInference-инструментация приложения; есть
  **отключаемая** product-телеметрия (`PHOENIX_TELEMETRY_ENABLED=false`).
- **Фичи сверх cost:** трейсинг, evals, datasets, experiments, prompt
  management, playground.
- **Плюсы:** **SQLite-режим для маленького деплоя** — прямая параллель
  нашему выбору хранилища; ELv2 как пример «почти open, но с защитой от
  перепродажи».
- **Минусы:** ELv2 ≠ настоящий open-source; даже отключаемая телеметрия —
  против принципа «без телеметрии» проекта. Evals/experiments вне scope.

#### LangSmith (LangChain)

- **Тип:** проприетарный SaaS; self-host — только Enterprise-add-on
  (непубличная цена, деплой в собственный VPC/K8s).
- **Как получает данные:** SDK-инструментация приложения.
- **Фичи сверх cost:** трейсинг, online/offline evals, Prompt Hub +
  Playground; cost вторичен, фокус на debugging агентов.
- **Вывод:** практически ничего переносимого для cost-CLI; полезен лишь как
  антипример по стоимости/сложности self-host.

#### Traceloop / OpenLLMetry

- **Тип:** open-source SDK (Apache 2.0) + опциональный SaaS (Traceloop).
- **Как получает данные:** инструментация на базе **OpenTelemetry** —
  стандартные OTel-спаны/метрики, экспорт в любой OTel-бэкенд.
- **Плюсы:** чистый Apache-2.0, OTel-стандарт (в Go OTel — первоклассный
  гражданин); идея «эмитим данные, хранит кто-то другой» — противоположная
  нашей, но иллюстрирует стандарт GenAI-семантики в OTel.
- **Минусы:** это инструментация рантайма, а не pull из usage-API; полный
  OTel-конвейер избыточен для локального CLI.

#### Braintrust

- **Тип:** проприетарный SaaS (self-host возможен без лицензионной платы, но
  без SOC2/ISO).
- **Как получает данные:** SDK-инструментация, eval-first парадигма.
- **Плюсы:** идея golden-датасетов и регрессионного сравнения созвучна нашим
  golden-тестам (но в коде, не в проде).
- **Минусы:** closed-source; eval-first парадигма полностью вне cost-tracking
  scope.

#### Weights & Biases Weave

- **Тип:** open-source SDK (Apache 2.0) + SaaS; полноценный self-host —
  коммерческий.
- **Как получает данные:** SDK-декоратор `@weave.op` — авто-захват
  inputs/outputs/cost/latency/токенов на каждый вызов.
- **Фичи сверх cost:** evals, **guardrails** (toxicity/bias/PII/hallucination),
  **monitors** (тренды качества во времени), datasets.
- **Плюсы:** авто-подсчёт cost/token на вызов — концептуально то, что мы
  делаем на уровне агрегата; идея monitors/трендов совпадает с нашей
  SQLite-историей.
- **Минусы:** guardrails/PII-детект/eval-скореры — тяжёлый ML-обвес,
  избыточный для pet-CLI.

#### Keywords AI

- **Тип:** проприетарный SaaS, self-host отсутствует.
- **Как получает данные:** AI-gateway (proxy) + трейс-логирование.
- **Плюсы:** дешёвый вход ($9/мес) как ориентир по цене; идея spend-лимитов.
- **Минусы:** закрытый, без self-host и прозрачной retention-политики —
  прямо конфликтует с локальностью aiCostTracker.

#### PromptLayer

- **Тип:** SaaS; self-host — только Enterprise. **Лицензионный статус
  противоречив** — маркетинг называет «open-source self-hostable», но
  подтверждения OSI-лицензии в официальном репозитории не найдено (факт
  неподтверждён).
- **Как получает данные:** логирование запросов между приложением и
  провайдером (proxy/SDK).
- **Вывод:** ориентация на визуальный prompt-management для не-технических
  команд — полностью вне scope cost-CLI.

#### Datadog LLM Observability

- **Тип:** проприетарный SaaS, без self-host.
- **Как получает данные:** инструментация приложения (LLM-спаны в APM),
  метрики со 100% трафика.
- **Фичи сверх cost:** полный APM-трейсинг, error tracking, **Cost
  Management с алертами по бюджету**.
- **Плюсы:** масштаб поддержки моделей (800+) и алерты по бюджету — интересно
  «духом».
- **Минусы:** закрытый SaaS без self-host, дорогой (вторичные оценки роста
  счёта до $10k+/мес — точность не гарантирована), требует полной
  Datadog-инструментации.

#### OpenMeter (by Kong)

- **Тип:** self-hosted open-source (**Apache 2.0**) + managed.
- **Актуальность:** вошёл в Kong, интеграция ожидается к 2026.
- **Как получает данные:** ingestion событий (SDK на Go/TS/Python/cURL или
  коллекторы) — миллионы событий/сек, дедупликация.
- **Фичи сверх cost:** реал-тайм metering и usage-лимиты, billing/invoicing,
  revenue-аналитика.
- **Плюсы:** **Go-стек, Apache 2.0** — технологически близко; архитектура
  «events in → aggregation → usage» концептуально та же агрегация; идея
  дедупликации/идемпотентности ingestion перекликается с нашим идемпотентным
  upsert `Save`.
- **Минусы:** это billing-инфраструктура для монетизации SaaS — на порядок
  тяжелее личного учёта расходов; реал-тайм ingestion миллионов событий
  избыточен для «дёргаю usage-API по команде».

#### Vantage (FinOps)

- **Тип:** проприетарный SaaS, есть free-tier.
- **Как получает данные:** **billing/usage-интеграции с провайдерами
  (token-level ingest)** — нативно для OpenAI, Anthropic, Databricks,
  Anyscale; агрегирует также Bedrock, Azure OpenAI, GCP Vertex AI, Cursor.
  **Единственная платформа в этом разделе, чей способ сбора данных близок
  нашему** (pull cost/usage от провайдера, а не инструментация рантайма).
- **Фичи сверх cost:** разбивка по developer/model/project, unified-дашборд
  мультипровайдерного спенда, бюджеты/алерты, MCP-сервер для запроса
  AI-спенда из AI-ассистентов.
- **Плюсы:** самый близкий по духу способ сбора данных и гранулярность
  per-model/per-project — прямой ориентир для нашей агрегации.
- **Минусы:** закрытый SaaS (данные уходят вендору); широкий FinOps-охват
  (K8s, Snowflake и пр.) избыточен для «только AI-провайдеры».

#### CloudZero (FinOps)

- **Тип:** проприетарный SaaS.
- **Как получает данные:** ingest cost-данных из billing/usage каждого
  источника (включая Anthropic/OpenAI/Bedrock), нормализация в единую
  модель. Способ близок нашему pull-подходу (хотя один из вторичных
  источников отмечает, что LLM-специфичная атрибуция у CloudZero менее
  зрелая, чем общий cloud-cost).
- **Фичи сверх cost:** unit-economics — cost per feature/deployment/customer,
  привязка LLM/GPU-спенда к P&L.
- **Плюсы:** pull cost из usage/billing API созвучен нашему подходу; идея
  нормализации разнородных источников в единую модель — полезный ориентир
  для порта `ProviderUsageSource`.
- **Минусы:** закрытый enterprise-SaaS с фокусом на unit-economics/P&L —
  бизнес-требования, которых у pet-проекта нет.

#### Что перенять «духом» vs что явно избыточно

**Интересно перенять (духом, без сервера/телеметрии):**
- Pull из usage/billing API провайдера как способ сбора — подтверждается
  только у Vantage и CloudZero (FinOps-класс); валидирует архитектурный
  выбор `aiCostTracker`.
- Per-model/per-project(-team) разбивка и бюджетные алерты по порогу
  (Vantage, CloudZero, Datadog, Keywords AI) — легковесно реализуемо в CLI.
- История/тренды во времени (W&B monitors, Langfuse) — уже реализовано через
  SQLite; подтверждение направления.
- Идемпотентный ingest/дедупликация (OpenMeter) — совпадает с нашим
  upsert-подходом `Save`.
- Нормализация разнородных провайдеров в единую модель (CloudZero) — ровно
  роль порта `ProviderUsageSource`.
- Open-core/permissive-лицензия и простой онбординг (Langfuse MIT,
  Helicone/OpenMeter/Weave Apache 2.0) — ориентир по духу лицензирования.

**Явно избыточно для pet-CLI без бизнес-требований:**
- SDK-инструментация рантайма, proxy/gateway на пути прод-трафика,
  OTel-конвейеры.
- Evals/LLM-as-judge/A-B промптов/playground/prompt-management.
- Guardrails, PII/toxicity-детект.
- Billing/invoicing/revenue-recognition, тарифные каталоги.
- Мультиоблачный FinOps-охват, unit-economics/P&L.
- Любая телеметрия вендору и закрытый SaaS без self-host.

---

## 3. Сравнительная таблица

| Продукт | Категория | Тип поставки | Лицензия | Способ сбора данных | Провайдеры (AI) | История | Формат вывода | Активность (2026-09) |
|---|---|---|---|---|---|---|---|---|
| **aiCostTracker** | self-hosted CLI | локальный бинарник | (частный проект) | provider usage/admin API | Anthropic, OpenAI, OpenRouter | SQLite | таблица/JSON/чарт | активная разработка |
| openusage | self-hosted TUI/CLI | локальный бинарник + демон | MIT | гибрид: логи + заголовки + прямые API | 36 источников (агенты + API) | SQLite | TUI/JSON/CSV/Prometheus | очень высокая |
| ccusage | self-hosted CLI | `npx`, без установки | MIT | парсинг локальных JSONL | 18+ агентных CLI (не provider API) | нет персистентной БД | таблица/JSON | очень высокая (18k★) |
| aitoken-cli | self-hosted CLI | npm-пакет | MIT | SDK-обёртка/ручное логирование | OpenAI, Anthropic, Google, Azure, Cohere | SQLite | таблица/JSON | низкая (заброшен) |
| ccost | self-hosted CLI | single-binary (Rust) | MIT (без LICENSE-файла) | парсинг локальных JSONL | только Claude | SQLite-кэш (не тренды) | таблица/JSON | остановлена (2025-06) |
| phuryn/claude-usage | self-hosted dashboard | web+CLI, Docker | MIT | парсинг локальных JSONL | только Claude Code | SQLite | web-дашборд/CLI | высокая (2.2k★) |
| claude-usage-tracker | self-hosted desktop | Electron-приложение | MIT | парсинг локальных логов | Claude-экосистема, Codex | локальный файл | HTML-дашборд | активен |
| openrouter-usage-monitor | self-hosted CLI | Python-скрипт | MIT | provider API (OpenRouter) | только OpenRouter | нет | терминальный вывод | низкая |
| LLMeter | web-платформа | self-host или SaaS | AGPL-3.0 | provider billing API (postfacto) | OpenAI, Anthropic, Mistral, DeepSeek, OpenRouter, +SDK | PostgreSQL | web-дашборд | активна |
| LiteLLM | gateway/proxy | self-hosted + SaaS | open core (OSS ядро + Commercial) | прокси-логи × прайс-карта | 100+ (прокси) | реляционная БД | admin UI/API | очень высокая |
| Portkey Gateway | gateway/proxy | self-hosted (ядро) + hosted | MIT-ядро, cost-analytics только hosted | прокси-логи | 250+ (прокси) | (hosted) | дашборд (hosted) | высокая |
| Cloudflare AI Gateway | gateway/proxy | только SaaS | проприетарный | перехват трафика | основные провайдеры | облако Cloudflare | дашборд | активна |
| TrueFoundry AI Gateway | gateway/proxy | self-hostable, closed-source | проприетарный | перехват трафика | hosted + self-hosted модели | (enterprise) | дашборд | активна |
| Kong AI Gateway | gateway/proxy | Enterprise/Konnect | проприетарный (Enterprise) | request counting | через прокси | (enterprise) | дашборд | активна |
| Higress | gateway/proxy | self-hosted (CNCF) | Apache 2.0 | token-квоты (не подтверждено $ USD) | через прокси | — | консоль/observability | активна |
| OpenRouter (gateway) | gateway/proxy | только SaaS | проприетарный | прокси-логи | 500+ моделей | — | Activity-дашборд/API | активна |
| Langfuse | observability | self-hosted + SaaS | MIT-ядро (ee/ коммерческий) | SDK/OTel-инструментация | provider-agnostic | Postgres+ClickHouse | web-дашборд | активна |
| Helicone | observability/gateway | self-hosted + SaaS (сворачивается) | Apache 2.0 | proxy или OTel-логирование | 100+ моделей | (self-host) | web-дашборд | **maintenance mode** |
| Arize Phoenix | observability | self-hosted (source-available) | Elastic License 2.0 | OTel/OpenInference | provider-agnostic | SQLite (small) / Postgres | web-UI | активна |
| LangSmith | observability | SaaS (+Enterprise self-host) | проприетарный | SDK-инструментация | provider-agnostic | (managed) | web-дашборд/API | активна |
| Traceloop/OpenLLMetry | observability (SDK) | open-source SDK + SaaS | Apache 2.0 | OTel-инструментация | provider-agnostic | (внешний бэкенд) | OTLP-экспорт | активна |
| Braintrust | observability/evals | SaaS (+self-host FOSS) | проприетарный | SDK-инструментация | provider-agnostic | (managed) | web-дашборд | активна |
| W&B Weave | observability | SDK + SaaS (+enterprise self-host) | Apache 2.0 (SDK) | SDK-декоратор | provider-agnostic | (managed) | web-дашборд | активна |
| Keywords AI | observability/gateway | только SaaS | проприетарный | proxy + логирование | мульти-провайдерный gateway | (managed) | web-дашборд | активна |
| PromptLayer | observability | SaaS (+Enterprise self-host) | статус неподтверждён | proxy/SDK логирование | provider-agnostic | (managed) | web-workspace | активна |
| Datadog LLM Observability | observability | только SaaS | проприетарный | SDK-инструментация (APM) | 800+ моделей (оценочно) | (managed) | web-дашборд | активна |
| OpenMeter | FinOps/metering | self-hosted (Kong) + managed | Apache 2.0 | event ingestion | не привязан к LLM | (managed) | API/аналитика | активна (интеграция в Kong) |
| Vantage | FinOps | только SaaS | проприетарный | provider billing/usage API (pull) | OpenAI, Anthropic, Bedrock, Azure OpenAI, Vertex AI | (managed) | web-дашборд/MCP/API | активна |
| CloudZero | FinOps | только SaaS | проприетарный | provider billing/usage API (pull) | OpenAI, Anthropic, Bedrock, +30 источников | (managed) | web-дашборд/API | активна |

---

## 4. Применимость фич к aiCostTracker — анализ и рекомендации

Ниже — только те возможности, которых у `aiCostTracker` сейчас нет или нет
полностью, с явной рекомендацией. Уже реализованные и уже запланированные
вещи (тренды/чарт — Этап 6, алерты — Этап 7, `--format=json` — Этап 8,
точность денег — Этап 9, per-provider дельты `--compare` и CSV-экспорт —
детально расписаны аудитом code↔docs в `docs/POST_MVP_PLAN.md`) не
дублируются здесь — только перекрёстная ссылка там, где исследование
подтверждает их ценность.

### 4.1 Кандидаты — в POST_MVP-бэклог

**Явное обоснование выбора net/http для OpenAI в TECHNICAL_PLAN.** Официальный
Go SDK OpenAI (`github.com/openai/openai-go`) **покрывает** usage/cost admin
API (`Admin.Organization.Usage.Costs` и категории usage) — значит, для OpenAI
ручная реализация уже не техническая необходимость, а сознательный выбор ради
единообразия порта `ProviderUsageSource` и общего паттерна retry+backoff по
всем трём провайдерам. **Рекомендация: да, взять** — не изменение кода, а
однострочное уточнение в `docs/TECHNICAL_PLAN.md §2`/§4 (сейчас там сказано
только «HTTP — ручной net/http без SDK» без разбивки по провайдерам); стоит
явно написать, что для Anthropic и OpenRouter это вынужденно (SDK не
покрывает usage/cost), а для OpenAI — осознанный выбор ради консистентности.
Сложность: тривиальная (правка документации).

**Prometheus-текстовый экспорт (`--format=prometheus`).** У `openusage`
(ближайшего по языку и духу конкурента) есть встроенный экспорт в Prometheus
— и это можно сделать **без сервера**: не поднимать HTTP-эндпоинт для scrape,
а по аналогии с `report.JSON`/предложенным `report.CSV` вывести в stdout
текстовый формат Prometheus exposition (`# TYPE aicost_cost_usd_total
counter` + строки `metric{provider="...",model="..."} value`), который
пользователь сам перенаправит в файл для `node_exporter`'овского
textfile collector или заберёт cron-скриптом. Соответствует духу
«локальный CLI без сервера»: это ещё один статический формат вывода, третий
рядом с JSON/CSV, а не постоянно слушающий порт. **Рекомендация: кандидат в
`docs/POST_MVP_PLAN.md` «Дальше»** — низкий риск, реализация симметрична уже
запланированному CSV (`internal/cli/report.go`'s `validateFormat` + новая
функция рендера), но нужно явное решение по набору меток (`provider`,
`model`, `day`) и имени метрик до старта работы — не начинать без этого.

### 4.2 Под вопросом — обсудить с пользователем перед взятием в план

**Бюджетные алерты через webhook (email/Slack).** `LLMeter` (Pro-тариф) и
Datadog LLM Observability шлют уведомления о превышении бюджета во внешние
каналы. У `aiCostTracker` уже есть `--fail-on-alert` (ненулевой exit code),
который штатно композируется с любым внешним нотификатором через shell/cron
(`aicost report --fail-on-alert || curl -X POST $SLACK_WEBHOOK ...`) без
единой строки нового кода. Добавление builtin webhook означало бы: новое
поле конфига с URL (потенциальный секрет), сетевой вызов из "read-only"
команды `report`, обработку сетевых ошибок нотификации отдельно от основной
логики. **Рекомендация: не брать по умолчанию** — существующая
exit-code-композиция уже закрывает сценарий средствами shell без усложнения
кода; если пользователь всё же хочет builtin-интеграцию — решение зависит от
того, готов ли он расширить зону ответственности CLI за пределы "прочитать
usage API и показать таблицу". Отмечено как открытый вопрос, не решение.

**Per-project/per-team разбивка расходов.** Vantage/CloudZero/LiteLLM
показывают spend с разбивкой по project/team — это востребованная фича у
конкурентов. Формально OpenAI организует API-ключи по `project_id`, и в
Admin API это в принципе может быть доступно как параметр
группировки/фильтра — **но точные параметры Usage/Costs API (поддерживают ли
`group_by=project_id`) не удалось перепроверить инструментом в этой сессии**
(официальные страницы `developers.openai.com` отдают HTTP 403 при попытке
автоматического fetch) — нужна ручная проверка документации перед оценкой
трудозатрат. У Anthropic похожего понятия «проекта» на уровне admin-ключа
организации нет (только workspaces, что не то же самое), у OpenRouter —
тоже нет. **Рекомендация: под вопросом, требует уточнения фактов** (сначала
вручную сверить OpenAI Admin API на предмет `project_id`-группировки), и
только затем решать, стоит ли частичная (только-OpenAI) реализация
усложнения ради одного из трёх провайдеров.

### 4.3 Не подходит — явно не взять

**Прайс-таблица моделей своими силами (как LiteLLM/Portkey/ccusage).** Все
три текущих провайдера (`Anthropic`, `OpenAI`, `OpenRouter`) в
`aiCostTracker` уже возвращают **готовую стоимость в USD** прямо в usage/cost
API-ответе (см. `docs/TECHNICAL_PLAN.md §4`) — в отличие от инструментов,
парсящих локальные логи (`ccusage`, `ccost`, `phuryn/claude-usage`), которым
*приходится* держать и поддерживать собственную прайс-карту моделей, потому
что у них нет доступа к биллингу. Заводить такую таблицу в `aiCostTracker`
означало бы **понизить**, а не повысить точность (собственная прайс-карта
устаревает и не учитывает скидки/tier-цены, которые провайдер уже применил
при расчёте `cost_report`). **Рекомендация: не подходит — текущий дизайн уже
превосходит эту фичу конкурентов, ничего добавлять не нужно.** (Стоит явно
отметить это как осознанное архитектурное преимущество в `TECHNICAL_PLAN.md`,
если там ещё не зафиксировано.)

**SDK-инструментация / прокси-режим сбора данных.** Ключевая архитектурная
идея почти всех продуктов §2.2–2.3 (перехват трафика через прокси или
SDK-обёртку в коде приложения) прямо противоречит инварианту `aiCostTracker`
"без сервера, читаем провайдерский usage API постфактум". Не подходит ни при
каких обстоятельствах без полного пересмотра идентичности проекта.

**Evals / LLM-as-judge / guardrails / prompt-management / A-B тестирование
промптов.** Это функциональность класса Langfuse/Phoenix/Braintrust/LangSmith/
W&B Weave — направлена на качество и надёжность самих LLM-вызовов приложения,
а не на учёт расходов. Полностью вне предметной области "cost tracker".

**Billing/invoicing/revenue-recognition, мультиоблачный FinOps (K8s,
Snowflake и т.п.), unit-economics/P&L.** Функциональность OpenMeter/
Vantage/CloudZero, рассчитанная на компании, монетизирующие AI-продукт для
внешних клиентов. У pet-проекта нет бизнес-модели, которую нужно было бы
финансово атрибутировать — по определению не подходит (сам проект прямо
заявляет "не бизнес — экономическая целесообразность не требуется").

**Любая исходящая телеметрия продукта (даже отключаемая по умолчанию).**
Arize Phoenix даёт пример "правильно" сделанной, но всё равно нежелательной
для нас практики — `PHOENIX_TELEMETRY_ENABLED=false`. `aiCostTracker`
принципиально не имеет телеметрии вовсе (см. корневой `CLAUDE.md`) —
добавлять даже opt-out телеметрию не подходит.

### 4.4 Уже подтверждённые архитектурные решения (изменений не требуют)

- **Идемпотентный `Save` (upsert)** — тот же принцип, что у OpenMeter'овской
  дедупликации ingestion; подтверждено конкурентным паттерном, менять
  нечего.
- **SQLite для локальной истории трендов** — подтверждено параллелью с Arize
  Phoenix (SQLite-режим для маленького деплоя) и W&B Weave (monitors —
  тренды во времени); текущий выбор адекватен масштабу задачи.
- **Порт `ProviderUsageSource`** как единая нормализация разнородных
  провайдеров — прямая параллель с ролью, которую у CloudZero играет
  внутренняя нормализация мультиоблачных источников в одну модель; архитектура
  подтверждена конкурентным паттерном на другом масштабе.

---

## 5. Официальные SDK провайдеров и Claude Skills

### 5.1 Официальные SDK провайдеров — применимость к aiCostTracker

**Вывод: текущее решение проекта (ручной `net/http` без SDK) для Anthropic и
OpenRouter полностью обосновано технически; для OpenAI — обосновано выбором
консистентности, а не отсутствием альтернативы.**

**Anthropic — официальный Go SDK НЕ покрывает usage/cost API.**
`github.com/anthropics/anthropic-sdk-go` не оборачивает
`/v1/organizations/usage_report/messages` и `/v1/organizations/cost_report`.
Подтверждено по исходникам: в `api.md` SDK — 0 упоминаний `usage_report`/
`cost_report`; раздел `## Organization` покрывает только `Organization.Get`,
API-ключи, workspaces, service accounts, rate limits, users, invites — но не
usage/cost-отчёты. Официальная документация Usage & Cost Admin API даёт
примеры только на cURL, без SDK-сниппетов. **Ручной HTTP-клиент —
единственный путь**, что полностью подтверждает текущую архитектуру.
Уточнение по ключам (совпадает с решением проекта «admin/org-ключ ≠ ключ
модели»): нужен Admin API key (`sk-ant-admin01-...`), OAuth-токен со scope
`org:admin`, либо unscoped personal/service-account ключ — обычный
workspace-ключ не подходит.

**OpenAI — официальный Go SDK ПОКРЫВАЕТ usage/cost API.**
`github.com/openai/openai-go` экспортирует `AdminOrganizationUsageService`
(`client.Admin.Organization.Usage`) с методами `Costs(...)` (дневной разбор
затрат, `GET /organization/costs`) и категориями usage (`Completions`,
`Embeddings`, `Images`, `Moderations`, `AudioSpeeches`,
`AudioTranscriptions`, `VectorStores` и др.). Значит, ручной `net/http` для
OpenAI **не техническая необходимость** — это сознательный выбор ради
единого порта `ProviderUsageSource` и общего паттерна retry+backoff по всем
провайдерам (см. §4.1 — стоит явно так и зафиксировать в
`TECHNICAL_PLAN.md`).

**OpenRouter — официального Go SDK нет вовсе.** Analytics/usage доступны
через REST (`GET /api/v1/activity`, `POST /api/v1/analytics/query`, `GET
/api/v1/analytics/meta`, `GET /api/v1/auth/key`), management key. Официальный
SDK у OpenRouter есть только на TypeScript. Ручной `net/http` в
`internal/provider/openrouter.go` обоснован так же, как для Anthropic.

| Провайдер | Официальный Go SDK покрывает usage/cost? | Вывод для ручного net/http |
|---|---|---|
| Anthropic | Нет (не обёрнуто в SDK) | Вынужденно и обоснованно |
| OpenAI | Да (`Admin.Organization.Usage.*`, включая `.Costs`) | Сознательный выбор ради консистентности, не необходимость |
| OpenRouter | Официального Go SDK нет вовсе | Обоснованно |

### 5.2 Официальные Claude Skills для разработки проекта

Источник истины — `anthropics/skills` (skills авторства Anthropic) и
`anthropics/claude-plugins-official` (Anthropic-managed marketplace; часть
плагинов там — от сторонних вендоров, не Anthropic; как «официальные
Anthropic» ниже засчитаны только позиции с `author.name == "Anthropic"`,
подтверждено по `marketplace.json`).

**Уже используется в проекте** (не дублировать): project-skills
`code-docs-audit`, `go-cost-tracker-dev`; системные `code-review`,
`deep-research`, `verify`, `run`, `claude-api`, `skill-creator`.

**Дополнительные официальные (Anthropic) — рекомендуемые:**

- **`gopls-lsp`** (plugin) — интеграция Go language server `gopls`: code
  intelligence, рефакторинг, анализ `.go`-файлов. Единственный Go-специфичный
  официальный компонент — самый релевантный из недостающих для проекта на
  Go. Требует `go install golang.org/x/tools/gopls@latest`.
- **`pr-review-toolkit`** (plugin) — набор ревью-агентов по
  comments/tests/error-handling/type-design/quality; шире, чем один
  `code-review`.
- **`feature-dev`** (plugin) — workflow разработки фичи (research
  кодовой базы → дизайн архитектуры → проверка качества); ложится на
  dev-workflow проекта (планирование → кодинг → ревью).
- **`claude-md-management`** (plugin) — аудит качества CLAUDE.md, захват
  знаний сессии, поддержание project memory; совпадает с практикой
  чекпоинтов проекта.
- **`commit-commands`** (plugin) — git-workflow команды (commit/push/PR);
  частично пересекается с ручным процессом шага 5 dev-workflow — оценить
  перед подключением, чтобы не задваивать.
- **`code-simplifier`** / **`security-guidance`** / **`claude-security`**
  (plugins) — возможно пересекаются с уже используемыми в среде `simplify` и
  `security-review` — **перед установкой сверить, не дублируют ли**
  функционал, а не подключать вслепую.

**Чего не нашли (важно, чтобы не выдумать):**
- Официального skill под «управление Go-зависимостями» (go.mod/go.sum) —
  не найден.
- Официального Go/CLI-специфичного тестового skill — не найден;
  `webapp-testing` (skill, `anthropics/skills`) существует официально, но
  ориентирован на веб-приложения и слабо применим к table-driven+golden
  тестам `aicost`. Если такой skill нужен — это кандидат на собственный
  project-skill, а не на готовый официальный.
- Официального skill под «исследование рынка» — не найден; покрытие даёт
  уже используемый `deep-research`.

---

## 6. Источники

**Self-hosted CLI/TUI:**
- https://github.com/janekbaraniewski/openusage · https://openusage.sh/
- https://github.com/ryoppippi/ccusage · https://www.npmjs.com/package/ccusage · https://ccusage.com/
- https://github.com/brian-mwirigi/aitoken-cli · https://www.brianmunene.me/blog/2-track-ai-api-costs
- https://github.com/carlosarraes/ccost · https://crates.io/crates/ccost
- https://github.com/phuryn/claude-usage
- https://github.com/658jjh/claude-usage-tracker
- https://github.com/mhd-medfa/openrouter-usage-monitor
- https://github.com/amedinat/LLMeter · https://leanlm.ai/blog/llm-cost-tracking-tools
- https://github.com/hassanazam/claude-cost · https://github.com/shreyasgm/anthropic-usage-skill
- claude-cost-cli (выбыл): https://www.npmjs.com/package/claude-cost-cli · https://www.codaone.ai/skills/claude-cost-cli/

**LLM-шлюзы/прокси:**
- https://github.com/BerriAI/litellm · https://www.litellm.ai/pricing · https://www.litellm.ai/oss · https://docs.litellm.ai/docs/proxy/cost_tracking
- https://github.com/portkey-ai/gateway · https://portkey.ai/docs/product/open-source
- https://www.cloudflare.com/products/ai-gateway/ · https://developers.cloudflare.com/ai-gateway/observability/analytics/ · https://developers.cloudflare.com/ai-gateway/reference/pricing/ · https://developers.cloudflare.com/ai-gateway/features/spend-limits/
- https://www.truefoundry.com/ai-gateway · https://www.truefoundry.com/blog/best-ai-gateway
- https://www.truefoundry.com/blog/kong-gateway-pricing-architecture-an-analysis-for-ai-teams-2026-edition · https://api7.ai/blog/kong-konnect-pricing
- https://github.com/higress-group/higress · https://higress.ai/en/ · https://higress.ai/en/docs/ai/scene-guide/token-management/
- https://openrouter.ai/docs/faq · https://www.truefoundry.com/blog/openrouter-pricing
- https://github.com/helicone/helicone · https://docs.helicone.ai/guides/cookbooks/cost-tracking

**LLM observability / FinOps:**
- https://github.com/langfuse/langfuse · https://langfuse.com/self-hosting · https://langfuse.com/handbook/chapters/open-source
- https://www.mintlify.com/blog/mintlify-acquires-helicone
- https://github.com/Arize-ai/phoenix · https://www.agenticwire.news/article/langfuse-vs-arize-phoenix
- https://www.langchain.com/pricing-langsmith · https://docs.langchain.com/langsmith/architectural-overview
- https://github.com/traceloop/openllmetry · https://www.traceloop.com/openllmetry
- https://www.braintrust.dev/articles/best-self-hosted-ai-evals-tools-2026
- https://wandb.ai/site/weave/ · https://docs.wandb.ai/weave/guides/evaluation/guardrails_and_monitors
- https://futureagi.com/blog/best-keywords-ai-alternatives-2026/
- https://www.promptlayer.com/blog/best-prompt-management-tools-2026-field-guide/
- https://cubeapm.com/faqs/datadog-llm-observability/ · https://ecorpit.com/datadog-llm-observability-pricing-cap-costs-2026/
- https://openmeter.io/ · https://github.com/openmeterio/openmeter · https://openmeter.io/blog/openmeter-is-joining-kong · https://openmeter.io/use-cases/ai
- https://www.vantage.sh/blog/finops-for-ai-token-costs · https://www.vantage.sh/blog/best-ai-cost-management-tools
- https://www.cloudzero.com/blog/cloudzero-anthropic/ · https://www.cloudzero.com/blog/ai-cost-observability/

**Официальные SDK и Claude Skills:**
- https://github.com/anthropics/anthropic-sdk-go (файлы `api.md`, `usage_test.go`)
- https://platform.claude.com/docs/en/manage-claude/usage-cost-api
- https://platform.claude.com/docs/en/api/admin-api/usage-cost/get-messages-usage-report
- https://platform.claude.com/docs/en/api/admin-api/usage-cost/get-cost-report
- https://github.com/openai/openai-go (файл `adminorganizationusage.go`)
- https://developers.openai.com/api/docs/guides/admin-apis
- https://openrouter.ai/docs/api/api-reference/analytics/get-user-activity
- https://openrouter.ai/docs/client-sdks/typescript/api-reference/analytics
- https://github.com/anthropics/skills
- https://github.com/anthropics/claude-plugins-official (файл `.claude-plugin/marketplace.json`)
- https://github.com/anthropics/claude-plugins-official/tree/main/plugins/gopls-lsp
- https://code.claude.com/docs/en/discover-plugins

**Неподтверждённые/противоречивые факты (перечислены явно в тексте):**
точные даты последних релизов Langfuse/Phoenix/Traceloop/LiteLLM/Portkey/
Higress через README не зафиксированы; лицензионный статус PromptLayer
противоречив; денежный (USD) cost-трекинг Higress не подтверждён; поддержка
группировки по `project_id` в OpenAI Usage/Costs API не перепроверена
инструментом в этой сессии (страницы `developers.openai.com` отдают 403 при
автоматическом fetch).
