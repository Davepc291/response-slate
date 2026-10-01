package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	deps := Deps{
		Stdout:   os.Stdout,
		Generate: webpush.GenerateVAPIDKeys,
	}
	if err := Run(deps); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
