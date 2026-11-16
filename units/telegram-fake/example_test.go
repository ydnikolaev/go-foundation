package telegramfake_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ydnikolaev/go-foundation/units/telegram-fake"
)

func Example() {
	s := telegramfake.New(`{"ok":true,"result":true}`)
	defer s.Close()

	// The code under test would point its Bot API client at s.URL; a plain POST stands in for it here.
	resp, err := http.Post(s.URL+"/bot1:x/sendMessage", "application/json", strings.NewReader(`{"chat_id":7}`))
	if err != nil {
		fmt.Println(err)
		return
	}
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	fmt.Println(string(answer))
	for _, c := range s.Calls() {
		fmt.Println(c.Path, string(c.Body))
	}
	// Output:
	// {"ok":true,"result":true}
	// /bot1:x/sendMessage {"chat_id":7}
}
