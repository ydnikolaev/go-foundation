package safehttp_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/ydnikolaev/go-foundation/units/safehttp"
)

func Example() {
	c := safehttp.New(safehttp.Options{})

	// An address inside the host's own network is refused before any connection is made.
	_, err := c.Get(context.Background(), "http://127.0.0.1:8080/admin")
	fmt.Println(errors.Is(err, safehttp.ErrBlocked))

	// The check a caller can reuse on its own: is an address a public one?
	fmt.Println(safehttp.Blocked(netip.MustParseAddr("10.0.0.1")), safehttp.Blocked(netip.MustParseAddr("93.184.216.34")))
	// Output:
	// true
	// true false
}
