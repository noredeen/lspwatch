package internal

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
	"github.com/sirupsen/logrus"
)

type DatadogMetricsExporter struct {
	metricsApiClient *datadogV2.MetricsApi
	datadogContext   context.Context
	metricsChan      chan MetricRecording
	wg               *sync.WaitGroup
	logger           *logrus.Logger
	logFile          *os.File
}

var _ MetricsExporter = &DatadogMetricsExporter{}

// No-op. No need to register metrics for the Datadog exporter.
func (dme *DatadogMetricsExporter) RegisterMetric(registration MetricRegistration) error {
	return nil
}

func (dme *DatadogMetricsExporter) EmitMetric(metricPoint MetricRecording) error {
	dme.metricsChan <- metricPoint
	return nil
}

func (dme *DatadogMetricsExporter) Shutdown() error {
	close(dme.metricsChan)
	dme.wg.Wait()
	if dme.logFile != nil {
		dme.logFile.Close()
	}
	return nil
}

func NewDatadogMetricsExporter(cfg *datadogConfig) (*DatadogMetricsExporter, error) {
	logger, logFile, err := CreateLogger("datadog.log", true)
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
	metricsChan := make(chan MetricRecording)

	exporter := DatadogMetricsExporter{
		metricsApiClient: metricsApi,
		datadogContext:   ctx,
		metricsChan:      metricsChan,
		wg:               &wg,
		logger:           logger,
		logFile:          logFile,
	}

	wg.Add(1)
	go exporter.runMetricsBatchHandler(&wg)

	return &exporter, nil
}

func computeTimeseriesId(metric MetricRecording) string {
	tags := *metric.Tags
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := metric.Name + ";"
	for _, k := range keys {
		result += k + "=" + tags[k] + ";"
	}

	hash := sha256.Sum256([]byte(result))
	return hex.EncodeToString(hash[:])
}

func getTags(metric MetricRecording) []string {
	tags := []string{}
	for key, value := range *metric.Tags {
		tagString := key + ":" + value
		tags = append(tags, tagString)
	}
	return tags
}

func (dme *DatadogMetricsExporter) processMetricsBatch(
	batch []MetricRecording,
	wg *sync.WaitGroup,
) {
	defer wg.Done()

	groupedMetrics := make(map[string][]MetricRecording)
	for _, metric := range batch {
		key := computeTimeseriesId(metric)
		var metrics []MetricRecording
		if coll, ok := groupedMetrics[key]; ok {
			metrics = coll
		} else {
			metrics = []MetricRecording{}
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
		timeseries.SetTags(metricTags)
		timeseriesColl = append(timeseriesColl, *timeseries)
	}

	payload := datadogV2.NewMetricPayload(timeseriesColl)
	// TODO: add deadline
	_, r, err := dme.metricsApiClient.SubmitMetrics(dme.datadogContext, *payload)
	if err != nil {
		dme.logger.Errorf("error seding metrics batch to Datadog: %v", err)
	}
	dme.logger.Infof("full http response (%v) from datadog: %v", r.Status, r)
}

func (dme *DatadogMetricsExporter) runMetricsBatchHandler(wg *sync.WaitGroup) {
	defer wg.Done()

	const timeout = 30 * time.Second
	const batchSize = 100

	var internalWg sync.WaitGroup
	defer internalWg.Wait()

	var batch []MetricRecording
	timer := time.NewTimer(timeout)

	dme.logger.Info("started datadog exporter...")

	for {
		select {
		case <-timer.C: // Timeout
			dme.logger.Info("timeout reached. flushing metrics batch")
			if len(batch) > 0 {
				internalWg.Add(1)
				go dme.processMetricsBatch(batch, &internalWg)
				batch = nil
			}
			timer.Reset(timeout)
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
				timer.Reset(timeout)
			}
		}
	}
}
