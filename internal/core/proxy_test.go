package core

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/noredeen/lspwatch/internal/testutil"
	"github.com/sirupsen/logrus"
)

type mockProxyMetricsRegistry struct {
	enableMetricCalls []telemetry.AvailableMetric
	emitMetricCalls   []telemetry.MetricRecording
	mu                sync.Mutex
}
type erroringMetricsRegistry struct {
	errorEnableMetric bool
	errorEmitMetric   bool
}

var _ telemetry.MetricsRegistry = &mockProxyMetricsRegistry{}
var _ telemetry.MetricsRegistry = &erroringMetricsRegistry{}

func (m *mockProxyMetricsRegistry) EnableMetric(metric telemetry.AvailableMetric) error {
	m.enableMetricCalls = append(m.enableMetricCalls, metric)
	return nil
}

func (m *mockProxyMetricsRegistry) EmitMetric(metric telemetry.MetricRecording) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitMetricCalls = append(m.emitMetricCalls, metric)
	return nil
}

func (m *mockProxyMetricsRegistry) IsMetricEnabled(metric telemetry.AvailableMetric) bool {
	return true
}

func (e *erroringMetricsRegistry) EnableMetric(metric telemetry.AvailableMetric) error {
	if e.errorEnableMetric {
		return errors.New("erroring metrics registry")
	}
	return nil
}

func (e *erroringMetricsRegistry) EmitMetric(metric telemetry.MetricRecording) error {
	if e.errorEmitMetric {
		return errors.New("erroring metrics registry")
	}
	return nil
}

func (e *erroringMetricsRegistry) IsMetricEnabled(metric telemetry.AvailableMetric) bool {
	return true
}

func TestNewProxyHandler(t *testing.T) {
	t.Run("creates a default handler when no configured metrics or metered requests", func(t *testing.T) {
		t.Parallel()
		metricsRegistry := mockProxyMetricsRegistry{}
		proxyHandler, err := NewProxyHandler(&config.LspwatchConfig{}, &metricsRegistry, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("expected no error creating proxy handler, got '%v'", err)
		}

		if len(proxyHandler.meteredRequests) != len(defaultMeteredRequests) {
			t.Fatalf("expected %d metered requests, got %d", len(defaultMeteredRequests), len(proxyHandler.meteredRequests))
		}

		if len(metricsRegistry.enableMetricCalls) != 1 {
			t.Fatalf("expected 1 EnableMetric call, got %d", len(metricsRegistry.enableMetricCalls))
		}

		if metricsRegistry.enableMetricCalls[0] != telemetry.RequestDuration {
			t.Fatalf("expected EnableMetric to be called with %s, got %s", telemetry.RequestDuration, metricsRegistry.enableMetricCalls[0])
		}
	})

	t.Run("creates a new proxy handler with custom metered requests", func(t *testing.T) {
		t.Parallel()
		customMeteredRequests := []string{"textDocument/references", "textDocument/hover"}
		cfg := &config.LspwatchConfig{
			MeteredRequests: &customMeteredRequests,
			Metrics:         &[]string{},
		}

		metricsRegistry := mockProxyMetricsRegistry{}
		proxyHandler, err := NewProxyHandler(cfg, &metricsRegistry, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("expected no error creating proxy handler, got '%v'", err)
		}

		if len(proxyHandler.meteredRequests) != len(customMeteredRequests) {
			t.Fatalf("expected %d metered requests, got %d", len(customMeteredRequests), len(proxyHandler.meteredRequests))
		}

		if len(metricsRegistry.enableMetricCalls) != 0 {
			t.Fatalf("expected 0 EnableMetric calls, got %d", len(metricsRegistry.enableMetricCalls))
		}

	})

	t.Run("should return error when EnableMetric calls return errors", func(t *testing.T) {
		t.Parallel()
		metricsRegistry := erroringMetricsRegistry{
			errorEnableMetric: true,
		}
		_, err := NewProxyHandler(&config.LspwatchConfig{}, &metricsRegistry, nil, nil, nil, nil, nil)
		if err == nil {
			t.Fatalf("expected error creating proxy handler, got nil")
		}
	})
}

func TestProxyHandler(t *testing.T) {
	t.Run("basic proxying works", func(t *testing.T) {
		t.Run("for any kind of correctly-formed LSP message (except exit)", func(t *testing.T) {
			t.Parallel()
			metricsRegistry := mockProxyMetricsRegistry{}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
			t.Cleanup(func() {
				proxyToClient.Close()
				proxyToServer.Close()
				clientToProxy.Close()
				serverToProxy.Close()
			})

			proxyHandler.Start()
			time.Sleep(50 * time.Millisecond)

			var msgFromClient string
			var msgFromServer string
			var err error

			msgFromClient = "Content-Length: 71\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"initialize\", \"params\": {\"processId\": 22}}"
			sendMessage(t, clientToProxy, msgFromClient)
			assertPropagation(t, "-> | initialize request", proxyToServer, msgFromClient)

			msgFromServer = "Content-Length: 72\r\n\r\n{\"jsonrpc\": \"2.0\", \"result\": {\"capabilities\": {\"positionEncoding\": 22}}}"
			sendMessage(t, serverToProxy, msgFromServer)
			assertPropagation(t, "<- | initialize response", proxyToClient, msgFromServer)

			msgFromClient = "Content-Length: 43\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"initialized\"}"
			sendMessage(t, clientToProxy, msgFromClient)
			assertPropagation(t, "-> | initialized request", proxyToServer, msgFromClient)

			msgFromServer = "Content-Length: 181\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"client/registerCapability\", \"params\": {\"registrations\": [{\"id\": \"1\", \"method\": \"$/cancelRequest\", \"registerOptions\": {\"idempotent\": true}}], \"id\": 22}}"
			sendMessage(t, clientToProxy, msgFromServer)
			assertPropagation(t, "<- | register capabilities request", proxyToServer, msgFromServer)

			msgFromClient = "Content-Length: 42\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 22, \"result\": \"\"}"
			sendMessage(t, clientToProxy, msgFromClient)
			assertPropagation(t, "-> | register capabilities response", proxyToServer, msgFromClient)

			msgFromClient = "Content-Length: 128\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"method\": \"textDocument/references\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file.ts\"}}}"
			sendMessage(t, clientToProxy, msgFromClient)
			assertPropagation(t, "-> | textDocument/references request", proxyToServer, msgFromClient)

			msgFromClient = "Content-Length: 128\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"method\": \"textDocument/references\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file.ts\"}}}"
			sendMessage(t, clientToProxy, msgFromClient)
			assertPropagation(t, "-> | DUPLICATE textDocument/references request", proxyToServer, msgFromClient)

			msgFromServer = "Content-Length: 41\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 2, \"result\": []}"
			sendMessage(t, serverToProxy, msgFromServer)
			assertPropagation(t, "<- | textDocument/references response", proxyToClient, msgFromServer)

			msgFromServer = "Content-Length: 43\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 563, \"result\": []}"
			sendMessage(t, serverToProxy, msgFromServer)
			assertPropagation(t, "<- | response for unbuffered request", proxyToClient, msgFromServer)

			err = proxyHandler.Shutdown()
			if err != nil {
				t.Fatalf("expected no error shutting down proxy handler, got '%v'", err)
			}

			testutil.AssertExitsBefore(t, "wait for proxy handler to shut down",
				func() {
					proxyHandler.Wait()
				},
				6*time.Second,
			)
		})

		t.Run("for incorrectly-formed messages", func(t *testing.T) {
			t.Parallel()
			metricsRegistry := mockProxyMetricsRegistry{}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
			t.Cleanup(func() {
				err := proxyHandler.Shutdown()
				if err != nil {
					t.Fatalf("expected no error shutting down proxy handler, got '%v'", err)
				}
				proxyToClient.Close()
				proxyToServer.Close()
				clientToProxy.Close()
				serverToProxy.Close()
				proxyHandler.Wait()
			})

			proxyHandler.Start()
			time.Sleep(50 * time.Millisecond)

			var msgFromClient string
			var msgFromServer string

			// No jsonrpc field.
			msgFromClient = "Content-Length: 110\r\n\r\n{\"id\": 1, \"method\": \"textDocument/references\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file.ts\"}}}"
			sendMessage(t, clientToProxy, msgFromClient)
			assertPropagation(t, "-> | missing field in textDocument/references request", proxyToServer, msgFromClient)

			// Empty JSON.
			msgFromServer = "Content-Length: 2\r\n\r\n{}"
			sendMessage(t, serverToProxy, msgFromServer)
			assertPropagation(t, "<- | empty json", proxyToClient, msgFromServer)
		})

		t.Run("when EmitMetric calls return errors", func(t *testing.T) {
			t.Parallel()
			metricsRegistry := erroringMetricsRegistry{
				errorEmitMetric: true,
			}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
			t.Cleanup(func() {
				err := proxyHandler.Shutdown()
				if err != nil {
					t.Fatalf("expected no error shutting down proxy handler, got '%v'", err)
				}
				proxyToClient.Close()
				proxyToServer.Close()
				clientToProxy.Close()
				serverToProxy.Close()
				proxyHandler.Wait()
			})

			proxyHandler.Start()
			time.Sleep(50 * time.Millisecond)

			var msgFromClient string
			var msgFromServer string

			msgFromClient = "Content-Length: 128\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"method\": \"textDocument/references\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file.ts\"}}}"
			sendMessage(t, clientToProxy, msgFromClient)
			buf := make([]byte, len(msgFromClient)+100)
			proxyToServer.Read(buf)

			time.Sleep(500 * time.Millisecond)

			msgFromServer = "Content-Length: 41\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"result\": []}"
			sendMessage(t, serverToProxy, msgFromServer)
			assertPropagation(t, "textDocument/references response w/ failed EmitMetric", proxyToClient, msgFromServer)
		})
	})

	t.Run("language server shutdown process is correctly handled", func(t *testing.T) {
		t.Run("an exit message from the client is intercepted and raised to caller", func(t *testing.T) {
			t.Parallel()
			metricsRegistry := mockProxyMetricsRegistry{}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
			t.Cleanup(func() {
				err := proxyHandler.Shutdown()
				if err != nil {
					t.Fatalf("expected no error shutting down proxy handler, got '%v'", err)
				}
				proxyToClient.Close()
				proxyToServer.Close()
				clientToProxy.Close()
				serverToProxy.Close()
				proxyHandler.Wait()
			})

			proxyHandler.Start()

			msgFromClient := "Content-Length: 36\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"exit\"}"
			msgReader := strings.NewReader(msgFromClient)
			msgReader.WriteTo(clientToProxy)
			buf := make([]byte, len(msgFromClient)+100)

			// Read should block since no data is sent to the server.
			testutil.AssertDoesNotExitBefore(
				t, "reading data sent from proxy to server",
				func() { proxyToServer.Read(buf) }, 2*time.Second,
			)

			// Must raise to caller.
			testutil.AssertExitsBefore(
				t, "proxy handler shutdown",
				func() { <-proxyHandler.ShutdownRequested() },
				200*time.Millisecond,
			)

		})

		t.Run("a shutdown instruction from the caller causes exit LSP request to propagate to the server", func(t *testing.T) {
			t.Parallel()
			metricsRegistry := mockProxyMetricsRegistry{}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
			t.Cleanup(func() {
				proxyToClient.Close()
				proxyToServer.Close()
				clientToProxy.Close()
				serverToProxy.Close()
				proxyHandler.Wait()
			})

			proxyHandler.Start()

			msgFromClient := "Content-Length: 36\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"exit\"}"
			msgReader := strings.NewReader(msgFromClient)
			msgReader.WriteTo(clientToProxy)

			// There should be no need for the caller to monitor the shutdown channel.
			time.Sleep(200 * time.Millisecond)

			err := proxyHandler.Shutdown()
			if err != nil {
				t.Fatalf("expected no error shutting down proxy handler, got '%v'", err)
			}

			buf := make([]byte, len(msgFromClient)+100)
			testutil.AssertExitsBefore(
				t, "propagate exit message to server",
				func() { proxyToServer.Read(buf) },
				300*time.Millisecond,
			)
			if !bytes.Contains(buf, []byte(msgFromClient)) {
				t.Errorf("expected exit message to get proxied, but got '%s'", string(buf))
			}
		})
	})

	t.Run("emits metrics by correctly matching request with response", func(t *testing.T) {
		t.Parallel()
		metricsRegistry := mockProxyMetricsRegistry{}
		logger := logrus.New()
		logger.SetOutput(io.Discard)
		proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
		t.Cleanup(func() {
			err := proxyHandler.Shutdown()
			if err != nil {
				t.Fatalf("expected no error shutting down proxy handler, got '%v'", err)
			}
			proxyToClient.Close()
			proxyToServer.Close()
			clientToProxy.Close()
			serverToProxy.Close()
			proxyHandler.Wait()
		})

		var msgFromServer string
		var msgFromClient string
		var buf []byte

		proxyHandler.Start()
		time.Sleep(100 * time.Millisecond)

		// All unit tests in this file use io.Pipe() to simulate communcation with a client/server.
		// In io.Pipe, reads and writes are matched one-to-one. So, a write will block until
		// a read happens to consume the message, and vice versa. This is why every sendMessage
		// call below is followed by the corresponding read to avoid blocking the proxy.

		// TODO: Probably a better idea to use a message queue for writing.

		// Erroneous server response to unbuffered request.
		msgFromServer = "Content-Length: 41\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"result\": []}"
		sendMessage(t, serverToProxy, msgFromServer)
		buf = make([]byte, len(msgFromServer)+100)
		proxyToClient.Read(buf)

		time.Sleep(500 * time.Millisecond)

		msgFromClient = "Content-Length: 128\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"method\": \"textDocument/references\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file.ts\"}}}"
		sendMessage(t, clientToProxy, msgFromClient)
		buf = make([]byte, len(msgFromClient)+100)
		proxyToServer.Read(buf)

		time.Sleep(500 * time.Millisecond)

		msgFromClient = "Content-Length: 127\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 2, \"method\": \"textDocument/document\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file1.ts\"}}}"
		sendMessage(t, clientToProxy, msgFromClient)
		buf = make([]byte, len(msgFromClient)+100)
		proxyToServer.Read(buf)

		time.Sleep(500 * time.Millisecond)

		msgFromServer = "Content-Length: 41\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"result\": []}"
		sendMessage(t, serverToProxy, msgFromServer)
		buf = make([]byte, len(msgFromServer)+100)
		proxyToClient.Read(buf)

		time.Sleep(500 * time.Millisecond)
		if len(metricsRegistry.emitMetricCalls) != 1 {
			t.Fatalf("expected 1 EmitMetric call, got %d", len(metricsRegistry.emitMetricCalls))
		}

		metricRec := metricsRegistry.emitMetricCalls[0]
		if metricRec.Name != "request.duration" {
			t.Fatalf("expected 'request.duration' metric, got '%s'", metricRec.Name)
		}

		tags := *metricRec.Tags
		method, ok := tags["method"]
		if !ok {
			t.Fatal("expected presence of 'method' tag in emitted metric")
		}

		if method != "textDocument/references" {
			t.Errorf("expected method to be textDocument/references, got '%s'", method)
		}
	})
}

func setUpTest(t *testing.T, metricsRegistry telemetry.MetricsRegistry, logger *logrus.Logger) (
	proxyHandler *ProxyHandler,
	proxyToClient, proxyToServer *io.PipeReader,
	clientToProxy, serverToProxy *io.PipeWriter,
) {
	t.Helper()
	// clientToProxy is for me to send message as the client to the proxy
	// clientIn is for the proxy to read what I send it as the client
	clientIn, clientToProxy := io.Pipe()

	// serverToProxy is for me to send message as the server to the proxy
	// serverIn is for the proxy to read what I send it as the server
	serverIn, serverToProxy := io.Pipe()

	// proxyToClient is for me to read what the proxy sends to the client
	// clientOut is for the proxy to write stuff to me as the client
	proxyToClient, clientOut := io.Pipe()

	// proxyToServer is for me to read what the proxy sends to the server
	// serverOut is for the proxy to write stuff to me as the server
	proxyToServer, serverOut := io.Pipe()

	proxyHandler, err := NewProxyHandler(
		&config.LspwatchConfig{},
		metricsRegistry,
		clientIn,
		clientOut,
		serverIn,
		serverOut,
		logger,
	)
	if err != nil {
		t.Fatalf("expected no error creating proxy handler, got '%v'", err)
	}

	return proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy
}

func sendMessage(t *testing.T, origin *io.PipeWriter, msg string) {
	t.Helper()
	msgReader := strings.NewReader(msg)
	_, err := msgReader.WriteTo(origin)
	if err != nil {
		t.Fatalf("expected no error writing message to origin, got '%v'", err)
	}
}

func assertPropagation(t *testing.T, description string, destination *io.PipeReader, msg string) {
	t.Helper()
	buf := make([]byte, len(msg)+100)
	ok := testutil.AssertExitsBefore(
		t,
		description,
		func() { destination.Read(buf) },
		100*time.Millisecond,
	)

	if ok && !bytes.Contains(buf, []byte(msg)) {
		t.Fatalf("expected message to get proxied, but got %s", string(buf))
	}
}
