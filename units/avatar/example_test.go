package avatar_test

import (
	"fmt"
	"math/rand/v2"

	"github.com/ydnikolaev/go-foundation/units/avatar"
)

// Example draws a key for a new user, stores it, and serves its picture later.
func Example() {
	r := rand.New(rand.NewPCG(1, 2)) // the caller owns the source; a test fixes it
	key := avatar.Random(r)

	fmt.Println(avatar.IsValid(key), len(avatar.Keys()))
	svg := avatar.SVG(key) // same bytes for the same key, always
	fmt.Println(len(svg) > 0, string(svg[:4]))
	fmt.Println(avatar.SVG("no-such-key") == nil)
	// Output:
	// true 16
	// true <svg
	// true
}
