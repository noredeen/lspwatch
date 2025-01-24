package internal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/bombsimon/logrusr/v4"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
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
	meterProvider *sdkmetric.MeterProvider
	meter         metric.Meter
	histograms    map[string]metric.Float64Histogram
}

var _ MetricsExporter = &MetricsOTelExporter{}
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

func (ome *MetricsOTelExporter) RegisterMetric(
	kind MetricKind,
	name string,
	description string,
	unit string,
) error {
	switch kind {
	case Histogram:
		hist, err := ome.meter.Float64Histogram(
			name,
			metric.WithDescription(description),
			metric.WithUnit(unit),
		)
		if err != nil {
			return err
		}

		ome.histograms[name] = hist
	}

	return nil
}

func (ome *MetricsOTelExporter) EmitMetric(metricPoint MetricRecording) error {
	if histogram, ok := ome.histograms[metricPoint.Name]; ok {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), emitMetricTimeout)
		defer cancel()

		histogram.Record(timeoutCtx, metricPoint.Value)
		return nil
	} else {
		return fmt.Errorf("histogram not found for metric: %s", metricPoint.Name)
	}
}

// NOTE: Might have to rework this into invoking a function stored in the struct.
func (ome *MetricsOTelExporter) Shutdown() error {
	return ome.meterProvider.Shutdown(context.Background())
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

func newTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
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
func newFileExporter(cfg *openTelemetryConfig) (*fileExporter, error) {
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

func newOTLPMetricsGRPCExporter(cfg *openTelemetryConfig) (sdkmetric.Exporter, error) {
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

func newOTLPMetricsHTTPExporter(cfg *openTelemetryConfig) (sdkmetric.Exporter, error) {
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

// https://opentelemetry.io/docs/languages/go/getting-started/#initialize-the-opentelemetry-sdk
func NewMetricsOTelExporter(cfg *openTelemetryConfig, logger *logrus.Logger) (*MetricsOTelExporter, error) {
	logr := logrusr.New(logger)
	// TODO: I don't think this works.
	otel.SetLogger(logr)

	res, err := newResource()
	if err != nil {
		return nil, err
	}

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

	meterProvider := newOTelMeterProvider(metricExporter, res)

	meter := meterProvider.Meter("lspwatch")

	return &MetricsOTelExporter{
		meterProvider: meterProvider,
		meter:         meter,
		histograms:    make(map[string]metric.Float64Histogram),
	}, nil
}
