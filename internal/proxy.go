package internal

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/elliotchance/orderedmap/v3"
	log "github.com/sirupsen/logrus"
)

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
    reader := bufio.NewReader(serverOutputPipe)
    for {
        var lspResponse LSPResponseMessage
        _, rawLspResponse, err := readLSPMessage(reader, lspResponse)
        if err != nil {
            logger.Errorf("Failed to parse message from language server: %v", err)
            continue
        }

        _, err = fmt.Print(rawLspResponse)
        if err != nil {
            log.Errorf("Failed to forward server message to client: %v", err)
            continue
        }
        
        requestTime, ok := rh.requestBuffer.Get(lspResponse.Id)
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
    reader := bufio.NewReader(os.Stdin)

    for {
        var lspRequest LSPRequestMessage
        _, rawLspRequest, err := readLSPMessage(reader, &lspRequest)
        if err != nil {
            logger.Errorf("Failed to parse message from client: %v", err)
            continue
        }

        requestArrivalTime := time.Now()

        rh.requestBuffer.Set(lspRequest.Id, requestArrivalTime)

        _, err = serverInputPipe.Write(rawLspRequest)
        if err != nil {
            log.Errorf("Failed to forward client message to language server stdin: %v", err)
        }
    }
}
