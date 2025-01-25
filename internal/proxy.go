package internal

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/elliotchance/orderedmap/v3"
	"github.com/sirupsen/logrus"
)

// TODO: This file needs cleaning up

type RequestBookmark struct {
	RequestTime time.Time
	Method      *string
}

type RequestsHandler struct {
	MetricsRegistry *MetricsRegistry
	requestBuffer   *orderedmap.OrderedMap[string, RequestBookmark]
}

var availableLSPMetrics = map[AvailableMetric]MetricRegistration{
	RequestDuration: {
		Kind:        Histogram,
		Name:        "lspwatch.request.duration",
		Description: "Duration of LSP request",
		Unit:        "s",
	},
	// TODO: Move to the module responsible for server metrics
	// ServerRSS: {
	// 	Kind:        Histogram,
	// 	Name:        "lspwatch.server.rss",
	// 	Description: "RSS of the language server process",
	// 	Unit:        "bytes", // TODO: Check if this is correct
	// },
}

// TODO: Move to MetricsRegistry
func (rh *RequestsHandler) registerMetrics(cfg *LspwatchConfig) error {
	// Default behavior if `metrics` is not specified in the config
	if cfg.Metrics == nil {
		err := rh.MetricsRegistry.RegisterMetric(RequestDuration)
		if err != nil {
			return err
		}
		return nil
	}

	for _, metric := range *cfg.Metrics {
		err := rh.MetricsRegistry.RegisterMetric(AvailableMetric(metric))
		if err != nil {
			return err
		}
	}

	return nil
}

func NewRequestsHandler(cfg *LspwatchConfig, logger *logrus.Logger) (*RequestsHandler, error) {
	var exporter MetricsExporter

	switch cfg.Exporter {
	case "opentelemetry":
		otelExporter, err := NewMetricsOTelExporter(cfg.OpenTelemetry, logger)
		if err != nil {
			return nil, fmt.Errorf("error creating OpenTelemetry exporter: %v", err)
		}
		exporter = otelExporter
	case "datadog":
		datadogExporter, err := NewDatadogMetricsExporter(cfg.Datadog)
		if err != nil {
			return nil, fmt.Errorf("error creating Datadog exporter: %v", err)
		}
		exporter = datadogExporter
	default:
		return nil, fmt.Errorf("invalid exporter: %v", cfg.Exporter)
	}

	rh := RequestsHandler{
		MetricsRegistry: NewMetricsRegistry(exporter, availableLSPMetrics),
		requestBuffer:   orderedmap.NewOrderedMap[string, RequestBookmark](),
	}

	err := rh.registerMetrics(cfg)
	if err != nil {
		return nil, fmt.Errorf("error registering metrics: %v", err)
	}

	return &rh, nil
}

func (rh *RequestsHandler) ListenServer(
	serverOutputPipe io.ReadCloser,
	stopChan <-chan struct{},
	wg *sync.WaitGroup,
	logger *logrus.Logger,
) {
	type ServerReadResult struct {
		serverMessage LSPServerMessage
		result        *LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())
	internalWg := sync.WaitGroup{}
	internalWg.Add(1)

	defer func() {
		stopReader()
		serverOutputPipe.Close()
		internalWg.Wait()
		wg.Done()
	}()

	serverReadResultChan := make(chan ServerReadResult)
	go func(ctx context.Context) {
		defer internalWg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				var serverMessage LSPServerMessage
				result := readLSPMessage(serverOutputPipe, &serverMessage)
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
		case <-stopChan:
			{
				return
			}
		case res := <-serverReadResultChan:
			{
				serverMessage := res.serverMessage
				result := res.result
				if result.err == nil {
					// In LSP, servers can originate requests (which include a `method` field)
					// in some cases. lspwatch ignores such server requests.
					if serverMessage.Id != nil && serverMessage.Method == nil {
						requestBookmark, ok := rh.requestBuffer.Get(serverMessage.Id.Value)
						if ok {
							rh.requestBuffer.Delete(serverMessage.Id.Value)

							if rh.MetricsRegistry.IsMetricEnabled(RequestDuration) {
								duration := time.Since(requestBookmark.RequestTime)
								requestDurationMetric := NewMetricRecording(
									"lspwatch.request.duration",
									time.Now().Unix(),
									duration.Seconds(),
									NewTag("method", *requestBookmark.Method),
								)
								err := rh.MetricsRegistry.EmitMetric(requestDurationMetric)
								if err != nil {
									logger.Errorf("error emitting metric: %v", err)
								}
							}

						} else {
							logger.Infof("received response for unbuffered request with ID=%v", serverMessage.Id.Value)
						}
					}
				} else {
					logger.Errorf("error parsing message from language server: %v", result.err)
				}

				// Forward message
				_, err := os.Stdout.Write(*result.rawBody)
				if err != nil {
					logger.Errorf("error forwarding server message to client: %v", err)
				}
			}
		}
	}
}

func (rh *RequestsHandler) ListenClient(
	serverInputPipe io.WriteCloser,
	stopChan chan struct{},
	wg *sync.WaitGroup,
	logger *logrus.Logger,
) {
	type ClientReadResult struct {
		clientMessage LSPClientMessage
		result        *LSPReadResult
	}

	ctx, stopReader := context.WithCancel(context.Background())
	internalWg := sync.WaitGroup{}
	internalWg.Add(1)

	defer func() {
		stopReader()
		os.Stdin.Close()
		internalWg.Wait()
		wg.Done()
	}()

	clientReadResultChan := make(chan ClientReadResult)
	go func(ctx context.Context) {
		defer internalWg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				var clientMessage LSPClientMessage
				result := readLSPMessage(os.Stdin, &clientMessage)
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

	for {
		select {
		case <-stopChan:
			{
				return
			}
		case res := <-clientReadResultChan:
			{
				result := res.result
				clientMessage := res.clientMessage
				if result.err == nil {
					// lspwatch ignores all non-request messages from clients
					// e.g (cancellations, progress checks, etc)
					if clientMessage.Id != nil && clientMessage.Method != nil {
						bookmark := RequestBookmark{
							RequestTime: time.Now(),
							Method:      clientMessage.Method,
						}
						isNewKey := rh.requestBuffer.Set(clientMessage.Id.Value, bookmark)
						if !isNewKey {
							logger.Infof("client request with ID=%v already exists in the buffer", clientMessage.Id.Value)
						}
					} else if clientMessage.Method != nil && *clientMessage.Method == "exit" {
						logger.Info("received exit request from client")
						stopChan <- struct{}{}
						return
					}
				} else {
					logger.Errorf("error parsing message from client: %v", result.err)
				}

				// Forward message
				_, err := serverInputPipe.Write(*result.rawBody)
				if err != nil {
					logger.Errorf("error forwarding client message to language server stdin: %v", err)
				}
			}
		}
	}
}
