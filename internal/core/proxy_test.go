package core

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/noredeen/lspwatch/internal/testutil"
	"github.com/sirupsen/logrus"
)

type nopWriteCloser struct {
	io.Writer
}

type mockProxyMetricsRegistry struct {
	enableMetricCalls []telemetry.AvailableMetric
	emitMetricCalls   []telemetry.MetricRecording
	mu                sync.Mutex
}

var _ telemetry.MetricsRegistry = &mockProxyMetricsRegistry{}

func (nopWriteCloser) Close() error { return nil }

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

func TestNewProxyHandler(t *testing.T) {
	t.Run("no configured metrics or metered requests", func(t *testing.T) {
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
}

func TestProxyHandler(t *testing.T) {
	t.Run("proxying works", func(t *testing.T) {
		// - (server) received server-originated message
		// - (server) received response for unbuffered request
		// - (client) received non-request message
		// - (client) duplicate request ID in the buffer
		t.Run("for any kind of correctly-formed LSP message (except exit)", func(t *testing.T) {
			t.Parallel()
			metricsRegistry := mockProxyMetricsRegistry{}
			logger := logrus.New()
			logger.SetOutput(os.Stdout)
			proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy := setUpTest(t, &metricsRegistry, logger)
			proxyHandler.Start()

			var msgFromClient string
			var msgFromServer string

			msgFromClient = "Content-Length: 71\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"initialize\", \"params\": {\"processId\": 22}}"
			sendMessageAndAssert(t, "-> | initialize request", clientToProxy, proxyToServer, msgFromClient)

			msgFromServer = "Content-Length: 72\r\n\r\n{\"jsonrpc\": \"2.0\", \"result\": {\"capabilities\": {\"positionEncoding\": 22}}}"
			sendMessageAndAssert(t, "<- | initialize response", serverToProxy, proxyToClient, msgFromServer)

			msgFromClient = "Content-Length: 43\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"initialized\"}"
			sendMessageAndAssert(t, "-> | initialized request", clientToProxy, proxyToServer, msgFromClient)

			msgFromServer = "Content-Length: 181\r\n\r\n{\"jsonrpc\": \"2.0\", \"method\": \"client/registerCapability\", \"params\": {\"registrations\": [{\"id\": \"1\", \"method\": \"$/cancelRequest\", \"registerOptions\": {\"idempotent\": true}}], \"id\": 22}}"
			sendMessageAndAssert(t, "<- | register capabilities request", serverToProxy, proxyToClient, msgFromServer)

			msgFromClient = "Content-Length: 42\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 22, \"result\": \"\"}"
			sendMessageAndAssert(t, "-> | register capabilities response", clientToProxy, proxyToServer, msgFromClient)

			msgFromClient = "Content-Length: 128\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"method\": \"textDocument/references\", \"params\": {\"textDocument\": {\"uri\": \"file:///path/to/file.ts\"}}}"
			sendMessageAndAssert(t, "-> | textDocument/references request", clientToProxy, proxyToServer, msgFromClient)

			msgFromServer = "Content-Length: 41\r\n\r\n{\"jsonrpc\": \"2.0\", \"id\": 1, \"result\": []}"
			sendMessageAndAssert(t, "<- | textDocument/references response", serverToProxy, proxyToClient, msgFromServer)
		})

		// - (server/client) bad json
		t.Run("for incorrectly-formed messages", func(t *testing.T) {
			t.Parallel()
		})

		t.Run("when EmitMetric calls return errors", func(t *testing.T) {
			t.Parallel()
		})
	})

	t.Run("an exit message from the client is intercepted (no proxying)", func(t *testing.T) {
		t.Parallel()
	})

	t.Run("a shutdown instruction causes the exit LSP request to propagate to the server", func(t *testing.T) {
		t.Parallel()
	})

	// (can tell from computed duration and tags)
	t.Run("emits metrics by correctly matches request with response", func(t *testing.T) {
		t.Parallel()
	})
}

func setUpTest(t *testing.T, metricsRegistry *mockProxyMetricsRegistry, logger *logrus.Logger) (
	proxyHandler *ProxyHandler,
	proxyToClient, proxyToServer *io.PipeReader,
	clientToProxy, serverToProxy *io.PipeWriter,
) {
	t.Helper()
	// clientToProxy is for me to send message as the client to the proxy
	// clientInput is for the proxy to read what I send it as the client
	clientIn, clientToProxy := io.Pipe()
	clientInput := io.NopCloser(clientIn)

	// serverToProxy is for me to send message as the server to the proxy
	// serverInput is for the proxy to read what I send it as the server
	serverIn, serverToProxy := io.Pipe()
	serverInput := io.NopCloser(serverIn)

	// proxyToClient is for me to read what the proxy sends to the client
	// clientOutput is for the proxy to write stuff to me as the client
	proxyToClient, clientOut := io.Pipe()
	clientOutput := nopWriteCloser{clientOut}

	// proxyToServer is for me to read what the proxy sends to the server
	// serverOutput is for the proxy to write stuff to me as the server
	proxyToServer, serverOut := io.Pipe()
	serverOutput := nopWriteCloser{serverOut}

	proxyHandler, err := NewProxyHandler(
		&config.LspwatchConfig{},
		metricsRegistry,
		clientInput,
		clientOutput,
		serverInput,
		serverOutput,
		logger,
	)
	if err != nil {
		t.Fatalf("expected no error creating proxy handler, got '%v'", err)
	}

	return proxyHandler, proxyToClient, proxyToServer, clientToProxy, serverToProxy
}

func sendMessageAndAssert(t *testing.T, description string, origin *io.PipeWriter, destination *io.PipeReader, msg string) {
	t.Helper()
	msgReader := strings.NewReader(msg)
	msgReader.WriteTo(origin)
	buf := make([]byte, len(msg)+100)
	ok := testutil.AssertExitsAfter(
		t,
		fmt.Sprintf("%s -- reading from destination", description),
		func() { destination.Read(buf) },
		100*time.Millisecond,
	)

	if ok && !bytes.Contains(buf, []byte(msg)) {
		t.Fatalf("expected message to get proxied, but got %s", string(buf))
	}
}
