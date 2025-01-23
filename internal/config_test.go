package internal

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReadLspwatchConfig(t *testing.T) {
	type testCase struct {
		name        string
		rawYaml     string
		expectedCfg LspwatchConfig
		err         bool
	}

	testCases := []testCase{
		{
			name: "opentelemetry with file protocol",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  protocol: file
  directory: ./`,
			expectedCfg: LspwatchConfig{
				Exporter: "opentelemetry",
				OpenTelemetry: &openTelemetryConfig{
					Protocol:  "file",
					Directory: "./",
				},
			},
			err: false,
		},

		{
			name: "missing opentelemetry config",
			rawYaml: `
exporter: opentelemetry
`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "missing opentelemetry protocol",
			rawYaml: `
exporter: opentelemetry
opentelemetry:
  directory: ./`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "datadog with all required fields",
			rawYaml: `
exporter: datadog
env_file: .env
datadog:
  client_api_key_env_var: CLIENT_API_KEY
  client_app_key_env_var: CLIENT_APP_KEY`,
			expectedCfg: LspwatchConfig{
				Exporter:    "datadog",
				EnvFilePath: ".env",
				Datadog: &datadogConfig{
					ClientApiKeyEnvVar: "CLIENT_API_KEY",
					ClientAppKeyEnvVar: "CLIENT_APP_KEY",
				},
			},
			err: false,
		},

		{
			name: "missing datadog env file",
			rawYaml: `
exporter: datadog
datadog:
  client_api_key_env_var: CLIENT_API_KEY
  client_app_key_env_var: CLIENT_APP_KEY`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},

		{
			name: "missing datadog client api key env var",
			rawYaml: `
exporter: datadog
env_file: .env
datadog:
  client_app_key_env_var: CLIENT_APP_KEY`,
			expectedCfg: LspwatchConfig{},
			err:         true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg, err := ReadLspwatchConfig([]byte(testCase.rawYaml))
			if testCase.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}

			if !cmp.Equal(cfg, testCase.expectedCfg) {
				t.Errorf("expected %+v, got %+v", testCase.expectedCfg, cfg)
			}
		})
	}
}
