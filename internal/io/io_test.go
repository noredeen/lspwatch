package io

import (
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

func TestLSPReader(t *testing.T) {
	t.Run("correct input with extra header", func(t *testing.T) {
		t.Parallel()

		correctInput := "Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(correctInput))
		lspReader := NewLSPReader(reader)

		var body LSPClientMessage
		res := lspReader.Read(&body)
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
		lspReader := NewLSPReader(reader)
		res := lspReader.Read(&body)
		if res.Err == nil {
			t.Fatalf("expected to get error when Content-Length header is missing, but got nil")
		}

		if !strings.Contains(res.Err.Error(), "missing Content-Length header") {
			t.Fatalf("expected error to contain 'missing Content-Length header', but got %s", res.Err.Error())
		}
	})

	t.Run("misformated Content-Length header", func(t *testing.T) {
		t.Parallel()
		misformated := "Content-Length 22\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(misformated))

		var body LSPClientMessage
		lspReader := NewLSPReader(reader)
		res := lspReader.Read(&body)
		if res.Err == nil {
			t.Fatalf("expected to get error when Content-Length header is misformated, but got nil")
		}

		if !strings.Contains(res.Err.Error(), "invalid header line") {
			t.Fatalf("expected error to contain 'invalid header line', but got %s", res.Err.Error())
		}
	})

	t.Run("Content-Length header is not an int", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: not-an-int\r\n\r\n{}"
		reader := io.NopCloser(strings.NewReader(input))
		lspReader := NewLSPReader(reader)
		var body LSPClientMessage
		res := lspReader.Read(&body)
		if res.Err == nil {
			t.Errorf("expected to get error when Content-Length header is not an int, but got nil")
		}
	})

	t.Run("Content-Length is negative", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: -1\r\n\r\n{}"
		reader := io.NopCloser(strings.NewReader(input))
		lspReader := NewLSPReader(reader)
		var body LSPClientMessage
		res := lspReader.Read(&body)
		if res.Err == nil {
			t.Errorf("expected to get error when Content-Length header is negative, but got nil")
		}
	})

	t.Run("invalid JSON body", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: 133\r\n\r\n{\"jsonrpc\"/ \"2.0\", \"method\"; \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(input))
		lspReader := NewLSPReader(reader)
		var body LSPClientMessage
		res := lspReader.Read(&body)
		if res.Err == nil {
			t.Errorf("expected to get error when JSON body is invalid, but got nil")
		}
	})

	t.Run("correctly reads two back-to-back messages", func(t *testing.T) {
		t.Parallel()
		input := "Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}Content-Length: 133\r\nSome-Header: Some-Value\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"textDocument/highlight\", \"id\": 2, \"params\": {\"textDocument\": {\"uri\": \"file:///Users/someone/test.go\"}}}"
		reader := io.NopCloser(strings.NewReader(input))
		lspReader := NewLSPReader(reader)

		var body LSPClientMessage
		res := lspReader.Read(&body)
		if res.Err != nil {
			t.Fatalf("expected first read to succeed, but got error '%v'", res.Err)
		}

		res = lspReader.Read(&body)
		if res.Err != nil {
			t.Fatalf("expected second read to succeed, but got error '%v'", res.Err)
		}
	})
}
