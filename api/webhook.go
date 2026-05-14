package handler

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/time/rate"

	"streamer-bot/bot"
	"streamer-bot/db"
)

// ─── Singleton init (reused across warm invocations on Vercel) ────────────────

var (
	initOnce  sync.Once
	globalBot *bot.Bot
	globalDB  *db.DB
	initErr   error
)

func initialize() {
	initOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		token := mustEnv("BOT_TOKEN")
		dsn := mustEnv("DATABASE_URL")
		streamerID := mustEnvInt64("STREAMER_CHAT_ID")
		channelID := mustEnvInt64("CHANNEL_ID")

		database, err := db.New(ctx, dsn)
		if err != nil {
			initErr = err
			return
		}
		globalDB = database

		b, err := bot.New(token, database, streamerID, channelID)
		if err != nil {
			initErr = err
			return
		}
		globalBot = b
	})
}

// ─── Rate limiter (per IP) ────────────────────────────────────────────────────

var (
	limiters   = make(map[string]*rate.Limiter)
	limitersMu sync.Mutex
)

func getLimiter(ip string) *rate.Limiter {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	if l, ok := limiters[ip]; ok {
		return l
	}
	// 10 burst, refills at 1 token per 2s (~30/min sustained)
	l := rate.NewLimiter(rate.Every(2*time.Second), 10)
	limiters[ip] = l
	return l
}

// ─── Vercel entry point ───────────────────────────────────────────────────────

// Handler is called by Vercel for every incoming HTTP request to /api/webhook.
func Handler(w http.ResponseWriter, r *http.Request) {
	initialize()
	if initErr != nil {
		log.Printf("FATAL init error: %v", initErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Only accept POST (Telegram only sends POST)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify Telegram secret token header
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret != "" && r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != secret {
		log.Printf("WARN invalid webhook secret from %s", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Per-IP rate limiting
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !getLimiter(ip).Allow() {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	// Cap request body size
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Parse Telegram Update
	var update tgbotapi.Update
	if err := json.Unmarshal(body, &update); err != nil {
		log.Printf("WARN unmarshal update: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Process with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	globalBot.HandleUpdate(ctx, update)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ─── Utility ──────────────────────────────────────────────────────────────────

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var: %s", key)
	}
	return v
}

func mustEnvInt64(key string) int64 {
	v := mustEnv(key)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("env var %s must be an integer: %v", key, err)
	}
	return n
}
