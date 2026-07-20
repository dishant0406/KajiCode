package main

import (
	"os"

	"github.com/dishant0406/KajiCode/internal/sandbox"
)

func main() {
	os.Exit(sandbox.RunWindowsSandboxSetup(os.Args[1:], os.Stderr))
}
