package exporters

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc/credentials"
)

const (
	otlpHandshakeTimeout = 7 * time.Second
	emitMetricTimeout    = 4 * time.Second
)

type MetricsOTelExporter struct {
	otelExporter  sdkmetric.Exporter
	resource      resource.Resource
	meterProvider *sdkmetric.MeterProvider
	meter         metric.Meter
	histograms    map[string]metric.Float64Histogram
	globalTags    []telemetry.Tag
	shutdownCtx   context.Context
}

var _ telemetry.MetricsExporter = &MetricsOTelExporter{}
var _ sdkmetric.Exporter = &fileExporter{}

// Why not the stdoutmetric exporter from otel/exporters?
//
// stdoutmetric with DeltaTemporality will export an object for each
// reading period, even if no new metrics were recorded. This custom
// exporter will not export objects that don't contain any ScopeMetrics
// (new recordings since last reading period). Will also be useful for
// a future automatic file rotation feature.
type fileExporter struct {
	file *os.File
}

func (ome *MetricsOTelExporter) RegisterMetric(registration telemetry.MetricRegistration) error {
	switch registration.Kind {
	case telemetry.Histogram:
		hist, err := ome.meter.Float64Histogram(
			registration.Name,
			metric.WithDescription(registration.Description),
			metric.WithUnit(registration.Unit),
		)
		if err != nil {
			return err
		}

		ome.histograms[registration.Name] = hist
	}

	return nil
}

func (ome *MetricsOTelExporter) EmitMetric(metricRecording telemetry.MetricRecording) error {
	histogram, ok := ome.histograms[metricRecording.Name]
	if !ok {
		return fmt.Errorf("histogram not found for metric: %s", metricRecording.Name)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), emitMetricTimeout)
	defer cancel()

	attrs := make([]attribute.KeyValue, 0, len(ome.globalTags)+len(*metricRecording.Tags))
	for _, tag := range ome.globalTags {
		attrs = append(attrs, attribute.String(tag.Key, string(tag.Value)))
	}

	for k, v := range *metricRecording.Tags {
		attrs = append(attrs, attribute.String(k, string(v)))
	}

	histogram.Record(timeoutCtx, metricRecording.Value, metric.WithAttributes(attrs...))
	return nil
}

func (ome *MetricsOTelExporter) Start() error {
	meterProvider := newOTelMeterProvider(ome.otelExporter, &ome.resource)
	meter := meterProvider.Meter("lspwatch")
	ome.meterProvider = meterProvider
	ome.meter = meter
	return nil
}

// NOTE: Might have to rework this into invoking a function stored in the struct.
func (ome *MetricsOTelExporter) Shutdown() error {
	// TODO: Timeout?
	ome.shutdownCtx = context.Background()
	go ome.meterProvider.Shutdown(ome.shutdownCtx)
	return nil
}

func (ome *MetricsOTelExporter) Wait() {
	<-ome.shutdownCtx.Done()
}

func (ome *MetricsOTelExporter) Release() error {
	return nil
}

func (ome *MetricsOTelExporter) SetGlobalTags(tags ...telemetry.Tag) {
	ome.globalTags = tags
}

// TODO: Honor the context?
func (e *fileExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	scopeMetrics := data.ScopeMetrics
	if len(scopeMetrics) == 0 {
		return nil
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	_, err = e.file.Write(jsonBytes)
	return err
}

func (e *fileExporter) Aggregation(instrumentKind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(instrumentKind)
}

func (e *fileExporter) Temporality(_ sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

func (e *fileExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *fileExporter) Shutdown(ctx context.Context) error {
	err := e.file.Close()
	if err != nil {
		return fmt.Errorf("error closing metrics file: %v", err)
	}

	return nil
}

// https://opentelemetry.io/docs/languages/go/getting-started/#initialize-the-opentelemetry-sdk
func NewMetricsOTelExporter(cfg *config.OpenTelemetryConfig) (*MetricsOTelExporter, error) {
	// TODO: I don't think this works.
	// logr := logrusr.New(logger)
	// otel.SetLogger(logr)

	var err error
	var metricExporter sdkmetric.Exporter
	switch cfg.Protocol {
	case "http":
		metricExporter, err = newOTLPMetricsHTTPExporter(cfg)
	case "grpc":
		metricExporter, err = newOTLPMetricsGRPCExporter(cfg)
	case "file":
		metricExporter, err = newFileExporter(cfg)
	}

	if err != nil {
		return nil, err
	}

	res, err := newResource()
	if err != nil {
		return nil, err
	}

	return &MetricsOTelExporter{
		resource:     *res,
		otelExporter: metricExporter,
		histograms:   make(map[string]metric.Float64Histogram),
	}, nil
}

func newTLSConfig(cfg *config.TLSConfig) (*tls.Config, error) {
	tlsCfg := tls.Config{}

	tlsCfg.InsecureSkipVerify = cfg.InsecureSkipVerify
	if cfg.CAFile != "" {
		caPem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("error reading CA file: %v", err)
		}
		rootCAs := x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(caPem) {
			return nil, fmt.Errorf("failed to append CA cert")
		}
		tlsCfg.ClientCAs = rootCAs
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("error loading TLS cert/key pair: %v", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return &tlsCfg, nil
}

// TODO: File rotation.
func newFileExporter(cfg *config.OpenTelemetryConfig) (*fileExporter, error) {
	path := cfg.Directory

	if path[len(path)-1] != '/' {
		path += "/"
	}
	path += "lspwatch_metrics.json"

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("error creating metrics file: %v", err)
	}

	return &fileExporter{file: file}, nil
}

func newResource() (*resource.Resource, error) {
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("lspwatch"),
			semconv.ServiceVersion("0.1.0"),
			semconv.DeploymentEnvironment("production"),
		),
	)
}

func newOTelMeterProvider(exporter sdkmetric.Exporter, res *resource.Resource) *sdkmetric.MeterProvider {
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				exporter,
				sdkmetric.WithInterval(30*time.Second),
			),
		),
	)

	return meterProvider
}

func newOTLPMetricsGRPCExporter(cfg *config.OpenTelemetryConfig) (sdkmetric.Exporter, error) {
	options := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpointURL(cfg.MetricsEndpointURL),
		otlpmetricgrpc.WithHeaders(cfg.Headers),
	}

	tlsCfg, err := newTLSConfig(&cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("error generating TLS config: %v", err)
	}

	tlsCredential := credentials.NewTLS(tlsCfg)
	if !cfg.TLS.Insecure {
		options = append(options, otlpmetricgrpc.WithTLSCredentials(tlsCredential))
	} else {
		options = append(options, otlpmetricgrpc.WithInsecure())
	}

	if cfg.Timeout != nil {
		options = append(options, otlpmetricgrpc.WithTimeout(time.Duration(*cfg.Timeout)*time.Second))
	}

	if cfg.Compression != "" {
		options = append(options, otlpmetricgrpc.WithCompressor(cfg.Compression))
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), otlpHandshakeTimeout)
	defer cancel()
	metricExporter, err := otlpmetricgrpc.New(
		timeoutCtx,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating OTLP gRPC metrics exporter: %v", err)
	}

	return metricExporter, nil
}

func newOTLPMetricsHTTPExporter(cfg *config.OpenTelemetryConfig) (sdkmetric.Exporter, error) {
	options := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.MetricsEndpointURL),
		otlpmetrichttp.WithHeaders(cfg.Headers),
	}

	tlsCfg, err := newTLSConfig(&cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("error generating TLS config: %v", err)
	}

	if !cfg.TLS.Insecure {
		options = append(options, otlpmetrichttp.WithTLSClientConfig(tlsCfg))
	} else {
		options = append(options, otlpmetrichttp.WithInsecure())
	}

	if cfg.Timeout != nil {
		otlpmetrichttp.WithTimeout(time.Duration(*cfg.Timeout) * time.Second)
	}

	if cfg.Compression != "" {
		// Only one option
		options = append(options, otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), otlpHandshakeTimeout)
	defer cancel()
	metricExporter, err := otlpmetrichttp.New(
		timeoutCtx,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating OTLP HTTP metrics exporter: %v", err)
	}

	return metricExporter, nil
}
