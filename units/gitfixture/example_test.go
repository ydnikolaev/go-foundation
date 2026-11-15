package gitfixture_test

import (
	"fmt"
	"strings"

	"github.com/ydnikolaev/go-foundation/units/gitfixture"
)

// Example shows the argument list a fixture helper passes to exec.Command: the maintenance flags first, then the
// subcommand.
func Example() {
	fmt.Println(strings.Join(gitfixture.Args("commit", "-m", "seed"), " "))
	// Output: -c gc.auto=0 -c maintenance.auto=false commit -m seed
}
