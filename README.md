# Streamer Bot

Telegram бот для сбора предложений от зрителей стрима.

---

## Стек
| Компонент | Технология |
|---|---|
| Язык | Go 1.21 |
| Хостинг | Vercel (Serverless Functions) |
| База данных | Neon PostgreSQL (бесплатный тариф) |
| Telegram API | go-telegram-bot-api v5 |

---

## Быстрый старт

### 1. Создать бота
1. Открой [@BotFather](https://t.me/BotFather) → `/newbot`
2. Сохрани `BOT_TOKEN`
3. Узнай свой `STREAMER_CHAT_ID` через [@userinfobot](https://t.me/userinfobot)

### 2. Настроить канал
1. Добавь бота в свой канал как администратора (права: публикация сообщений)
2. Узнай `CHANNEL_ID`:
   - Перешли любое сообщение из канала боту [@JsonDumpBot](https://t.me/JsonDumpBot)
   - Найди поле `forward_from_chat.id` — это и есть ID канала (начинается с `-100`)

### 3. База данных (Neon)
1. Зарегистрируйся на [neon.tech](https://neon.tech) — бесплатно
2. Создай проект → скопируй строку подключения (`DATABASE_URL`)
3. Открой **SQL Editor** в Neon и выполни содержимое файла `migrations/001_init.sql`

### 4. Деплой на Vercel
1. [Установи Vercel CLI](https://vercel.com/docs/cli): `npm i -g vercel`
2. Войди: `vercel login`
3. Задай переменные окружения в Vercel Dashboard → Settings → Environment Variables:
   ```
   BOT_TOKEN
   STREAMER_CHAT_ID
   CHANNEL_ID
   DATABASE_URL
   WEBHOOK_SECRET    (любая случайная строка, например из: openssl rand -hex 32)
   APP_URL           (твой URL на Vercel, напр. https://my-bot.vercel.app)
   ```
4. Задеплой:
   ```bash
   vercel --prod
   ```

### 5. Зарегистрировать вебхук
После деплоя выполни **один раз**:
```bash
# Локально (нужны .env файл или переменные окружения)
cp .env.example .env
# Заполни .env реальными значениями, затем:
go run ./cmd/setup
```

Или из Vercel CLI:
```bash
BOT_TOKEN=xxx WEBHOOK_SECRET=yyy APP_URL=https://your-project.vercel.app go run ./cmd/setup
```

---

## Использование

### Пользователи
После перехода по ссылке на бота пользователь видит клавиатуру с кнопками:

| Кнопка | Действие |
|---|---|
| 🎮 Предложить игру | Предложение помечается тегом `#Игра` |
| 💡 Предложения на стрим | Предложение помечается тегом `#Предложение` |
| 🕵️ Анонимно | Предложение помечается `#Анонимно`, имя не передаётся |

### Стример (личный чат с ботом)
Каждое предложение приходит с кнопками:

| Кнопка | Действие |
|---|---|
| 📢 В канал | Публикует в канал с кнопками 👍/👎 |
| ⭐ В топ | Добавляет в список топа |
| 📦 В архив | Перемещает в архив |
| 🗑 Удалить | Удаляет из БД и чата |
| ℹ️ Информация | Показывает тег, ID автора, время, номер |

**Команды стримера:**
```
/archive  — список архивных предложений
/top      — топ предложений по лайкам
/stats    — общая статистика
/help     — справка
```

---

## Безопасность
- Вебхук защищён заголовком `X-Telegram-Bot-Api-Secret-Token`
- Rate limiting: не более 10 запросов в burst / ~30 в минуту с одного IP
- Размер тела запроса ограничен 512 КБ
- Длина предложения ограничена 4000 символов
- Все строки экранируются через `html.EscapeString` перед вставкой в HTML-сообщения
- БД использует параметризованные запросы (защита от SQL-инъекций)

---

## Локальная разработка

Для тестирования без деплоя используй [ngrok](https://ngrok.com):

```bash
# Запусти локальный сервер
go run ./cmd/local  # (если добавишь локальный http-сервер, см. ниже)

# В другом терминале
ngrok http 8080

# Зарегистрируй вебхук на ngrok URL
APP_URL=https://xxxx.ngrok.io go run ./cmd/setup
```

---

## Структура проекта
```
.
├── api/
│   └── webhook.go          ← Точка входа Vercel + rate limiter + webhook verify
├── cmd/
│   └── setup/
│       └── main.go         ← Утилита регистрации вебхука
├── internal/
│   ├── bot/
│   │   ├── bot.go          ← Основная логика бота
│   │   ├── callback.go     ← Кодирование callback-данных
│   │   ├── keyboards.go    ← Клавиатуры (Reply + Inline)
│   │   └── state.go        ← FSM состояния пользователей
│   ├── db/
│   │   └── postgres.go     ← Все запросы к БД
│   └── models/
│       └── proposal.go     ← Типы данных
├── migrations/
│   └── 001_init.sql        ← SQL схема (запустить один раз в Neon)
├── .env.example
├── go.mod
├── vercel.json
└── README.md
```
