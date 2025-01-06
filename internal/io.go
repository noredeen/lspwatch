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

type LSPClientMessage struct {
	Id     *StringOrInt
	Method *string
	Params *json.RawMessage
}

type LSPServerMessage struct {
	Id     *StringOrInt
	Method *string
	Result *json.RawMessage
	Error  *struct {
		Code    int
		Message string
		Data    *json.RawMessage
	}
}

type HeaderCaptureReader struct {
	reader  io.Reader
	buffer  bytes.Buffer
	reading bool
}

func NewHeaderCaptureReader(reader io.Reader) *HeaderCaptureReader {
	return &HeaderCaptureReader{reader: reader, reading: true}
}

func (hcr *HeaderCaptureReader) trimBufferAfterHeader() {
	headerEnd := bytes.Index(hcr.buffer.Bytes(), []byte("\r\n\r\n"))
	if headerEnd != -1 {
		hcr.buffer.Truncate(headerEnd + 4)
	}
}

func (hcr *HeaderCaptureReader) Read(p []byte) (int, error) {
	n, err := hcr.reader.Read(p)
	if err != nil {
		return n, err
	}

	if hcr.reading {
		hcr.buffer.Write(p[:n])
		if bytes.Contains(hcr.buffer.Bytes(), []byte("\r\n\r\n")) {
			hcr.reading = false
			hcr.trimBufferAfterHeader()
		}
	}

	return n, err
}

func (hcr *HeaderCaptureReader) CapturedBytes() []byte {
	return hcr.buffer.Bytes()
}

func readLSPMessage(
	reader io.Reader,
	jsonBody interface{},
) (textproto.MIMEHeader, []byte, error) {
	// NOTE: Passing an io.TeeReader into a bufio.Reader will not work here
	//	because the io.TeeReader will capture the entire buffer that was read
	//	by textproto, and textproto.Reader reads from buffers of size > 1.
	//	This means io.TeeReader will frequently capture bytes beyond the
	//	header of the request. My solution is to create a custom capturing
	//	reader to operate underneath bufio.Reader for retaining ONLY header bytes.

	capReader := NewHeaderCaptureReader(reader)
	bufReader := bufio.NewReader(capReader)
	tp := textproto.NewReader(bufReader)

	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to read LSP request header: %v", err)
	}

	rawLspRequest := capReader.CapturedBytes()

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
