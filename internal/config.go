package internal

import (
	"fmt"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

// TODO: These validations are borked :(

type openTelemetryConfig struct {
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

type datadogConfig struct {
	ClientApiKeyEnvVar string `yaml:"client_api_key_env_var" validate:"required"`
	ClientAppKeyEnvVar string `yaml:"client_app_key_env_var" validate:"required"`
}

type LspwatchConfig struct {
	Exporter      string               `yaml:"exporter" validate:"required,oneof=opentelemetry datadog"`
	EnvFilePath   string               `yaml:"env_file" validate:"required_if=Exporter datadog,omitempty"`
	OpenTelemetry *openTelemetryConfig `yaml:"opentelemetry" validate:"required_if=Exporter opentelemetry"`
	Datadog       *datadogConfig       `yaml:"datadog" validate:"required_if=Exporter datadog"`
}

func GetDefaultConfig() LspwatchConfig {
	return LspwatchConfig{
		Exporter: "opentelemetry",
		OpenTelemetry: &openTelemetryConfig{
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
		if _, ok := err.(*validator.InvalidValidationError); ok {
			return LspwatchConfig{}, fmt.Errorf("internal validation error: %v", err)
		}

		return LspwatchConfig{}, fmt.Errorf("invalid lspwatch configuration: %v", err)
	}

	return config, nil
}
