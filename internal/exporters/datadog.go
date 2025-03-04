package exporters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
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

// TODO: Make these configurable.
const defaultBatchSize = 100
const defaultBatchTimeout = 30 * time.Second

type metricsProcessor interface {
	setGlobalTags(tags ...telemetry.Tag)
	processBatch(batch []telemetry.MetricRecording, wg *sync.WaitGroup, logger *logrus.Logger)
}

type metricsApiClient interface {
	SubmitMetrics(
		ctx context.Context,
		body datadogV2.MetricPayload,
		o ...datadogV2.SubmitMetricsOptionalParameters,
	) (datadogV2.IntakePayloadAccepted, *http.Response, error)
}

type defaultMetricsProcessor struct {
	metricsApiClient metricsApiClient
	datadogContext   context.Context
	globalTags       []telemetry.Tag
}

type DatadogMetricsExporter struct {
	processor    metricsProcessor
	metricsChan  chan telemetry.MetricRecording
	batchSize    int
	batchTimeout time.Duration
	wg           *sync.WaitGroup
	logger       *logrus.Logger
	logFile      *os.File

	mu      *sync.Mutex
	running bool
}

var _ metricsProcessor = &defaultMetricsProcessor{}
var _ telemetry.MetricsExporter = &DatadogMetricsExporter{}

func (dmp *defaultMetricsProcessor) setGlobalTags(tags ...telemetry.Tag) {
	dmp.globalTags = tags
}

// TODO: This should be refactored to use a context.
func (dmp *defaultMetricsProcessor) processBatch(
	batch []telemetry.MetricRecording,
	wg *sync.WaitGroup,
	logger *logrus.Logger,
) {
	defer wg.Done()

	timeseriesColl := getTimeseries(batch, dmp.globalTags)
	payload := datadogV2.NewMetricPayload(timeseriesColl)
	// TODO: Units.
	// TODO: Add deadline.
	_, r, err := dmp.metricsApiClient.SubmitMetrics(dmp.datadogContext, *payload)
	if err != nil {
		logger.Errorf("error seding metrics batch to Datadog: %v", err)
	} else {
		logger.Infof("received %v response from Datadog", r.Status)
	}
}

// No-op. No need to register metrics for the Datadog exporter.
// An exporter is managed through a MetricsRegistry in lspwatch, which
// takes care of managing enabled/disabled metrics from the config.
func (dme *DatadogMetricsExporter) RegisterMetric(registration telemetry.MetricRegistration) error {
	return nil
}

func (dme *DatadogMetricsExporter) EmitMetric(metric telemetry.MetricRecording) error {
	dme.metricsChan <- metric
	return nil
}

func (dme *DatadogMetricsExporter) Start() error {
	dme.mu.Lock()
	defer dme.mu.Unlock()

	if dme.running {
		return nil
	}

	dme.wg.Add(1)
	go dme.runMetricsBatchHandler()
	dme.running = true
	return nil
}

func (dme *DatadogMetricsExporter) Shutdown() error {
	dme.mu.Lock()
	defer dme.mu.Unlock()

	if !dme.running {
		return nil
	}

	close(dme.metricsChan)

	dme.running = false
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
	dme.processor.setGlobalTags(tags...)
}

func (dme *DatadogMetricsExporter) runMetricsBatchHandler() {
	var internalWg sync.WaitGroup

	defer func() {
		internalWg.Wait()
		dme.wg.Done()
	}()

	var batch []telemetry.MetricRecording
	timer := time.NewTimer(dme.batchTimeout)

	dme.logger.Info("started Datadog exporter")

	for {
		select {
		case <-timer.C: // Timeout
			dme.logger.Info("timeout reached. flushing metrics batch")
			if len(batch) > 0 {
				internalWg.Add(1)
				go dme.processor.processBatch(batch, &internalWg, dme.logger)
				batch = nil
			}
			timer.Reset(dme.batchTimeout)
		case metric, ok := <-dme.metricsChan:
			if !ok { // Channel closed
				dme.logger.Info("metrics channel closed. flushing metrics batch")
				if len(batch) > 0 {
					internalWg.Add(1)
					go dme.processor.processBatch(batch, &internalWg, dme.logger)
				}
				return
			}

			batch = append(batch, metric)

			if len(batch) >= dme.batchSize { // Full batch
				internalWg.Add(1)
				dme.logger.Info("full batch reached. flushing.")
				go dme.processor.processBatch(batch, &internalWg, dme.logger)
				batch = nil
				timer.Reset(dme.batchTimeout)
			}
		}
	}
}

// Use GetDatadogContext to create a context with the Datadog API keys.
func NewDatadogMetricsExporter(
	datadogCtx context.Context,
	cfg *config.DatadogConfig,
	logDir string,
) (*DatadogMetricsExporter, error) {
	logger, logFile, err := io.CreateLogger(logDir, "datadog.log")
	if err != nil {
		return nil, fmt.Errorf("error creating datadog logger: %v", err)
	}

	datadogCfg := datadog.NewConfiguration()
	if cfg.DisableCompression != nil {
		datadogCfg.Compress = !*cfg.DisableCompression
	}

	client := datadog.NewAPIClient(datadogCfg)
	metricsApi := datadogV2.NewMetricsApi(client)

	batchSize := defaultBatchSize
	if cfg.BatchSize != nil {
		batchSize = *cfg.BatchSize
	}
	batchTimeout := defaultBatchTimeout
	if cfg.BatchTimeout != nil {
		batchTimeout = time.Duration(*cfg.BatchTimeout) * time.Second
	}

	var wg sync.WaitGroup
	metricsChan := make(chan telemetry.MetricRecording)
	exporter := DatadogMetricsExporter{
		processor: &defaultMetricsProcessor{
			metricsApiClient: metricsApi,
			datadogContext:   datadogCtx,
		},
		metricsChan:  metricsChan,
		batchSize:    batchSize,
		batchTimeout: batchTimeout,
		mu:           &sync.Mutex{},
		wg:           &wg,
		logger:       logger,
		logFile:      logFile,
	}

	return &exporter, nil
}

func GetDatadogContext(cfg *config.DatadogConfig) context.Context {
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

	return ctx
}

func getTimeseries(batch []telemetry.MetricRecording, globalTags []telemetry.Tag) []datadogV2.MetricSeries {
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
		for _, tag := range globalTags {
			metricTags = append(metricTags, getTagString(tag.Key, string(tag.Value)))
		}
		timeseries.SetTags(metricTags)
		timeseriesColl = append(timeseriesColl, *timeseries)
	}

	return timeseriesColl
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
