package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	reader := bufio.NewReader(os.Stdin)
	go func() {
		for {
			buf := make([]byte, 1024)
			reader.Read(buf)
		}
	}()

	bytes, err := os.ReadFile("./testdata/server_messages.json")
	if err != nil {
		panic(err)
	}

	var messages []string
	err = json.Unmarshal(bytes, &messages)
	if err != nil {
		panic(err)
	}

	// Wait for the client to send all of its messages.
	time.Sleep(3 * time.Second)

	for _, message := range messages {
		_, err = os.Stdout.Write([]byte(message))
		if err != nil {
			panic(err)
		}

		time.Sleep(100 * time.Millisecond)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)
	<-done
}
