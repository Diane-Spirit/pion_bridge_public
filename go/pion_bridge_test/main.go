package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	test := flag.Bool("t", false, "Enable test mode")
	flag.Parse()

	if *test {
		fmt.Println("Test mode enabled.")
	}

	StartBridge(test)
	fmt.Println("Bridge running, use CTRL+C to stop.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	StopBridge()
	fmt.Println("Bridge stopped.")
}
