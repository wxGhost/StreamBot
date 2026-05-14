module streamer-bot

go 1.22

require (
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
	github.com/jackc/pgx/v5 v5.7.2
	github.com/joho/godotenv v1.5.1
	golang.org/x/time v0.9.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.32.0 // indirect
	golang.org/x/sync v0.10.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)

// Mirror golang.org/x packages via GitHub so they resolve without golang.org access
replace (
	golang.org/x/crypto => github.com/golang/crypto v0.32.0
	golang.org/x/sync   => github.com/golang/sync v0.10.0
	golang.org/x/text   => github.com/golang/text v0.21.0
	golang.org/x/time   => github.com/golang/time v0.9.0
	gopkg.in/yaml.v3    => github.com/go-yaml/yaml/v3 v3.0.1
)
