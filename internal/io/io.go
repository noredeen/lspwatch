package io

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"path/filepath"
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

var _ io.Reader = &HeaderCaptureReader{}

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

func NewLSPMessageReader(reader io.ReadCloser) LSPMessageReader {
	headerCaptureReader := NewHeaderCaptureReader(reader)
	return LSPMessageReader{
		headerCaptureReader: &headerCaptureReader,
		bufReader:           bufio.NewReader(&headerCaptureReader),
	}
}

// If logDir is not an empty string, CreateLogger will create a new file inside logDir
// (creating the directory if it doesn't exist) and set that as the logger's output.
//
// TODO: Write some integration tests for this.
func CreateLogger(logDir string, fileName string) (*logrus.Logger, *os.File, error) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	if logDir != "" {
		dirExists, err := checkDir(logDir)
		if err != nil {
			return nil, nil, fmt.Errorf("error checking log directory: %v", err)
		}

		if !dirExists {
			err = os.MkdirAll(logDir, 0755)
			if err != nil {
				return nil, nil, fmt.Errorf("error creating log directory: %v", err)
			}
		}

		filePath := filepath.Join(logDir, fileName)
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating log file: %v", err)
		}
		logger.Out = file
		return logger, file, nil
	}

	return logger, nil, nil
}

func checkDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}
