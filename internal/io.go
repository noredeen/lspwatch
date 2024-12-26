package internal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/textproto"
	"strconv"
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

func readLSPMessage(
    reader *bufio.Reader,
    jsonBody interface{},
) (textproto.MIMEHeader, []byte, error) {
    rawLspRequest := []byte{}
    tp := textproto.NewReader(reader)
    headers, err := tp.ReadMIMEHeader()

    if err != nil {
        return nil, nil, fmt.Errorf("Failed to read LSP request header: %v\n", err)
    }

    contentLengths, ok := headers["Content-Length"]
    if !ok {
        return nil, nil, fmt.Errorf("Missing Content-Length header in LSP request\n")
    }

    contentLength := contentLengths[0]
    contentByteCnt, err := strconv.Atoi(contentLength)
    if err != nil {
        return nil, nil, fmt.Errorf("Content-Length value is not an integer\n")
    }

    requestContent := []byte{}
    for i := 0; i < contentByteCnt; i++ {
        ch, err := reader.ReadByte()
        if err != nil {
            return nil, nil, fmt.Errorf("Error reading content byte: %v\n", err)
        }

        requestContent = append(requestContent, ch)
    }

    err = json.Unmarshal(requestContent, jsonBody)
    if err != nil {
        return nil, nil, fmt.Errorf("Failed to decode JSON-RPC payload: %v\n", err)
    }

    rawLspRequest = append(rawLspRequest, requestContent...)
    
    return headers, rawLspRequest, nil
}
