.PHONY: deps setup deploy lint

## Download all Go dependencies (run first)
deps:
	go mod tidy

## Register webhook with Telegram (run once after deploy)
setup:
	go run ./cmd/setup

## Deploy to Vercel production
deploy:
	vercel --prod

## Run go vet + staticcheck
lint:
	go vet ./...
	@which staticcheck > /dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed, skipping"
