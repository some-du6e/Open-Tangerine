//go:build !windows

package main

import (
	"fmt"
	"os"
)

// Toss is a Windows app; other platforms only build so the shared code can be tested.
func main() {
	fmt.Fprintln(os.Stderr, "Toss runs on Windows.")
	os.Exit(1)
}
