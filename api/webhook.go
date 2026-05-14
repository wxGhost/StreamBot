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

// ─── Singleton ────────────────────────────────────────────────────────────────

var (
	initOnce  sync.Once
	globalBot *bot.Bot
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
			log.Printf("ERROR db.New: %v", err)
			initErr = err
			return
		}

		b, err := bot.New(token, database, streamerID, channelID)
		if err != nil {
			log.Printf("ERROR bot.New: %v", err)
			initErr = err
			return
		}
		globalBot = b
		log.Printf("INFO bot initialized ok")
	})
}

// ─── Rate limiter ─────────────────────────────────────────────────────────────

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
	l := rate.NewLimiter(rate.Every(2*time.Second), 10)
	limiters[ip] = l
	return l
}

// ─── Handler ──────────────────────────────────────────────────────────────────

func Handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("INFO request: method=%s path=%s", r.Method, r.URL.Path)

	initialize()
	if initErr != nil {
		log.Printf("ERROR init failed: %v", initErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Accept both POST (Telegram) and GET (health check / webhook verify)
	if r.Method == http.MethodGet {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	if r.Method != http.MethodPost {
		log.Printf("WARN unexpected method: %s", r.Method)
		w.WriteHeader(http.StatusOK) // return 200 anyway to avoid Telegram retries
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Verify secret token
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret != "" && r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != secret {
		log.Printf("WARN invalid secret token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Rate limit
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !getLimiter(ip).Allow() {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	// Read body
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("WARN read body: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	log.Printf("INFO body len=%d", len(body))

	// Parse update
	var update tgbotapi.Update
	if err := json.Unmarshal(body, &update); err != nil {
		log.Printf("WARN unmarshal: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Handle
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	globalBot.HandleUpdate(ctx, update)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ─── Util ─────────────────────────────────────────────────────────────────────

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing env var: %s", key)
	}
	return v
}

func mustEnvInt64(key string) int64 {
	v := mustEnv(key)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("env var %s not integer: %v", key, err)
	}
	return n
}
