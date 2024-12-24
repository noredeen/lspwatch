package internal

import (
	"bufio"
	"fmt"
	"io"
	"os"
  "time"
	"strings"
  "strconv"
  "encoding/json"
)

type LSPRequestMessage struct {
    Jsonrpc string
    Id string
    Method string
    Params *json.RawMessage
}

type LSPResponseMessage struct {
    Jsonrpc string
    Id string
    Result *json.RawMessage
    Error *struct {
        Code int
        Message string
        Data *json.RawMessage
    }
}

func ListenServer() {

}

func ListenClient(langServPipe io.WriteCloser) {
    reader := bufio.NewReader(os.Stdin)
    rawLspRequest := []byte{}
    headers := make(map[string]string)

    for {
        line, _, err := reader.ReadLine()
        if err != nil {
            if err.Error() == "EOF" {
                fmt.Printf("Incomplete header section\n")
                return
            }
            fmt.Printf("Error reading header line: %v\n", err)
            return
        }

        if len(line) == 1 && line[0] == '\r' {
            break
        }

        header := strings.Split(string(line), ":")
        key := header[0]
        value := strings.Trim(header[1], "\r ")

        headers[key] = value

        rawLspRequest = append(rawLspRequest, line...)
        rawLspRequest = append(rawLspRequest, '\n')
    }

    contentLength, ok := headers["Content-Length"]
    if !ok {
        fmt.Printf("Missing Content-Length header in LSP request\n")
        return
    }

    contentByteCnt, err := strconv.Atoi(contentLength)
    if err != nil {
        fmt.Printf("Content-Length value is not an integer\n")
        return
    }

    requestContent := []byte{}
    for i := 0; i < contentByteCnt; i++ {
        ch, err := reader.ReadByte()
        if err != nil {
            fmt.Printf("Error reading content byte: %v\n", err)
            return
        }

        requestContent = append(requestContent, ch)
    }

    rawLspRequest = append(rawLspRequest, requestContent...)

    // Capture request ID
    var lspRequest LSPRequestMessage
    err = json.Unmarshal(requestContent, &lspRequest)
    if err != nil {
        fmt.Printf("Missing request ID\n")
        return
    }

    // Capture initial time
    startTime := time.Now()

    // Store request

    // Forward request
    langServPipe.Write(rawLspRequest)

    // TODO: go processRequest(...)
}
