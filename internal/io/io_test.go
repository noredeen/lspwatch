package io

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"encoding/json"

	"github.com/google/go-cmp/cmp"
)

func TestStringOrInt(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		t.Parallel()
		var v StringOrInt
		err := json.Unmarshal([]byte(`"hello"`), &v)
		if err != nil {
			t.Fatalf("expected to unmarshal string, but got error: %v", err)
		}

		if v.Value != "hello" {
			t.Fatalf("expected Value to be hello, but got %s", v.Value)
		}
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()
		var v StringOrInt
		err := json.Unmarshal([]byte(`123`), &v)
		if err != nil {
			t.Fatalf("expected to unmarshal int, but got error: %v", err)
		}

		if v.Value != "123" {
			t.Fatalf("expected Value to be 123, but got %s", v.Value)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		var v StringOrInt
		err := json.Unmarshal([]byte(`true`), &v)
		if err == nil {
			t.Fatalf("expected to get error when unmarshalling invalid json, but got nil")
		}

		if !strings.Contains(err.Error(), "value is neither string nor int") {
			t.Fatalf("expected error to contain 'value is neither string nor int', but got %s", err.Error())
		}
	})
}

func TestHeaderCaptureReader(t *testing.T) {
	t.Run("captures a complete header one Read()", func(t *testing.T) {
		t.Parallel()
		headers := "Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n"
		reader := io.NopCloser(strings.NewReader(headers + "{\"foo\": \"bar\"}"))
		headerCaptureReader := NewHeaderCaptureReader(reader)
		buffer := make([]byte, 1024)
		headerCaptureReader.Read(buffer)

		if !cmp.Equal(headerCaptureReader.CapturedBytes(), []byte(headers)) {
			t.Fatalf("expected captured bytes to be %s, but got %s", headers, string(headerCaptureReader.CapturedBytes()))
		}

		if !bytes.Contains(buffer, []byte(headers)) {
			t.Fatal("expected Read() to place read bytes in the buffer")
		}
	})

	t.Run("captures a complete header (and no more) with multiple Read()s", func(t *testing.T) {
		t.Parallel()
		headers := "Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n"
		reader := io.NopCloser(strings.NewReader(headers))
		headerCaptureReader := NewHeaderCaptureReader(reader)
		buffer1 := make([]byte, 3)
		buffer2 := make([]byte, 100)
		headerCaptureReader.Read(buffer1)
		headerCaptureReader.Read(buffer2)

		if !cmp.Equal(headerCaptureReader.CapturedBytes(), []byte(headers)) {
			t.Fatalf("expected captured bytes to be %s, but got %s", headers, string(headerCaptureReader.CapturedBytes()))
		}
	})

	t.Run("captures a partial header", func(t *testing.T) {
		t.Parallel()
		headers := "Content-Length: 133\r\nSome-Header: Some-"
		reader := io.NopCloser(strings.NewReader(headers))
		headerCaptureReader := NewHeaderCaptureReader(reader)
		buffer := make([]byte, 500)
		headerCaptureReader.Read(buffer)

		if !cmp.Equal(headerCaptureReader.CapturedBytes(), []byte(headers)) {
			t.Fatalf("expected captured bytes to be %s, but got %s", headers, string(headerCaptureReader.CapturedBytes()))
		}

		if !bytes.Contains(buffer, []byte(headers)) {
			t.Fatal("expected Read() to place read bytes in the buffer")
		}
	})
}

func TestLSPMessageReader(t *testing.T) {
	t.Run("correct input with extra header", func(t *testing.T) {
		t.Parallel()

		correctInput := "Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(correctInput))
		lspmr := NewLSPMessageReader(reader)

		var body LSPClientMessage
		res := lspmr.ReadLSPMessage(&body)
		if res.Err != nil {
			t.Fatalf("expcted to read correct LSP message, but got error: %v", res.Err)
		}

		contentLen, ok := res.Headers["Content-Length"]
		if !ok {
			t.Fatalf("expected Content-Length header to be present in result")
		}

		if contentLen[0] != "133" {
			t.Fatalf("expected Content-Length header to be 133, but got %s", contentLen[0])
		}

		someHeader, ok := res.Headers["Some-Header"]
		if !ok {
			t.Fatalf("expected Some-Header header to be present in result")
		}

		if someHeader[0] != "Some-Value" {
			t.Fatalf("expected Some-Header header to be Some-Value, but got %s", someHeader[0])
		}

		resBody := string(*res.RawBody)
		if !cmp.Equal(resBody, correctInput) {
			t.Fatalf("expected RawBody to be %s, but got %s", correctInput, resBody)
		}
	})

	t.Run("missing Content-Length header", func(t *testing.T) {
		t.Parallel()
		missingContentLengthInput := "\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(missingContentLengthInput))

		var body LSPClientMessage
		lspmr := NewLSPMessageReader(reader)
		res := lspmr.ReadLSPMessage(&body)
		if res.Err == nil {
			t.Fatalf("expected to get error when Content-Length header is missing, but got nil")
		}

		if !strings.Contains(res.Err.Error(), "missing Content-Length header") {
			t.Fatalf("expected error to contain 'missing Content-Length header', but got %s", res.Err.Error())
		}
	})

	t.Run("Content-Length header is not an int", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: not-an-int\r\n\r\n{}"
		reader := io.NopCloser(strings.NewReader(input))
		lspmr := NewLSPMessageReader(reader)
		var body LSPClientMessage
		res := lspmr.ReadLSPMessage(&body)
		if res.Err == nil {
			t.Errorf("expected to get error when Content-Length header is not an int, but got nil")
		}
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: 133\r\n\r\n{\"jsonrpc\"/ \"2.0\", \"method\"; \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(input))
		lspmr := NewLSPMessageReader(reader)
		var body LSPClientMessage
		res := lspmr.ReadLSPMessage(&body)
		if res.Err == nil {
			t.Errorf("expected to get error when JSON body is invalid, but got nil")
		}
	})

	t.Run("correctly reads two back-to-back messages", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(input))
		lspmr := NewLSPMessageReader(reader)

		var body LSPClientMessage
		res := lspmr.ReadLSPMessage(&body)
		if res.Err != nil {
			t.Fatalf("expected first read to succeed, but got error '%v'", res.Err)
		}

		res = lspmr.ReadLSPMessage(&body)
		if res.Err != nil {
			t.Fatalf("expected second read to succeed, but got error '%v'", res.Err)
		}
	})
}
