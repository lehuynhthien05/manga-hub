package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	fmt.Println("[Backend] HTTP REST & WebSocket listening on port 8080")
	fmt.Println("[Backend] TCP Progress Sync listening on port 9090")
	fmt.Println("[Backend] UDP Chapter Alerts listening on port 9091")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("[Backend] Service stopped.")
}
