package internal

import (
	"context"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV2"
	"github.com/sirupsen/logrus"
	"os"
	"sync"
	"time"
)

type DatadogMetricsExporter struct {
	metricsApiClient *datadogV2.MetricsApi
	datadogContext   context.Context
}

var _ MetricsExporter = DatadogMetricsExporter{}

// TODO: impl
func (dme DatadogMetricsExporter) EmitMetric(metricPoint MetricPoint) error {
	return nil
}

// TODO: impl
func (dme DatadogMetricsExporter) Shutdown() error {
	return nil
}

func NewDatadogLatencyExporter() *DatadogMetricsExporter {
	ctx := context.WithValue(
		context.Background(),
		datadog.ContextAPIKeys,
		map[string]datadog.APIKey{
			"apiKeyAuth": {
				Key: os.Getenv("DD_CLIENT_API_KEY"),
			},
			"appKeyAuth": {
				Key: os.Getenv("DD_CLIENT_APP_KEY"),
			},
		},
	)
	cfg := datadog.NewConfiguration()
	client := datadog.NewAPIClient(cfg)
	metricsApi := datadogV2.NewMetricsApi(client)

	return &DatadogMetricsExporter{
		metricsApiClient: metricsApi,
		datadogContext:   ctx,
	}
}

func processMetricsBatch(logger *logrus.Logger, batch []MetricPoint, wg *sync.WaitGroup) error {
	// TODO: POST to Datadog
	defer wg.Done()

	//point := datadogV2.NewMetricPoint()
	//points := []datadogV2.MetricPoint{*point}
	//timeseries := *datadogV2.NewMetricSeries("", points)
	//payload := datadogV2.NewMetricPayload(timeseries)
	//metricsApi.SubmitMetrics()
	return nil
}

func runMetricsBatchHandler(
	logger *logrus.Logger,
	metrics chan MetricPoint,
	done chan struct{},
) {
	const timeout = 30 * time.Second
	const batchSize = 100

	var wg sync.WaitGroup

	var batch []MetricPoint
	timer := time.NewTimer(timeout)

	for {
		select {
		case metric, ok := <-metrics:
			if !ok { // Channel closed
				if len(batch) > 0 {
					wg.Add(1)
					go processMetricsBatch(logger, batch, &wg)
					break
				}
			}

			batch = append(batch, metric)

			if len(batch) >= batchSize { // Full batch
				wg.Add(1)
				go processMetricsBatch(logger, batch, &wg)
				batch = nil
				timer.Reset(timeout)
			}

		case <-timer.C: // Timeout
			if len(batch) > 0 {
				wg.Add(1)
				go processMetricsBatch(logger, batch, &wg)
				batch = nil
			}
			timer.Reset(timeout)

		case <-done:
			logger.Info("Datadog exporter shutting down...")
			break
		}
	}

	wg.Wait()
}
