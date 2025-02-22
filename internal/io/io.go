package io

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"strconv"

	"github.com/sirupsen/logrus"
)

type StringOrInt struct {
	Value string
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

type LSPMessageReader struct {
	headerCaptureReader *HeaderCaptureReader
	bufReader           *bufio.Reader
}

// io.Reader implementation that captures the first complete MIME header
// within the buffer.
type HeaderCaptureReader struct {
	reader       io.ReadCloser
	headerBuffer bytes.Buffer
	reading      bool
}

type LSPReadResult struct {
	Headers textproto.MIMEHeader
	RawBody *[]byte
	Err     error
}

// For debugging
type LoggingReader struct {
	r      io.Reader
	Logger *logrus.Logger
}

var _ io.Reader = &LoggingReader{}
var _ io.Reader = &HeaderCaptureReader{}

func (lr *LoggingReader) Read(p []byte) (n int, err error) {
	n, err = lr.r.Read(p)
	lr.Logger.Infof("Read %d bytes: %q\n", n, p[:n])
	return n, err
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

	return fmt.Errorf("value is neither string nor int: %s", data)
}

func (hcr *HeaderCaptureReader) trimBufferAfterHeader() {
	headerEnd := bytes.Index(hcr.headerBuffer.Bytes(), []byte("\r\n\r\n"))
	if headerEnd != -1 {
		hcr.headerBuffer.Truncate(headerEnd + 4)
	}
}

func (hcr *HeaderCaptureReader) Read(p []byte) (int, error) {
	n, err := hcr.reader.Read(p)
	if err != nil {
		return n, err
	}

	if hcr.reading {
		hcr.headerBuffer.Write(p[:n])
		if bytes.Contains(hcr.headerBuffer.Bytes(), []byte("\r\n\r\n")) {
			hcr.reading = false
			hcr.trimBufferAfterHeader()
		}
	}

	return n, err
}

func (hcr *HeaderCaptureReader) Close() error {
	return hcr.reader.Close()
}

func (hcr *HeaderCaptureReader) Reset() {
	hcr.reading = true
	hcr.headerBuffer.Truncate(0)
}

// Returns the bytes captured by the reader through the end of the first MIME header.
func (hcr *HeaderCaptureReader) CapturedBytes() []byte {
	return hcr.headerBuffer.Bytes()
}

// Reads an LSP message.
func (lspmr *LSPMessageReader) ReadLSPMessage(jsonBody interface{}) LSPReadResult {
	lspmr.headerCaptureReader.Reset()
	tp := textproto.NewReader(lspmr.bufReader)

	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		return LSPReadResult{
			Err: fmt.Errorf("failed to read LSP request header: %v", err),
		}
	}

	rawLspRequest := lspmr.headerCaptureReader.CapturedBytes()

	contentLengths, ok := headers["Content-Length"]
	if !ok {
		return LSPReadResult{
			Err: fmt.Errorf("missing Content-Length header in LSP request"),
		}
	}

	contentLength := contentLengths[0]
	contentByteCnt, err := strconv.Atoi(contentLength)
	if err != nil {
		return LSPReadResult{
			Err: fmt.Errorf("Content-Length value '%s' is not an integer", contentLength),
		}
	}

	contentBuffer := make([]byte, contentByteCnt)
	_, err = io.ReadFull(lspmr.bufReader, contentBuffer)
	if err != nil {
		return LSPReadResult{
			Err: fmt.Errorf("error reading content bytes: %v", err),
		}
	}

	err = json.Unmarshal(contentBuffer, jsonBody)
	if err != nil {
		return LSPReadResult{
			Err:     fmt.Errorf("failed to decode JSON-RPC payload: %v", err),
			RawBody: &contentBuffer,
		}
	}

	rawLspRequest = append(rawLspRequest, contentBuffer...)

	return LSPReadResult{
		Headers: headers,
		RawBody: &rawLspRequest,
	}
}

func NewHeaderCaptureReader(reader io.ReadCloser) HeaderCaptureReader {
	return HeaderCaptureReader{reader: reader, reading: true}
}

// func ReadLSPMessage(
// 	reader io.ReadCloser,
// 	jsonBody interface{},
// ) LSPReadResult {
// 	// NOTE: Passing an io.TeeReader into a bufio.Reader will not work here
// 	//	because the io.TeeReader will capture the entire buffer that was read
// 	//	by textproto, and textproto.Reader reads from buffers of size > 1.
// 	//	This means io.TeeReader will frequently capture bytes beyond the
// 	//	header of the request. My solution is to create a custom capturing
// 	//	reader to operate underneath bufio.Reader for retaining ONLY header bytes.

// 	capReader := NewHeaderCaptureReader(reader)
// 	bufReader := bufio.NewReader(&capReader)
// 	tp := textproto.NewReader(bufReader)

// 	headers, err := tp.ReadMIMEHeader()
// 	if err != nil {
// 		return LSPReadResult{
// 			Err: fmt.Errorf("failed to read LSP request header: %v", err),
// 		}
// 	}

// 	rawLspRequest := capReader.CapturedBytes()

// 	contentLengths, ok := headers["Content-Length"]
// 	if !ok {
// 		return LSPReadResult{
// 			Err: fmt.Errorf("missing Content-Length header in LSP request"),
// 		}
// 	}

// 	contentLength := contentLengths[0]
// 	contentByteCnt, err := strconv.Atoi(contentLength)
// 	if err != nil {
// 		return LSPReadResult{
// 			Err: fmt.Errorf("Content-Length value is not an integer"),
// 		}
// 	}

// 	contentBuffer := make([]byte, contentByteCnt)
// 	_, err = bufReader.Read(contentBuffer)
// 	if err != nil {
// 		return LSPReadResult{
// 			Err: fmt.Errorf("error reading content bytes: %v", err),
// 		}
// 	}

// 	err = json.Unmarshal(contentBuffer, jsonBody)
// 	if err != nil {
// 		return LSPReadResult{
// 			Err: fmt.Errorf("failed to decode JSON-RPC payload: %v", err),
// 		}
// 	}

// 	rawLspRequest = append(rawLspRequest, contentBuffer...)

// 	return LSPReadResult{
// 		Headers: headers,
// 		RawBody: &rawLspRequest,
// 	}
// }

func CreateLogger(filePath string, enabled bool) (*logrus.Logger, *os.File, error) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	if enabled {
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating log file: %v", err)
		}
		logger.Out = file
		return logger, file, nil
	}

	return logger, nil, nil
}

func NewLSPMessageReader(reader io.ReadCloser) LSPMessageReader {
	headerCaptureReader := NewHeaderCaptureReader(reader)
	return LSPMessageReader{
		headerCaptureReader: &headerCaptureReader,
		bufReader:           bufio.NewReader(&headerCaptureReader),
	}
}
