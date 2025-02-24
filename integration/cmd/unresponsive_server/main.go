package main

import (
	"bufio"
	"os"
)

func main() {
	reader := bufio.NewReader(os.Stdin)
	go func() {
		for {
			buf := make([]byte, 1024)
			reader.Read(buf)
		}
	}()

	select {}
}
