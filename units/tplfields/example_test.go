package tplfields_test

import (
	"errors"
	"fmt"

	"github.com/ydnikolaev/go-foundation/units/tplfields"
)

func Example() {
	fields, err := tplfields.Fields(`Hi {{.user.name}}{{if .plan}}, plan {{with .plan}}{{.title}}{{end}}{{end}}`, nil)
	fmt.Println(fields, err)

	// What it cannot follow is refused with the line, never guessed.
	_, err = tplfields.Fields("one\n{{range .items}}{{.}}{{end}}", nil)
	var fe *tplfields.Error
	if errors.As(err, &fe) {
		fmt.Println(fe.Line, fe.Reason)
	}
	// Output:
	// [plan plan.title user.name] <nil>
	// 2 unsupported
}
