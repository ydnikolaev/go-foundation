package alert_test

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/ydnikolaev/go-foundation/units/alert"
)

// An Alerter that sends through Telegram, fed by a slog tee. It has no Output line, so `go test` compiles it
// without running it (running it would call the Bot API).
func Example_telegram() {
	notifier, err := alert.NewTelegram(alert.TelegramConfig{
		Token:  os.Getenv("ALERT_TELEGRAM_TOKEN"),
		ChatID: os.Getenv("ALERT_TELEGRAM_CHAT_ID"),
	})
	if err != nil {
		log.Fatal(err)
	}
	base := slog.NewJSONHandler(os.Stderr, nil)
	alerter := alert.New(notifier, alert.Config{
		Prefix:  "myservice alert: ", // the separator space is yours
		Colour:  "blue",
		Release: "v1.2.3",
		Log:     slog.New(base), // the base logger, never the one fronted by the tee
	})
	logger := slog.New(alert.NewHandler(base, alerter))

	logger.Error("request failed", "route", "GET /v1/items", "status", 500, "code", "internal")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := alerter.Close(ctx); err != nil {
		log.Print(err)
	}
}
