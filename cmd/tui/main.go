package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	fmt.Println("[TUI] Starting MangaHub Terminal UI (connecting to Backend on ports 8080, 9090, 9091)...")
	fmt.Println("[TUI] Press Enter or Ctrl+C to exit.")
	_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
}
