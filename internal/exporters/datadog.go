package exporters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV2"
	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/io"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/sirupsen/logrus"
)

type DatadogMetricsExporter struct {
	metricsApiClient *datadogV2.MetricsApi
	datadogContext   context.Context
	metricsChan      chan telemetry.MetricRecording
	globalTags       []telemetry.Tag
	wg               *sync.WaitGroup
	logger           *logrus.Logger
	logFile          *os.File
}

var _ telemetry.MetricsExporter = &DatadogMetricsExporter{}

const batchTimeout = 30 * time.Second
const batchSize = 100

// No-op. No need to register metrics for the Datadog exporter.
func (dme *DatadogMetricsExporter) RegisterMetric(registration telemetry.MetricRegistration) error {
	return nil
}

func (dme *DatadogMetricsExporter) EmitMetric(metric telemetry.MetricRecording) error {
	dme.metricsChan <- metric
	return nil
}

func (dme *DatadogMetricsExporter) Start() error {
	dme.wg.Add(1)
	go dme.runMetricsBatchHandler()
	return nil
}

func (dme *DatadogMetricsExporter) Shutdown() error {
	if dme.metricsChan != nil {
		close(dme.metricsChan)
		dme.metricsChan = nil
	}

	return nil
}

func (dme *DatadogMetricsExporter) Wait() {
	dme.wg.Wait()
}

func (dme *DatadogMetricsExporter) Release() error {
	if dme.logFile != nil {
		dme.logFile.Close()
		dme.logFile = nil
	}
	return nil
}

func (dme *DatadogMetricsExporter) SetGlobalTags(tags ...telemetry.Tag) {
	dme.globalTags = tags
}

func NewDatadogMetricsExporter(cfg *config.DatadogConfig) (*DatadogMetricsExporter, error) {
	logger, logFile, err := io.CreateLogger("datadog.log", true)
	if err != nil {
		return nil, fmt.Errorf("error creating datadog logger: %v", err)
	}

	ctx := context.WithValue(
		context.Background(),
		datadog.ContextAPIKeys,
		map[string]datadog.APIKey{
			"apiKeyAuth": {
				Key: os.Getenv(cfg.ClientApiKeyEnvVar),
			},
			"appKeyAuth": {
				Key: os.Getenv(cfg.ClientAppKeyEnvVar),
			},
		},
	)

	if cfg.Site != "" {
		ctx = context.WithValue(
			ctx,
			datadog.ContextServerVariables,
			map[string]string{
				"site": cfg.Site,
			},
		)
	}

	datadogCfg := datadog.NewConfiguration()

	if cfg.DisableCompression != nil {
		datadogCfg.Compress = !*cfg.DisableCompression
	}

	client := datadog.NewAPIClient(datadogCfg)
	metricsApi := datadogV2.NewMetricsApi(client)

	var wg sync.WaitGroup
	metricsChan := make(chan telemetry.MetricRecording)

	exporter := DatadogMetricsExporter{
		metricsApiClient: metricsApi,
		datadogContext:   ctx,
		metricsChan:      metricsChan,
		wg:               &wg,
		logger:           logger,
		logFile:          logFile,
	}

	return &exporter, nil
}

func computeTimeseriesId(metric telemetry.MetricRecording) string {
	tags := *metric.Tags
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := metric.Name + ";"
	for _, k := range keys {
		result += k + "=" + string(tags[k]) + ";"
	}

	hash := sha256.Sum256([]byte(result))
	return hex.EncodeToString(hash[:])
}

func getTagString(key string, value string) string {
	return key + ":" + value
}

func getTags(metric telemetry.MetricRecording) []string {
	tags := []string{}
	for key, value := range *metric.Tags {
		tagString := getTagString(key, string(value))
		tags = append(tags, tagString)
	}
	return tags
}

func (dme *DatadogMetricsExporter) processMetricsBatch(
	batch []telemetry.MetricRecording,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	groupedMetrics := make(map[string][]telemetry.MetricRecording)
	for _, metric := range batch {
		key := computeTimeseriesId(metric)
		var metrics []telemetry.MetricRecording
		if coll, ok := groupedMetrics[key]; ok {
			metrics = coll
		} else {
			metrics = []telemetry.MetricRecording{}
		}
		metrics = append(metrics, metric)
		groupedMetrics[key] = metrics
	}

	timeseriesColl := make([]datadogV2.MetricSeries, 0, len(groupedMetrics))
	for _, metrics := range groupedMetrics {
		metricName := metrics[0].Name
		metricTags := getTags(metrics[0])

		points := []datadogV2.MetricPoint{}
		for _, metricRecording := range metrics {
			point := datadogV2.MetricPoint{
				Timestamp: datadog.PtrInt64(metricRecording.Timestamp),
				Value:     datadog.PtrFloat64(metricRecording.Value),
			}
			points = append(points, point)
		}

		timeseries := datadogV2.NewMetricSeries(metricName, points)
		for _, tag := range dme.globalTags {
			metricTags = append(metricTags, getTagString(tag.Key, string(tag.Value)))
		}
		timeseries.SetTags(metricTags)
		timeseriesColl = append(timeseriesColl, *timeseries)
	}

	payload := datadogV2.NewMetricPayload(timeseriesColl)
	// TODO: Units.
	// TODO: Add deadline.
	_, r, err := dme.metricsApiClient.SubmitMetrics(dme.datadogContext, *payload)
	if err != nil {
		dme.logger.Errorf("error seding metrics batch to Datadog: %v", err)
	} else {
		dme.logger.Infof("received %v response from Datadog", r.Status)
	}

}

func (dme *DatadogMetricsExporter) runMetricsBatchHandler() {
	var internalWg sync.WaitGroup

	defer func() {
		internalWg.Wait()
		dme.wg.Done()
	}()

	var batch []telemetry.MetricRecording
	timer := time.NewTimer(batchTimeout)

	dme.logger.Info("started Datadog exporter")

	for {
		select {
		case <-timer.C: // Timeout
			dme.logger.Info("timeout reached. flushing metrics batch")
			if len(batch) > 0 {
				internalWg.Add(1)
				go dme.processMetricsBatch(batch, &internalWg)
				batch = nil
			}
			timer.Reset(batchTimeout)
		case metric, ok := <-dme.metricsChan:
			if !ok { // Channel closed
				dme.logger.Info("metrics channel closed. flushing metrics batch")
				if len(batch) > 0 {
					internalWg.Add(1)
					go dme.processMetricsBatch(batch, &internalWg)
				}
				return
			}

			batch = append(batch, metric)

			if len(batch) >= batchSize { // Full batch
				internalWg.Add(1)
				dme.logger.Info("full batch reached. flushing.")
				go dme.processMetricsBatch(batch, &internalWg)
				batch = nil
				timer.Reset(batchTimeout)
			}
		}
	}
}
