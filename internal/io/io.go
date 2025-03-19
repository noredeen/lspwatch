package io

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

type LSPReadResult struct {
	Headers map[string][]string
	RawBody *[]byte
	Err     error
}

type SingleUseDiverterPipe struct {
	source            io.Reader
	firstDestination  io.Writer
	secondDestination io.Writer
	switchCtx         context.Context
	switchFunc        context.CancelFunc
}

type LSPReader struct {
	in *bufio.Reader
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

// Adapted from https://github.com/golang/tools/blob/bf70295789942e4b20ca70a8cd2fe1f3ca2a70bd/internal/jsonrpc2_v2/frame.go#L111
func (lr *LSPReader) Read(jsonBody any) LSPReadResult {
	headers := make(map[string][]string)
	rawLspRequest := make([]byte, 0)

	for {
		line, err := lr.in.ReadString('\n')
		rawLspRequest = append(rawLspRequest, []byte(line)...)
		if err != nil {
			if err == io.EOF {
				if len(rawLspRequest) == 0 {
					return LSPReadResult{
						Err: io.EOF,
					}
				}

				err = io.ErrUnexpectedEOF
			}
			return LSPReadResult{
				Err: fmt.Errorf("error reading header line: %w", err),
			}
		}

		line = strings.TrimSpace(line)
		if line == "" {
			// End of headers
			break
		}

		colonIndex := strings.IndexRune(line, ':')
		if colonIndex < 0 {
			return LSPReadResult{
				Err: fmt.Errorf("invalid header line: %q", line),
			}
		}

		name, value := line[:colonIndex], strings.TrimSpace(line[colonIndex+1:])
		headers[name] = append(headers[name], value)
	}

	contentLengths, ok := headers["Content-Length"]
	if !ok {
		return LSPReadResult{
			Err:     fmt.Errorf("missing Content-Length header in LSP request"),
			RawBody: &rawLspRequest,
		}
	}

	contentLength := contentLengths[0]
	length, err := strconv.ParseInt(contentLength, 10, 32)
	if err != nil {
		return LSPReadResult{
			Err:     fmt.Errorf("failed parsing Content-Length: %q", contentLength),
			RawBody: &rawLspRequest,
		}
	}
	if length <= 0 {
		return LSPReadResult{
			Err:     fmt.Errorf("invalid Content-Length: %d", length),
			RawBody: &rawLspRequest,
		}
	}

	jsonData := make([]byte, length)
	_, err = io.ReadFull(lr.in, jsonData)
	if err != nil {
		return LSPReadResult{
			Err:     fmt.Errorf("failed to read JSON-RPC payload: %w", err),
			RawBody: &rawLspRequest,
		}
	}

	rawLspRequest = append(rawLspRequest, jsonData...)
	err = json.Unmarshal(jsonData, jsonBody)
	if err != nil {
		return LSPReadResult{
			Err:     fmt.Errorf("failed to decode JSON-RPC payload: %w", err),
			RawBody: &rawLspRequest,
		}
	}

	return LSPReadResult{
		Headers: headers,
		RawBody: &rawLspRequest,
	}
}

func NewLSPReader(reader io.ReadCloser) LSPReader {
	return LSPReader{in: bufio.NewReader(reader)}
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

func (bd *SingleUseDiverterPipe) Start() {
	go func() {
		for {
			buf := make([]byte, 1024)
			n, err := bd.source.Read(buf)
			if err != nil {
				break
			}

			select {
			case <-bd.switchCtx.Done():
				bd.secondDestination.Write(buf[:n])
			default:
				bd.firstDestination.Write(buf[:n])
			}
		}
	}()
}

func (bd *SingleUseDiverterPipe) Switch() {
	bd.switchFunc()
}

func NewSingleUseDiverterPipe(
	source io.Reader,
	firstDestination io.Writer,
	secondDestination io.Writer,
) SingleUseDiverterPipe {
	switchCtx, switchFunc := context.WithCancel(context.Background())
	return SingleUseDiverterPipe{
		source:            source,
		firstDestination:  firstDestination,
		secondDestination: secondDestination,
		switchCtx:         switchCtx,
		switchFunc:        switchFunc,
	}
}

func checkDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return info.IsDir(), nil
}
