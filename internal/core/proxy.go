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
	// Metrics and request buffer.
	meteredRequests map[string]struct{}
	metricsRegistry telemetry.MetricsRegistry
	requestBuffer   *orderedmap.OrderedMap[string, RequestBookmark]

	// Main readers/writers.
	inputFromClient io.ReadCloser
	outputToClient  io.WriteCloser
	inputFromServer io.ReadCloser
	outputToServer  io.WriteCloser

	// For controlling where data from the server flows to inside lspwatch.
	serverDataDiverterPipe lspwatch_io.SingleUseDiverterPipe
	// Reader/writer for lspwatch command mode.
	serverPassThroughReader io.ReadCloser
	serverPassThroughWriter io.WriteCloser
	// Reader/writer for lspwatch proxy mode.
	serverLspReader io.ReadCloser
	serverLspWriter io.WriteCloser

	// Ensure diversion of server data flow happens once.
	switchOnce sync.Once

	// Lifecycle management.
	outgoingShutdown chan struct{}
	incomingShutdown chan struct{}
	// Must be unbuffered to ensure synchronization.
	modeSwitchHandshake chan struct{}
	listenersWaitGroup  *sync.WaitGroup
	shutdownOnce        sync.Once
	mode                string
	logger              *logrus.Logger
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
	ph.serverDataDiverterPipe.Start()

	displayMode := ph.mode
	if displayMode == "" {
		displayMode = "command"
	}

	ph.logger.Infof("starting proxy handler in %q mode", displayMode)

	if ph.mode == "proxy" {
		ph.listenersWaitGroup.Add(2)
		go ph.listenClient()
		go ph.listenServer()
	} else if ph.mode == "command" {
		go ph.passThroughServerBytes()
	} else {
		ph.listenersWaitGroup.Add(2)
		go ph.listenClient()
		go ph.listenServer()
		go ph.passThroughServerBytes()
	}

	ph.logger.Info("proxy listeners started")
}

func (ph *ProxyHandler) SwitchedToProxyMode() chan struct{} {
	return ph.modeSwitchHandshake
}

// Blocks until the proxy handler receives the confirmation.
func (ph *ProxyHandler) ConfirmProxyMode() {
	ph.modeSwitchHandshake <- struct{}{}
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

func (ph *ProxyHandler) passThroughServerBytes() {
	for {
		buf := make([]byte, 1024)
		n, err := ph.serverPassThroughReader.Read(buf)
		if err != nil {
			ph.logger.Errorf("error reading from server pass through reader: %v", err)
			break
		}
		ph.logger.Infof("read %d bytes from server pass through reader", n)
		os.Stdout.Write(buf[:n])
	}
}

func (ph *ProxyHandler) listenServer() {
	type ServerReadResult struct {
		serverMessage lspwatch_io.LSPServerMessage
		result        *lspwatch_io.LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())

	defer func() {
		stopReader()
		err := ph.inputFromServer.Close()
		if err != nil {
			ph.logger.Errorf("error closing reader for server input: %v", err)
		}
		// TODO: Closing the outputToServer writer should be done in the caller, not here?
		err = ph.outputToClient.Close()
		if err != nil {
			ph.logger.Errorf("error closing writer for client output: %v", err)
		}
		err = ph.serverLspReader.Close()
		if err != nil {
			ph.logger.Errorf("error closing server LSP reader: %v", err)
		}
		err = ph.serverLspWriter.Close()
		if err != nil {
			ph.logger.Errorf("error closing server LSP writer: %v", err)
		}

		ph.listenersWaitGroup.Done()
		ph.logger.Info("server listener shutdown complete")
	}()

	lspReader := lspwatch_io.NewLSPReader(ph.serverLspReader)
	serverReadResultChan := make(chan ServerReadResult)
	go func(ctx context.Context) {
		for {
			var serverMessage lspwatch_io.LSPServerMessage
			result := lspReader.Read(&serverMessage)
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
				if result.Err != nil {
					if result.Err == io.EOF {
						ph.logger.Info("server closed connection")
						ph.raiseShutdownRequest()
						return
					}

					ph.logger.Errorf("error reading message from language server: %v", result.Err)
					continue
				}

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
								ph.logger.Infof("emitting metric %q", requestDurationMetric.Name)
								err := ph.metricsRegistry.EmitMetric(requestDurationMetric)
								if err != nil {
									ph.logger.Errorf("error emitting metric: %v", err)
								}
							}
						}
					} else {
						ph.logger.Infof(
							"received client response for unbuffered request with ID=%q",
							serverMessage.Id.Value,
						)
					}
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

	defer func() {
		stopReader()
		err := ph.inputFromClient.Close()
		if err != nil {
			ph.logger.Errorf("error closing reader for client input: %v", err)
		}
		// TODO: Closing the outputToServer writer should be done in the caller, not here?
		err = ph.outputToServer.Close()
		if err != nil {
			ph.logger.Errorf("error closing writer for server output: %v", err)
		}
		ph.listenersWaitGroup.Done()
		ph.logger.Info("client listener shutdown complete")
	}()

	lspReader := lspwatch_io.NewLSPReader(ph.inputFromClient)
	clientReadResultChan := make(chan ClientReadResult)
	go func(ctx context.Context) {
		for {
			var clientMessage lspwatch_io.LSPClientMessage
			result := lspReader.Read(&clientMessage)
			res := ClientReadResult{
				clientMessage: clientMessage,
				readResult:    &result,
			}
			select {
			case <-ctx.Done():
				return
			case clientReadResultChan <- res:
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
				if readResult.Err != nil {
					if readResult.Err == io.EOF {
						ph.logger.Info("client closed connection")
						// Notably, no ph.raiseShutdownRequest() here.
						// A client closing the connection is not considered an error.
						// lspwatch is to continue its server-watching duties.
						return
					}

					ph.logger.Errorf("error reading message from client: %v", readResult.Err)
					continue
				}

				// Client has sent an LSP message, which kicks off normal LSP communication.
				// So, stop pass-through of server bytes and start LSP processing.
				ph.switchToProxyModeOnce()

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
					continue
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

func (ph *ProxyHandler) switchToProxyModeOnce() {
	ph.switchOnce.Do(func() {
		ph.modeSwitchHandshake <- struct{}{}

		// Stop pass-through of server bytes.
		ph.serverPassThroughWriter.Close()
		ph.serverPassThroughReader.Close()
		ph.serverDataDiverterPipe.Switch()

		<-ph.modeSwitchHandshake
	})
}

func NewProxyHandler(
	cfg *config.LspwatchConfig,
	metricsRegistry telemetry.MetricsRegistry,
	inputFromClient io.ReadCloser,
	outputToClient io.WriteCloser,
	inputFromServer io.ReadCloser,
	outputToServer io.WriteCloser,
	mode string,
	logger *logrus.Logger,
) (*ProxyHandler, error) {
	switch mode {
	case "command", "proxy", "":
	default:
		return nil, fmt.Errorf("invalid mode: '%s'", mode)
	}

	meteredRequests := defaultMeteredRequests
	if cfg.MeteredRequests != nil {
		meteredRequests = *cfg.MeteredRequests
	}

	meteredRequestsMap := make(map[string]struct{})
	for _, requestMethod := range meteredRequests {
		meteredRequestsMap[requestMethod] = struct{}{}
	}

	serverPassThroughReader, serverPassThroughWriter := io.Pipe()
	serverLspReader, serverLspWriter := io.Pipe()
	serverDataDiverterPipe := lspwatch_io.NewSingleUseDiverterPipe(
		inputFromServer,
		serverPassThroughWriter,
		serverLspWriter,
	)

	rh := ProxyHandler{
		meteredRequests: meteredRequestsMap,
		metricsRegistry: metricsRegistry,
		requestBuffer:   orderedmap.NewOrderedMap[string, RequestBookmark](),

		inputFromClient: inputFromClient,
		outputToClient:  outputToClient,
		inputFromServer: inputFromServer,
		outputToServer:  outputToServer,

		serverDataDiverterPipe:  serverDataDiverterPipe,
		serverPassThroughReader: serverPassThroughReader,
		serverPassThroughWriter: serverPassThroughWriter,
		serverLspReader:         serverLspReader,
		serverLspWriter:         serverLspWriter,
		switchOnce:              sync.Once{},

		// Buffer size of 1 to avoid blocking. Caller does not need to be
		// monitoring the shutdown channel.
		outgoingShutdown:    make(chan struct{}, 1),
		incomingShutdown:    make(chan struct{}),
		modeSwitchHandshake: make(chan struct{}),
		mode:                mode,
		logger:              logger,
		listenersWaitGroup:  &sync.WaitGroup{},
	}

	err := rh.enableMetrics(cfg)
	if err != nil {
		return nil, fmt.Errorf("error enabling metrics: %v", err)
	}

	return &rh, nil
}
