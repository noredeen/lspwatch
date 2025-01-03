package internal

import (
	"fmt"
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

func (rh *RequestsHandler) ListenServer(
	serverOutputPipe io.ReadCloser,
	logger *log.Logger,
) {
	// reader := bufio.NewReader(serverOutputPipe)

	for {
		var lspResponse LSPResponseMessage
		_, rawLspResponse, err := readLSPMessage(&loggingReader{r: serverOutputPipe, Logger: logger}, lspResponse)
		if err != nil {
			logger.Errorf("Failed to parse message from language server: %v", err)
			continue
		}

		logger.Info("Received message from server!!!")

		_, err = fmt.Print(rawLspResponse)
		if err != nil {
			log.Errorf("Failed to forward server message to client: %v", err)
			continue
		}

		requestTime, ok := rh.requestBuffer.Get(lspResponse.Id.Value)
		if !ok {
			logger.Error("Received response for nonexistent request")
			continue
		}

		latency := time.Now().Sub(requestTime)
		logger.Infof("REQUEST LATENCY: %v", latency.Seconds())
	}
}

func (rh *RequestsHandler) ListenClient(
	serverInputPipe io.WriteCloser,
	logger *log.Logger,
) {
	// reader := bufio.NewReader(os.Stdin)

	for {
		var lspRequest LSPRequestMessage
		headers, rawLspRequest, err := readLSPMessage(&loggingReader{r: os.Stdin, Logger: logger}, &lspRequest)
		if err != nil {
			logger.Errorf("Failed to parse message from client: %v", err)
			continue
		}

		logger.Info("Received message from client!!!")
		logger.Info("HEADERS: %v", headers)

		requestArrivalTime := time.Now()

		rh.requestBuffer.Set(lspRequest.Id.Value, requestArrivalTime)

		n, err := serverInputPipe.Write(rawLspRequest)
		if err != nil {
			log.Errorf("Failed to forward client message to language server stdin: %v", err)
		}

		logger.Infof("Wrote %v bytes to server stdin", n)
	}
}
