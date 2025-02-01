package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/elliotchance/orderedmap/v3"
	"github.com/noredeen/lspwatch/internal/config"
	lspwatch_io "github.com/noredeen/lspwatch/internal/io"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/sirupsen/logrus"
)

// TODO: This file needs cleaning up

type RequestBookmark struct {
	RequestTime time.Time
	Method      string
}

type ProxyHandler struct {
	meteredRequests    map[string]struct{}
	metricsRegistry    telemetry.MetricsRegistry
	requestBuffer      *orderedmap.OrderedMap[string, RequestBookmark]
	outgoingShutdown   chan struct{}
	incomingShutdown   chan struct{}
	listenersWaitGroup *sync.WaitGroup
	logger             *logrus.Logger
}

var availableLSPMetrics = map[telemetry.AvailableMetric]telemetry.MetricRegistration{
	telemetry.RequestDuration: {
		Kind:        telemetry.Histogram,
		Name:        "lspwatch.request.duration",
		Description: "Duration of LSP request",
		Unit:        "s",
	},
}

var defaultMeteredRequests = []string{
	"initialize",
	"textDocument/references",
	"textDocument/hover",
	"textDocument/documentSymbol",
	"textDocument/completion",
	"textDocument/diagnostic",
	"textDocument/signatureHelp",
}

func (ph *ProxyHandler) ShutdownRequested() chan struct{} {
	return ph.outgoingShutdown
}

// Idempotent and non-blocking. Use Wait() to block until shutdown is complete.
func (ph *ProxyHandler) Shutdown() error {
	if ph.incomingShutdown != nil {
		close(ph.incomingShutdown)
		ph.incomingShutdown = nil
	}

	return nil
}

func (ph *ProxyHandler) Wait() {
	ph.listenersWaitGroup.Wait()
	ph.logger.Info("proxy listeners shutdown complete")
}

func (ph *ProxyHandler) Launch(
	serverOutputPipe io.ReadCloser,
	serverInputPipe io.WriteCloser,
) {
	ph.listenersWaitGroup.Add(2)
	go ph.listenServer(serverOutputPipe)
	go ph.listenClient(serverInputPipe)
}

func (ph *ProxyHandler) raiseShutdownRequest() {
	ph.outgoingShutdown <- struct{}{}
}

func (ph *ProxyHandler) enableMetrics(cfg *config.LspwatchConfig) error {
	// Default behavior if `metrics` is not specified in the config
	if cfg.Metrics == nil {
		err := ph.metricsRegistry.EnableMetric(telemetry.RequestDuration)
		if err != nil {
			return err
		}
		return nil
	}

	for _, metric := range *cfg.Metrics {
		err := ph.metricsRegistry.EnableMetric(telemetry.AvailableMetric(metric))
		if err != nil {
			return err
		}
	}

	return nil
}

func (ph *ProxyHandler) listenServer(serverOutputPipe io.ReadCloser) {
	type ServerReadResult struct {
		serverMessage lspwatch_io.LSPServerMessage
		result        *lspwatch_io.LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())
	internalWg := sync.WaitGroup{}
	internalWg.Add(1)

	defer func() {
		stopReader()
		serverOutputPipe.Close()
		internalWg.Wait()
		ph.listenersWaitGroup.Done()
		ph.logger.Info("server listener shutdown complete")
	}()

	serverReadResultChan := make(chan ServerReadResult)
	go func(ctx context.Context) {
		defer internalWg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				var serverMessage lspwatch_io.LSPServerMessage
				result := lspwatch_io.ReadLSPMessage(serverOutputPipe, &serverMessage)
				res := ServerReadResult{
					serverMessage: serverMessage,
					result:        &result,
				}
				if ctx.Err() == nil {
					serverReadResultChan <- res
				}
			}
		}
	}(ctx)

	for {
		select {
		case <-ph.incomingShutdown:
			{
				return
			}
		case res := <-serverReadResultChan:
			{
				serverMessage := res.serverMessage
				result := res.result
				if result.Err == nil {
					// In LSP, servers can originate requests (which include a `method` field)
					// in some cases. lspwatch ignores such server requests.
					if serverMessage.Id != nil && serverMessage.Method == nil {
						requestBookmark, ok := ph.requestBuffer.Get(serverMessage.Id.Value)
						if ok {
							ph.requestBuffer.Delete(serverMessage.Id.Value)

							// Only consider requests which lspwatch is configured to meter.
							if _, metered := ph.meteredRequests[requestBookmark.Method]; metered {

								// Meter this requests's duration only if it's enabled.
								if ph.metricsRegistry.IsMetricEnabled(telemetry.RequestDuration) {
									duration := time.Since(requestBookmark.RequestTime)
									requestDurationMetric := telemetry.NewMetricRecording(
										telemetry.RequestDuration,
										time.Now().Unix(),
										duration.Seconds(),
										telemetry.NewTag("method", telemetry.TagValue(requestBookmark.Method)),
									)
									err := ph.metricsRegistry.EmitMetric(requestDurationMetric)
									if err != nil {
										ph.logger.Errorf("error emitting metric: %v", err)
									}
								}
							}
						} else {
							ph.logger.Infof(
								"received response for unbuffered request with ID=%v",
								serverMessage.Id.Value,
							)
						}
					}
				} else {
					ph.logger.Errorf("error parsing message from language server: %v", result.Err)
				}

				// Forward message
				_, err := os.Stdout.Write(*result.RawBody)
				if err != nil {
					ph.logger.Errorf("error forwarding server message to client: %v", err)
				}
			}
		}
	}
}

func (ph *ProxyHandler) listenClient(serverInputPipe io.WriteCloser) {
	type ClientReadResult struct {
		clientMessage lspwatch_io.LSPClientMessage
		result        *lspwatch_io.LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())
	internalWg := sync.WaitGroup{}
	internalWg.Add(1)

	defer func() {
		stopReader()
		os.Stdin.Close()
		internalWg.Wait()
		ph.listenersWaitGroup.Done()
		ph.logger.Info("client listener shutdown complete")
	}()

	clientReadResultChan := make(chan ClientReadResult)
	go func(ctx context.Context) {
		defer internalWg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				var clientMessage lspwatch_io.LSPClientMessage
				result := lspwatch_io.ReadLSPMessage(os.Stdin, &clientMessage)
				message := ClientReadResult{
					clientMessage: clientMessage,
					result:        &result,
				}
				if ctx.Err() == nil {
					clientReadResultChan <- message
				}
			}
		}
	}(ctx)

	var shutdownMessage *[]byte

	for {
		select {
		case <-ph.incomingShutdown:
			{
				// If there's an intercepted shutdown message, write it to the server.
				if shutdownMessage != nil {
					_, err := serverInputPipe.Write(*shutdownMessage)
					if err != nil {
						// The caller is expected to use a timeout and eventually
						// forcefully terminate the process if the write fails.
						ph.logger.Errorf("error writing shutdown message to language server stdin: %v", err)
					}
				}
				return
			}
		case res := <-clientReadResultChan:
			{
				result := res.result
				clientMessage := res.clientMessage
				if result.Err == nil {
					// lspwatch ignores all non-request messages from clients
					// e.g (cancellations, progress checks, etc)
					if clientMessage.Id != nil && clientMessage.Method != nil {
						bookmark := RequestBookmark{
							RequestTime: time.Now(),
							Method:      *clientMessage.Method,
						}
						isNewKey := ph.requestBuffer.Set(clientMessage.Id.Value, bookmark)
						if !isNewKey {
							ph.logger.Infof(
								"client request with ID=%v already exists in the buffer",
								clientMessage.Id.Value,
							)
						}
					} else if clientMessage.Method != nil && *clientMessage.Method == "exit" {
						ph.logger.Info("received exit request from client")
						shutdownMessage = result.RawBody
						ph.raiseShutdownRequest()
						// Skip forwarding the shutdown message
						break
					}
				} else {
					ph.logger.Errorf("error parsing message from client: %v", result.Err)
				}

				// Forward message
				_, err := serverInputPipe.Write(*result.RawBody)
				if err != nil {
					ph.logger.Errorf("error forwarding client message to language server stdin: %v", err)
				}
			}
		}
	}
}

func NewProxyHandler(
	exporter telemetry.MetricsExporter,
	cfg *config.LspwatchConfig,
	logger *logrus.Logger,
) (*ProxyHandler, error) {
	var meteredRequests []string
	if cfg.MeteredRequests != nil {
		meteredRequests = *cfg.MeteredRequests
	} else {
		meteredRequests = defaultMeteredRequests
	}

	meteredRequestsMap := make(map[string]struct{})
	for _, requestMethod := range meteredRequests {
		meteredRequestsMap[requestMethod] = struct{}{}
	}

	rh := ProxyHandler{
		meteredRequests:    meteredRequestsMap,
		metricsRegistry:    telemetry.NewMetricsRegistry(exporter, availableLSPMetrics),
		requestBuffer:      orderedmap.NewOrderedMap[string, RequestBookmark](),
		outgoingShutdown:   make(chan struct{}),
		incomingShutdown:   make(chan struct{}),
		logger:             logger,
		listenersWaitGroup: &sync.WaitGroup{},
	}

	err := rh.enableMetrics(cfg)
	if err != nil {
		return nil, fmt.Errorf("error enabling metrics: %v", err)
	}

	return &rh, nil
}
