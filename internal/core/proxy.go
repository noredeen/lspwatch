package core

import (
	"context"
	"fmt"
	"io"
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
	inputFromClient    io.ReadCloser
	outputToClient     io.WriteCloser
	inputFromServer    io.ReadCloser
	outputToServer     io.WriteCloser
	outgoingShutdown   chan struct{}
	incomingShutdown   chan struct{}
	listenersWaitGroup *sync.WaitGroup
	logger             *logrus.Logger
	shutdownOnce       sync.Once
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
	ph.shutdownOnce.Do(func() {
		close(ph.incomingShutdown)
	})

	return nil
}

func (ph *ProxyHandler) Wait() {
	ph.listenersWaitGroup.Wait()
	ph.logger.Info("proxy listeners shutdown complete")
}

func (ph *ProxyHandler) Start() {
	ph.listenersWaitGroup.Add(2)
	go ph.listenServer()
	go ph.listenClient()
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

func (ph *ProxyHandler) listenServer() {
	type ServerReadResult struct {
		serverMessage lspwatch_io.LSPServerMessage
		result        *lspwatch_io.LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())
	internalWg := sync.WaitGroup{}
	internalWg.Add(1)

	defer func() {
		stopReader()
		err := ph.inputFromServer.Close()
		if err != nil {
			ph.logger.Errorf("error closing reader for server input: %v", err)
		}
		// TODO: IMO, closing the outputToClient writer should be done in the caller, not here
		err = ph.outputToClient.Close()
		if err != nil {
			ph.logger.Errorf("error closing writer for client output: %v", err)
		}
		internalWg.Wait()
		ph.listenersWaitGroup.Done()
		ph.logger.Info("server listener shutdown complete")
	}()

	serverReadResultChan := make(chan ServerReadResult)
	go func(ctx context.Context) {
		defer internalWg.Done()

		for {
			var serverMessage lspwatch_io.LSPServerMessage
			result := lspwatch_io.ReadLSPMessage(ph.inputFromServer, &serverMessage)
			res := ServerReadResult{
				serverMessage: serverMessage,
				result:        &result,
			}
			select {
			case <-ctx.Done():
				return
			case serverReadResultChan <- res:
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
					break
				}

				// Forward message
				_, err := ph.outputToClient.Write(*result.RawBody)
				if err != nil {
					ph.logger.Errorf("error forwarding server message to client: %v", err)
				}
			}
		}
	}
}

func (ph *ProxyHandler) listenClient() {
	type ClientReadResult struct {
		clientMessage lspwatch_io.LSPClientMessage
		readResult    *lspwatch_io.LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())
	internalWg := sync.WaitGroup{}
	internalWg.Add(1)

	defer func() {
		stopReader()
		err := ph.inputFromClient.Close()
		if err != nil {
			ph.logger.Errorf("error closing reader for client input: %v", err)
		}
		// TODO: IMO, closing the outputToServer writer should be done in the caller, not here
		err = ph.outputToServer.Close()
		if err != nil {
			ph.logger.Errorf("error closing writer for server output: %v", err)
		}
		internalWg.Wait()
		ph.listenersWaitGroup.Done()
		ph.logger.Info("client listener shutdown complete")
	}()

	clientReadResultChan := make(chan ClientReadResult)
	go func(ctx context.Context) {
		defer internalWg.Done()

		for {
			var clientMessage lspwatch_io.LSPClientMessage
			result := lspwatch_io.ReadLSPMessage(ph.inputFromClient, &clientMessage)
			message := ClientReadResult{
				clientMessage: clientMessage,
				readResult:    &result,
			}
			select {
			case <-ctx.Done():
				return
			case clientReadResultChan <- message:
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
					_, err := ph.outputToServer.Write(*shutdownMessage)
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
				readResult := res.readResult
				clientMessage := res.clientMessage
				if readResult.Err == nil {
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
						shutdownMessage = readResult.RawBody
						ph.raiseShutdownRequest()
						// Skip forwarding the shutdown message
						break
					}
				} else {
					ph.logger.Errorf("error reading message from client: %v", readResult.Err)
					break
				}

				// Forward message
				_, err := ph.outputToServer.Write(*readResult.RawBody)
				if err != nil {
					ph.logger.Errorf("error forwarding client message to language server stdin: %v", err)
				}
			}
		}
	}
}

func NewProxyHandler(
	cfg *config.LspwatchConfig,
	metricsRegistry telemetry.MetricsRegistry,
	inputFromClient io.ReadCloser,
	outputToClient io.WriteCloser,
	inputFromServer io.ReadCloser,
	outputToServer io.WriteCloser,
	logger *logrus.Logger,
) (*ProxyHandler, error) {
	meteredRequests := defaultMeteredRequests
	if cfg.MeteredRequests != nil {
		meteredRequests = *cfg.MeteredRequests
	}

	meteredRequestsMap := make(map[string]struct{})
	for _, requestMethod := range meteredRequests {
		meteredRequestsMap[requestMethod] = struct{}{}
	}

	rh := ProxyHandler{
		meteredRequests: meteredRequestsMap,
		metricsRegistry: metricsRegistry,
		requestBuffer:   orderedmap.NewOrderedMap[string, RequestBookmark](),
		inputFromClient: inputFromClient,
		outputToClient:  outputToClient,
		inputFromServer: inputFromServer,
		outputToServer:  outputToServer,
		// Buffer size of 1 to avoid blocking. Caller does not need to be
		// monitoring the shutdown channel.
		outgoingShutdown:   make(chan struct{}, 1),
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
