package internal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
)

type StringOrInt struct {
	Value string
}

func (s *StringOrInt) UnmarshalJSON(data []byte) error {
	var intVal int
	if err := json.Unmarshal(data, &intVal); err == nil {
		s.Value = strconv.Itoa(intVal)
		return nil
	}

	var strVal string
	if err := json.Unmarshal(data, &strVal); err == nil {
		s.Value = strVal
		return nil
	}

	return fmt.Errorf("Value is neither string nor int: %s", data)
}

type LSPRequestMessage struct {
	Jsonrpc string
	Id      StringOrInt
	Method  string
	Params  *json.RawMessage
}

type LSPResponseMessage struct {
	Jsonrpc string
	Id      StringOrInt
	Result  *json.RawMessage
	Error   *struct {
		Code    int
		Message string
		Data    *json.RawMessage
	}
}

func readLSPMessage(
	reader *loggingReader,
	jsonBody interface{},
) (textproto.MIMEHeader, []byte, error) {
	// NOTE: Passing an io.TeeReader into a bufio.Reader will not work here
	//	because the TeeReader will capture the entire buffer that was read,
	//	and textproto.Reader reads from buffers of size > 1. We will need
	//	a different way of getting the raw header bytes. It's looking like
	//	I might have to just rebuild them from the parsed headers. Ugh

	headerBytes := bytes.Buffer{}
	teeReader := io.TeeReader(reader, &headerBytes)
	bufReader := bufio.NewReader(teeReader)
	tp := textproto.NewReader(bufReader)

	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to read LSP request header: %v", err)
	}

	rawLspRequest := headerBytes.Bytes()

	contentLengths, ok := headers["Content-Length"]
	if !ok {
		return nil, nil, fmt.Errorf("Missing Content-Length header in LSP request")
	}

	contentLength := contentLengths[0]
	contentByteCnt, err := strconv.Atoi(contentLength)
	if err != nil {
		return nil, nil, fmt.Errorf("Content-Length value is not an integer")
	}

	requestContent := []byte{}
	for i := 0; i < contentByteCnt; i++ {
		buffer := make([]byte, 1)
		_, err := bufReader.Read(buffer)
		if err != nil {
			return nil, nil, fmt.Errorf("Error reading content byte: %v", err)
		}

		requestContent = append(requestContent, buffer...)
	}

	err = json.Unmarshal(requestContent, jsonBody)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to decode JSON-RPC payload: %v", err)
	}

	rawLspRequest = append(rawLspRequest, requestContent...)

	return headers, rawLspRequest, nil
}
