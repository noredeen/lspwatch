package config

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

type LspwatchConfig struct {
	Exporter    string `yaml:"exporter" validate:"required,oneof=opentelemetry datadog"`
	EnvFilePath string `yaml:"env_file" validate:"required_if=Exporter datadog,omitempty"`

	Metrics         *[]string `yaml:"metrics" validate:"omitempty,dive,oneof=request.duration server.rss"`
	Tags            []string  `yaml:"tags" validate:"omitempty,dive,oneof=user os language_server ram"`
	MeteredRequests *[]string `yaml:"metered_requests" validate:"omitempty"`
	PollingInterval *int      `yaml:"polling_interval" validate:"omitnil,gte=1,lte=1000"`

	OpenTelemetry *OpenTelemetryConfig `yaml:"opentelemetry" validate:"required_if=Exporter opentelemetry"`
	Datadog       *DatadogConfig       `yaml:"datadog" validate:"required_if=Exporter datadog"`
}

type OpenTelemetryConfig struct {
	Protocol           string            `yaml:"protocol" validate:"required,oneof=grpc http file"`
	Directory          string            `yaml:"directory" validate:"required_if=Protocol file,omitempty,min=1"`
	MetricsEndpointURL string            `yaml:"metrics_endpoint_url" validate:"required_unless=Protocol file,omitempty"`
	TLS                TLSConfig         `yaml:"tls" validate:"omitempty"`
	Compression        string            `yaml:"compression" validate:"omitempty,oneof=gzip"`
	Headers            map[string]string `yaml:"headers" validate:"omitempty"`
	Timeout            *int              `yaml:"timeout" validate:"omitnil"`
}

type TLSConfig struct {
	Insecure           bool   `yaml:"insecure"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	CAFile             string `yaml:"ca_file" validate:"omitempty,filepath"`
	CertFile           string `yaml:"cert_file" validate:"omitempty,filepath"`
	KeyFile            string `yaml:"key_file" validate:"omitempty,filepath"`
}

type DatadogConfig struct {
	ClientApiKeyEnvVar string `yaml:"client_api_key_env_var" validate:"required"`
	ClientAppKeyEnvVar string `yaml:"client_app_key_env_var" validate:"required"`
	BatchSize          *int   `yaml:"exporter_batch_size" validate:"omitnil,gte=1,lte=500"`
	BatchTimeout       *int   `yaml:"exporter_batch_timeout" validate:"omitnil,gte=1,lte=250"`
	Site               string `yaml:"site" validate:"omitempty,oneof=datadoghq.com us3.datadoghq.com us5.datadoghq.com ap1.datadoghq.com datadoghq.eu ddog-gov.com"`
	DisableCompression *bool  `yaml:"disable_compression" validate:"omitnil"`
}

func GetDefaultConfig() LspwatchConfig {
	return LspwatchConfig{
		Exporter: "opentelemetry",
		OpenTelemetry: &OpenTelemetryConfig{
			Protocol:  "file",
			Directory: "./",
		},
	}
}

func ReadLspwatchConfig(fileBytes []byte) (LspwatchConfig, error) {
	config := LspwatchConfig{}
	err := yaml.Unmarshal(fileBytes, &config)
	if err != nil {
		return LspwatchConfig{}, fmt.Errorf("error decoding config YAML: %v", err)
	}

	validate := validator.New(validator.WithRequiredStructEnabled())
	err = validate.Struct(config)
	if err != nil {
		return LspwatchConfig{}, fmt.Errorf("invalid lspwatch configuration: %v", err)
	}

	return config, nil
}
