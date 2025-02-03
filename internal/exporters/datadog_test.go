package exporters

import (
	"context"
	"io"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV2"
	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/noredeen/lspwatch/internal/testutil"
	"github.com/sirupsen/logrus"
)

// TODO: Write some helpers to reduce code duplication.

type mockMetricsApi struct {
	submitMetricsCalled bool
	submitMetricsInput  datadogV2.MetricPayload
	submitMetricsError  error
}

type processBatchCall struct {
	batchArg  []telemetry.MetricRecording
	wgArg     *sync.WaitGroup
	loggerArg *logrus.Logger
}

type mockMetricsProcessor struct {
	processBatchCalls []processBatchCall
}

func (m *mockMetricsProcessor) processBatch(
	batch []telemetry.MetricRecording,
	wg *sync.WaitGroup,
	logger *logrus.Logger,
) {
	defer wg.Done()
	m.processBatchCalls = append(
		m.processBatchCalls,
		processBatchCall{
			batchArg:  batch,
			wgArg:     wg,
			loggerArg: logger,
		},
	)
}

func (m *mockMetricsProcessor) setGlobalTags(tags ...telemetry.Tag) {}

func (m *mockMetricsApi) SubmitMetrics(
	ctx context.Context,
	body datadogV2.MetricPayload,
	o ...datadogV2.SubmitMetricsOptionalParameters,
) (datadogV2.IntakePayloadAccepted, *http.Response, error) {
	m.submitMetricsCalled = true
	m.submitMetricsInput = body

	resp := &http.Response{
		StatusCode: http.StatusAccepted,
		Status:     "202 Accepted",
	}

	if m.submitMetricsError != nil {
		return datadogV2.IntakePayloadAccepted{}, resp, m.submitMetricsError
	}

	return datadogV2.IntakePayloadAccepted{}, resp, nil
}

func TestGetDatadogContext(t *testing.T) {
	apiKey := "test-api-key"
	appKey := "test-app-key"
	os.Setenv("TEST_DATADOG_API_KEY", apiKey)
	os.Setenv("TEST_DATADOG_APP_KEY", appKey)

	t.Run("no site", func(t *testing.T) {
		cfg := &config.DatadogConfig{
			ClientApiKeyEnvVar: "TEST_DATADOG_API_KEY",
			ClientAppKeyEnvVar: "TEST_DATADOG_APP_KEY",
		}

		ctx := GetDatadogContext(cfg)
		ctxKeys, ok := ctx.Value(datadog.ContextAPIKeys).(map[string]datadog.APIKey)
		if !ok {
			t.Fatalf("expected api keys to be set in Datadog context")
		}

		if ctxKeys["apiKeyAuth"].Key != apiKey {
			t.Errorf(
				"expected apiKeyAuth value in Datadog context to be %s, got %s",
				apiKey,
				ctxKeys["apiKeyAuth"].Key,
			)
		}

		if ctxKeys["appKeyAuth"].Key != appKey {
			t.Errorf(
				"expected appKeyAuth value in Datadog context to be %s, got %s",
				appKey,
				ctxKeys["appKeyAuth"].Key,
			)
		}

		if ctx.Value(datadog.ContextServerVariables) != nil {
			t.Errorf("expected server variables to be nil, got %v", ctx.Value(datadog.ContextServerVariables))
		}
	})

	t.Run("with site", func(t *testing.T) {
		site := "test-site"
		cfg := &config.DatadogConfig{
			ClientApiKeyEnvVar: "TEST_DATADOG_API_KEY",
			ClientAppKeyEnvVar: "TEST_DATADOG_APP_KEY",
			Site:               "test-site",
		}

		ctx := GetDatadogContext(cfg)
		ctxServerVars, ok := ctx.Value(datadog.ContextServerVariables).(map[string]string)
		if !ok {
			t.Fatalf("expected context server variables to be set in Datadog context")
		}

		if ctxServerVars["site"] != site {
			t.Errorf("expected site to be %s, got %s", site, ctxServerVars["site"])
		}
	})
}

func TestDefaultMetricsProcessor_getTimeseries(t *testing.T) {
	batch := []telemetry.MetricRecording{
		telemetry.NewMetricRecording(
			"test.first_metric",
			time.Now().Unix(),
			1.5,
			telemetry.NewTag("tag1", "value1"),
		),
		telemetry.NewMetricRecording(
			"test.first_metric",
			time.Now().Unix()+1,
			1.5,
			telemetry.NewTag("tag1", "value1"),
		),
		telemetry.NewMetricRecording(
			"test.first_metric",
			time.Now().Unix()+2,
			2.0,
			telemetry.NewTag("tag1", "value1"),
			telemetry.NewTag("tag2", "value2"),
		),
		telemetry.NewMetricRecording(
			"test.second_metric",
			time.Now().Unix(),
			2.5,
			telemetry.NewTag("tag1", "value1"),
		),
	}

	timeseries := getTimeseries(batch, []telemetry.Tag{
		telemetry.NewTag("global_tag", "global_value"),
	})

	if len(timeseries) != 3 {
		t.Fatalf("expected getTimeseries to return 3 timeseries, got %d", len(timeseries))
	}

	firstTimeseriesGood := false
	secondTimeseriesGood := false
	thirdTimeseriesGood := false
	for _, ts := range timeseries {
		if ts.Metric == "test.first_metric" {
			if len(ts.Tags) == 2 &&
				slices.Contains(ts.Tags, getTagString("tag1", "value1")) &&
				slices.Contains(ts.Tags, getTagString("global_tag", "global_value")) {
				if firstTimeseriesGood {
					t.Errorf("expected only one timeseries with metric 'test.first_metric' and tags 'tag1=value1' and 'global_tag=global_value'")
				}
				firstTimeseriesGood = true
			}

			if len(ts.Tags) == 3 &&
				slices.Contains(ts.Tags, getTagString("tag1", "value1")) &&
				slices.Contains(ts.Tags, getTagString("tag2", "value2")) &&
				slices.Contains(ts.Tags, getTagString("global_tag", "global_value")) {
				if secondTimeseriesGood {
					t.Errorf("expected only one timeseries with metric 'test.first_metric' and tags 'tag1=value1', 'tag2=value2', and 'global_tag=global_value'")
				}
				secondTimeseriesGood = true
			}
		} else if ts.Metric == "test.second_metric" {
			if len(ts.Tags) == 2 &&
				slices.Contains(ts.Tags, getTagString("tag1", "value1")) &&
				slices.Contains(ts.Tags, getTagString("global_tag", "global_value")) {
				if thirdTimeseriesGood {
					t.Errorf("expected only one timeseries with metric 'test.second_metric' and tags 'tag1=value1' and 'global_tag=global_value'")
				}
				thirdTimeseriesGood = true
			}
		}
	}

	if !firstTimeseriesGood {
		t.Errorf("expected timeseries with metric 'test.first_metric' and tags 'tag1=value1' and 'global_tag=global_value'")
	}

	if !secondTimeseriesGood {
		t.Errorf("expected timeseries with metric 'test.first_metric' and tags 'tag1=value1', 'tag2=value2', and 'global_tag=global_value'")
	}

	if !thirdTimeseriesGood {
		t.Errorf("expected timeseries with metric 'test.second_metric' and tags 'tag1=value1' and 'global_tag=global_value'")
	}
}

func TestDefaultMetricsProcessor_processBatch(t *testing.T) {
	processor := &defaultMetricsProcessor{
		metricsApiClient: &mockMetricsApi{},
		datadogContext:   context.Background(),
		globalTags: []telemetry.Tag{
			telemetry.NewTag("global_tag", "global_value"),
		},
	}

	batch := []telemetry.MetricRecording{
		telemetry.NewMetricRecording(
			"test.first_metric",
			time.Now().Unix(),
			1.5,
			telemetry.NewTag("tag1", "value1"),
		),
	}

	logger := logrus.New()
	logger.SetOutput(io.Discard)

	var wg sync.WaitGroup
	wg.Add(1)
	processor.processBatch(batch, &wg, logger)
	// This only checks that wg.Done() is called. Will be removed soon in favor of a context.
	testutil.AssertExitsAfter(t, func() { wg.Wait() }, 100*time.Millisecond)

	client := processor.metricsApiClient.(*mockMetricsApi)
	if !client.submitMetricsCalled {
		t.Fatalf("expected processBatch to call SubmitMetrics")
	}
}

// TODO
func TestDatadogMetricsExporter_RegisterMetric(t *testing.T) {

}

// TODO
func TestNewDatadogMetricsExporter(t *testing.T) {

}

func TestDatadogMetricsExporter_StartShutdown(t *testing.T) {
	voidLogger := logrus.New()
	voidLogger.SetOutput(io.Discard)

	batchSize := 10
	batchTimeout := 20 * time.Second
	t.Run("shutdown when not started", func(t *testing.T) {
		t.Parallel()
		metricsExporter := createMetricsExporter(t, batchSize, batchTimeout)
		err := metricsExporter.Shutdown()
		if err != nil {
			t.Fatalf("expected Shutdown not to return an error, got %v", err)
		}

		testutil.AssertExitsAfter(t, func() { metricsExporter.Wait() }, 1*time.Second)
	})

	t.Run("shutdown when already running", func(t *testing.T) {
		t.Parallel()
		metricsExporter := createMetricsExporter(t, batchSize, batchTimeout)
		metricsExporter.logger.SetOutput(os.Stdout)
		err := metricsExporter.Start()
		if err != nil {
			t.Fatalf("expected Start not to return an error, got %v", err)
		}

		err = metricsExporter.Shutdown()
		if err != nil {
			t.Fatalf("expected Shutdown not to return an error, got %v", err)
		}

		testutil.AssertExitsAfter(t, func() { metricsExporter.Wait() }, 3*time.Second)
	})

	t.Run("double shutdown when already running", func(t *testing.T) {
		t.Parallel()
		metricsExporter := createMetricsExporter(t, batchSize, batchTimeout)
		err := metricsExporter.Start()
		if err != nil {
			t.Fatalf("expected Start not to return an error, got %v", err)
		}

		err = metricsExporter.Shutdown()
		if err != nil {
			t.Fatalf("expected first Shutdown not to return an error, got %v", err)
		}

		err = metricsExporter.Shutdown()
		if err != nil {
			t.Fatalf("expected second Shutdown not to return an error, got %v", err)
		}

		testutil.AssertExitsAfter(t, func() { metricsExporter.Wait() }, 1*time.Second)
	})
}

func TestDatadogMetricsExporter(t *testing.T) {
	t.Run("full batch before timeout", func(t *testing.T) {
		t.Parallel()
		voidLogger := logrus.New()
		voidLogger.SetOutput(io.Discard)

		batchSize := 10
		batchTimeout := 20 * time.Second
		processor := mockMetricsProcessor{}
		metricsExporter := DatadogMetricsExporter{
			metricsChan:  make(chan telemetry.MetricRecording),
			batchSize:    batchSize,
			batchTimeout: batchTimeout,
			wg:           &sync.WaitGroup{},
			logger:       voidLogger,
			processor:    &processor,
			mu:           &sync.Mutex{},
		}

		// TODO: Global tags?

		metricsExporter.Start()

		for i := 0; i < batchSize; i++ {
			metricsExporter.EmitMetric(
				telemetry.NewMetricRecording(
					"test.metric",
					time.Now().Unix(),
					1.0,
					telemetry.NewTag("tag1", "value1"),
				),
			)
		}

		// Give the handler time to pick up the batch
		time.Sleep(1 * time.Second)

		if len(processor.processBatchCalls) != 1 {
			t.Errorf("expected processBatch to be called once, but it was called %d times", len(processor.processBatchCalls))
		} else {
			call := processor.processBatchCalls[0]
			if len(call.batchArg) != batchSize {
				t.Errorf("expected processBatch to be called with a batch of size %d, but it was called with a batch of size %d", batchSize, len(call.batchArg))
			}

			for _, metric := range call.batchArg {
				if metric.Name != "test.metric" {
					t.Errorf("expected metric to have metric name 'test.metric', but it was %s", metric.Name)
				}

				if metric.Value != 1.0 {
					t.Errorf("expected metric to have value 1.0, but it was %f", metric.Value)
				}

				if len(*metric.Tags) != 1 {
					t.Errorf("expected metric to have 1 tag, but it had %d tags", len(*metric.Tags))
				}

				if (*metric.Tags)["tag1"] != "value1" {
					t.Errorf("expected metric to have tag 'tag1=value1', but it had tag '%s'", (*metric.Tags)["tag1"])
				}
			}
		}

		metricsExporter.Shutdown()
		testutil.AssertExitsAfter(t, func() { metricsExporter.Wait() }, 1*time.Second)
	})

	t.Run("timeout before full batch", func(t *testing.T) {
		t.Parallel()
		voidLogger := logrus.New()
		voidLogger.SetOutput(io.Discard)

		batchSize := 30
		batchTimeout := 2 * time.Second
		processor := mockMetricsProcessor{}
		metricsExporter := DatadogMetricsExporter{
			metricsChan:  make(chan telemetry.MetricRecording),
			batchSize:    batchSize,
			batchTimeout: batchTimeout,
			wg:           &sync.WaitGroup{},
			logger:       voidLogger,
			processor:    &processor,
			mu:           &sync.Mutex{},
		}

		// TODO: Global tags?

		metricsExporter.Start()

		metricCnt := batchSize / 2
		for i := 0; i < metricCnt; i++ {
			metricsExporter.EmitMetric(
				telemetry.NewMetricRecording(
					"test.metric",
					time.Now().Unix(),
					1.0,
					telemetry.NewTag("tag1", "value1"),
				),
			)
		}

		// Wait for batch to time out (+1 second to account for the handler)
		time.Sleep(batchTimeout + 1*time.Second)

		if len(processor.processBatchCalls) != 1 {
			t.Errorf("expected processBatch to be called once, but it was called %d times", len(processor.processBatchCalls))
		} else {
			call := processor.processBatchCalls[0]
			if len(call.batchArg) != metricCnt {
				t.Errorf("expected processBatch to be called with a batch of size %d, but it was called with a batch of size %d", metricCnt, len(call.batchArg))
			}

			for _, metric := range call.batchArg {
				if metric.Name != "test.metric" {
					t.Errorf("expected metric to have metric name 'test.metric', but it was %s", metric.Name)
				}

				if metric.Value != 1.0 {
					t.Errorf("expected metric to have value 1.0, but it was %f", metric.Value)
				}

				if len(*metric.Tags) != 1 {
					t.Errorf("expected metric to have 1 tag, but it had %d tags", len(*metric.Tags))
				}

				if (*metric.Tags)["tag1"] != "value1" {
					t.Errorf("expected metric to have tag 'tag1=value1', but it had tag '%s'", (*metric.Tags)["tag1"])
				}
			}
		}

		metricsExporter.Shutdown()
		testutil.AssertExitsAfter(t, func() { metricsExporter.Wait() }, 1*time.Second)
	})

	t.Run("shutdown before timeout or full batch", func(t *testing.T) {
		t.Parallel()
		voidLogger := logrus.New()
		voidLogger.SetOutput(io.Discard)

		batchSize := 30
		batchTimeout := 20 * time.Second
		processor := mockMetricsProcessor{}
		metricsExporter := DatadogMetricsExporter{
			metricsChan:  make(chan telemetry.MetricRecording),
			batchSize:    batchSize,
			batchTimeout: batchTimeout,
			wg:           &sync.WaitGroup{},
			logger:       voidLogger,
			processor:    &processor,
			mu:           &sync.Mutex{},
		}

		// TODO: Global tags?

		metricsExporter.Start()

		metricCnt := 3
		for i := 0; i < metricCnt; i++ {
			metricsExporter.EmitMetric(
				telemetry.NewMetricRecording(
					"test.metric",
					time.Now().Unix(),
					1.0,
					telemetry.NewTag("tag1", "value1"),
				),
			)
		}

		metricsExporter.Shutdown()

		// Give the handler time to pick up the batch
		time.Sleep(1 * time.Second)

		if len(processor.processBatchCalls) != 1 {
			t.Errorf("expected processBatch to be called once, but it was called %d times", len(processor.processBatchCalls))
		} else {
			call := processor.processBatchCalls[0]
			if len(call.batchArg) != metricCnt {
				t.Errorf("expected processBatch to be called with a batch of size %d, but it was called with a batch of size %d", batchSize, len(call.batchArg))
			}

			for _, metric := range call.batchArg {
				if metric.Name != "test.metric" {
					t.Errorf("expected metric to have metric name 'test.metric', but it was %s", metric.Name)
				}

				if metric.Value != 1.0 {
					t.Errorf("expected metric to have value 1.0, but it was %f", metric.Value)
				}

				if len(*metric.Tags) != 1 {
					t.Errorf("expected metric to have 1 tag, but it had %d tags", len(*metric.Tags))
				}

				if (*metric.Tags)["tag1"] != "value1" {
					t.Errorf("expected metric to have tag 'tag1=value1', but it had tag '%s'", (*metric.Tags)["tag1"])
				}
			}
		}

		testutil.AssertExitsAfter(t, func() { metricsExporter.Wait() }, 1*time.Second)
	})
}

func createMetricsExporter(t *testing.T, batchSize int, batchTimeout time.Duration) *DatadogMetricsExporter {
	t.Helper()
	voidLogger := logrus.New()
	voidLogger.SetOutput(io.Discard)

	return &DatadogMetricsExporter{
		metricsChan:  make(chan telemetry.MetricRecording),
		batchSize:    batchSize,
		batchTimeout: batchTimeout,
		wg:           &sync.WaitGroup{},
		logger:       voidLogger,
		processor:    &mockMetricsProcessor{},
		mu:           &sync.Mutex{},
	}
}
