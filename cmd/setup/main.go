// cmd/setup/main.go
// Run once after deploying to Vercel to register the webhook:
//
//	BOT_TOKEN=xxx WEBHOOK_SECRET=yyy APP_URL=https://your-project.vercel.app go run ./cmd/setup
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	token := os.Getenv("BOT_TOKEN")
	secret := os.Getenv("WEBHOOK_SECRET")
	appURL := os.Getenv("APP_URL")

	if token == "" || appURL == "" {
		log.Fatal("BOT_TOKEN and APP_URL are required")
	}

	if err := setupWebhook(appURL, token, secret); err != nil {
		log.Fatalf("SetupWebhook: %v", err)
	}
	fmt.Printf("✅ Webhook registered: %s/api/webhook\n", appURL)
}

func setupWebhook(appURL, token, secret string) error {
	webhookURL := appURL + "/api/webhook"
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", token)

	payload := map[string]interface{}{
		"url":             webhookURL,
		"max_connections": 5,
	}
	if secret != "" {
		payload["secret_token"] = secret
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("post setWebhook: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram error: %s", result.Description)
	}
	return nil
}
