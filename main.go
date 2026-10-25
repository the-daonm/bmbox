package main

import (
	"os"

	"github.com/the-daonm/bmbox/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
