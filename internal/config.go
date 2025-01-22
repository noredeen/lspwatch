package internal

import (
	"fmt"
	"os"

	"github.com/go-playground/validator/v10"
	"gopkg.in/yaml.v3"
)

type openTelemetryConfig struct {
	Protocol  string             `yaml:"protocol" validate:"required,oneof=grpc http file"`
	Directory string             `yaml:"directory" validate:"required_if=Protocol file,min=1"`
	Endpoint  string             `yaml:"metrics_endpoint" validate:"required_unless=Protocol file"`
	Headers   *map[string]string `yaml:"headers"`
	Timeout   *int               `yaml:"timeout"`
	// TODO: protocol, TLS, retry, proxy, ...
}

type datadogConfig struct {
	ClientApiKeyEnvVar string `yaml:"client_api_key_env_var" validate:"required"`
	ClientAppKeyEnvVar string `yaml:"client_app_key_env_var" validate:"required"`
}

type LspwatchConfig struct {
	Exporter      string              `yaml:"exporter" validate:"required,oneof=opentelemetry datadog"`
	EnvFilePath   string              `yaml:"env_file" validate:"filepath,required_if=Exporter datadog"`
	OpenTelemetry openTelemetryConfig `yaml:"opentelemetry" validate:"required_if=Exporter opentelemetry"`
	Datadog       datadogConfig       `yaml:"datadog" validate:""`
}

func GetDefaultConfig() LspwatchConfig {
	return LspwatchConfig{
		Exporter: "opentelemetry",
		OpenTelemetry: openTelemetryConfig{
			Protocol:  "file",
			Directory: ".",
		},
	}
}

func ReadLspwatchConfig(path string) (*LspwatchConfig, error) {
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	config := LspwatchConfig{}
	err = yaml.Unmarshal(fileBytes, &config)
	if err != nil {
		return nil, fmt.Errorf("error decoding config YAML: %v", err)
	}

	validate := validator.New(validator.WithRequiredStructEnabled())
	err = validate.Struct(config)
	// TODO: Return erros
	// if validationErrors != nil {
	// 	return nil, fmt.Errorf("invalid lspwatch configuration: %v", err)
	// }

	return &config, nil
}
