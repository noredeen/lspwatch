package internal

import (
	"github.com/elliotchance/orderedmap/v3"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"time"
)

type loggingReader struct {
	r      io.Reader
	Logger *log.Logger
}

func (lr *loggingReader) Read(p []byte) (n int, err error) {
	n, err = lr.r.Read(p)
	lr.Logger.Infof("Read %d bytes: %q\n", n, p[:n])
	return n, err
}

var _ io.Reader = &loggingReader{}

type RequestsHandler struct {
	requestBuffer *orderedmap.OrderedMap[string, time.Time]
}

func NewRequestsHandler() *RequestsHandler {
	return &RequestsHandler{orderedmap.NewOrderedMap[string, time.Time]()}
}

// TODO: 1) Start setting up 3rd party o11y connections (otel, datadog, etc)
//	 2) At some point I have to stop ignoring concurrency problems

func (rh *RequestsHandler) ListenServer(
	serverOutputPipe io.ReadCloser,
	logger *log.Logger,
) {
	for {
		var serverMessage LSPServerMessage
		_, rawLspMessage, err := readLSPMessage(serverOutputPipe, &serverMessage)
		if err == nil {
			currentTime := time.Now()

			// In LSP, servers can originate requests (which include a `method` field)
			// in some cases. lspwatch ignores such server requests.
			if serverMessage.Id != nil && serverMessage.Method == nil {
				requestTime, ok := rh.requestBuffer.Get(serverMessage.Id.Value)
				if ok {
					rh.requestBuffer.Delete(serverMessage.Id.Value)
					_ = currentTime.Sub(requestTime)
				} else {
					logger.Infof("Received response for unbuffered request with ID=%v", serverMessage.Id.Value)
				}
			}
		} else {
			logger.Errorf("Failed to parse message from language server: %v", err)
		}

		// Forward message
		_, err = os.Stdout.Write(rawLspMessage)
		if err != nil {
			log.Errorf("Failed to forward server message to client: %v", err)
			continue
		}
	}
}

func (rh *RequestsHandler) ListenClient(
	serverInputPipe io.WriteCloser,
	logger *log.Logger,
) {
	for {
		var clientMessage LSPClientMessage
		_, rawLspMessage, err := readLSPMessage(os.Stdin, &clientMessage)
		if err == nil {
			// lspwatch ignores all non-request messages from clients
			// e.g (cancellations, progress checks, etc)
			if clientMessage.Id != nil && clientMessage.Method != nil {
				isNewKey := rh.requestBuffer.Set(clientMessage.Id.Value, time.Now())
				if !isNewKey {
					log.Infof("Client request with ID=%v already exists in the buffer", clientMessage.Id.Value)
				}
			}
		} else {
			logger.Errorf("Failed to parse message from client: %v", err)
		}

		// Forward message
		_, err = serverInputPipe.Write(rawLspMessage)
		if err != nil {
			log.Errorf("Failed to forward client message to language server stdin: %v", err)
		}
	}
}
