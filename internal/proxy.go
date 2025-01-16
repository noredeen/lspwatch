package internal

// MVP FEATURES:
// - [ ] export to otel collector or straight to datadog
// - [ ] lspwatch command should work with flags (use `lspwatch [flags] -- [server cmd]`)
//	- [ ] should support --version, --disable
// - [ ] collect resource consumption data for server process
// - [ ] lspwatch should be configurable via a json/yaml file

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/elliotchance/orderedmap/v3"
	"github.com/sirupsen/logrus"
)

type MetricPoint struct {
	Name      string
	Timestamp int
	Value     float64
}

type MetricsExporter interface {
	EmitMetric(metric MetricPoint) error
	Shutdown() error
}

type loggingReader struct {
	r      io.Reader
	Logger *logrus.Logger
}

func (lr *loggingReader) Read(p []byte) (n int, err error) {
	n, err = lr.r.Read(p)
	lr.Logger.Infof("Read %d bytes: %q\n", n, p[:n])
	return n, err
}

var _ io.Reader = &loggingReader{}

type RequestsHandler struct {
	requestBuffer   *orderedmap.OrderedMap[string, time.Time]
	latencyExporter MetricsExporter
}

func NewRequestsHandler(logger *logrus.Logger) (*RequestsHandler, error) {
	otelLatencyExporter, err := NewOTelMetricsExporter(logger)
	if err != nil {
		return nil, err
	}

	return &RequestsHandler{
		requestBuffer:   orderedmap.NewOrderedMap[string, time.Time](),
		latencyExporter: otelLatencyExporter,
	}, nil
}

func (rh *RequestsHandler) ListenServer(
	serverOutputPipe io.ReadCloser,
	logger *logrus.Logger,
	stopChan <-chan struct{},
	wg *sync.WaitGroup,
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
						requestTime, ok := rh.requestBuffer.Get(serverMessage.Id.Value)
						if ok {
							rh.requestBuffer.Delete(serverMessage.Id.Value)
							duration := time.Since(requestTime)
							rh.latencyExporter.EmitMetric(MetricPoint{Value: duration.Seconds()})
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
	logger *logrus.Logger,
	stopChan chan struct{},
	wg *sync.WaitGroup,
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
						isNewKey := rh.requestBuffer.Set(clientMessage.Id.Value, time.Now())
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
