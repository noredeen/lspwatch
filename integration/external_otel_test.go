package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Test flow:
//
//	(go test process) ----launch----
//	    |                           |
//	    |                           v
//	    |              lspwatch instance [ w/ compiled mock language server ]
//	    |                            |
//	     ---check---     ---export---
//	                |   |
//	                v   v
//	        **OTel collector in Docker**

type otelExportedObject struct {
	ResourceMetrics []struct {
		ScopeMetrics []struct {
			Metrics []struct {
				Name      string
				Histogram struct {
					DataPoints []struct {
						Count      string
						Attributes []struct {
							Key   string
							Value struct {
								StringValue string
							}
						}
					}
				}
			}
		}
	}
}

func TestLspwatchWithExternalOtel(t *testing.T) {
	testDataDir := os.Getenv("TEST_DATA_DIR")
	if testDataDir == "" {
		t.Fatalf("TEST_DATA_DIR is not set")
	}

	lspwatchBinary := os.Getenv("LSPWATCH_BIN")
	if lspwatchBinary == "" {
		t.Fatalf("LSPWATCH_BIN is not set")
	}

	coverageDir := os.Getenv("COVERAGE_DIR")
	if coverageDir == "" {
		t.Fatalf("COVERAGE_DIR is not set")
	}

	bytes, err := os.ReadFile(filepath.Join(testDataDir, "client_messages.json"))
	if err != nil {
		t.Fatalf("failed to read client messages: %v", err)
	}

	var messages []string
	err = json.Unmarshal(bytes, &messages)
	if err != nil {
		t.Fatalf("failed to unmarshal client messages: %v", err)
	}

	t.Logf("Starting lspwatch binary '%s'", lspwatchBinary)

	// Launch lspwatch process.
	cmd := exec.Command(
		lspwatchBinary,
		"--config",
		"./external_otel_lspwatch.yaml",
		"--log",
		"--",
		"./build/mock_language_server",
	)

	coverPath := fmt.Sprintf("GOCOVERDIR=%s", coverageDir)
	t.Logf("Sending coverage data to: %s", coverageDir)
	cmd.Env = append(os.Environ(), coverPath)

	serverStdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	serverStdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}

	err = cmd.Start()
	if err != nil {
		t.Fatalf("failed to start lspwatch: %v", err)
	}

	t.Cleanup(func() {
		// For an unknown reason, this Close() is required to make the
		// process exit gracefully.
		err := serverStdin.Close()
		if err != nil {
			t.Fatalf("failed to close server stdin: %v", err)
		}

		err = cmd.Process.Signal(os.Interrupt)
		if err != nil {
			t.Fatalf("failed to signal lspwatch to shutdown: %v", err)
		}

		// Give it some time to flush coverage data
		done := make(chan error)
		go func() {
			done <- cmd.Wait()
		}()

		// Wait up to 5 seconds for graceful shutdown
		select {
		case <-time.After(10 * time.Second):
			t.Log("Process didn't exit gracefully, forcing kill")
			cmd.Process.Kill()
		case err := <-done:
			t.Logf("Process exited with: %v", err)
		}
	})

	// TODO: Need this?
	go func() {
		for {
			buf := make([]byte, 100)
			serverStdout.Read(buf)
		}
	}()

	// Send client messages to lspwatch.
	for _, message := range messages {
		_, err = serverStdin.Write([]byte(message))
		if err != nil {
			t.Fatalf("failed to write message to lspwatch stdin: %v", err)
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Sent %d messages", len(messages))
	t.Log("Waiting...")

	// Wait for the language server to send all its messages and for the exporter to flush.
	time.Sleep(13 * time.Second)

	// Inspect the otel collector export for metrics
	otelFile, err := os.Open("/tmp/file-exporter/metrics.json")
	if err != nil {
		t.Fatalf("failed to open otel metrics file: %v", err)
	}

	decoder := json.NewDecoder(otelFile)
	foundRequestMetric := false
	foundServerMetric := false
	for {
		var exportedObject otelExportedObject
		err = decoder.Decode(&exportedObject)
		if err != nil {
			t.Logf("failed to decode otel metrics JSON export: %v", err)
			break
		}

		for _, resourceMetrics := range exportedObject.ResourceMetrics {
			for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
				for _, metric := range scopeMetrics.Metrics {
					if metric.Name == "lspwatch.request.duration" {
						foundRequestMetric = true
					}
					if metric.Name == "lspwatch.server.rss" {
						foundServerMetric = true
					}

					firstDataPointAttrs := metric.Histogram.DataPoints[0].Attributes
					assertOTelAttributes(
						t,
						firstDataPointAttrs,
						[]string{"user", "os", "ram", "language_server"},
					)
				}
			}
		}
	}

	if !foundRequestMetric {
		t.Errorf("expected to find request metrics")
	}
	if !foundServerMetric {
		t.Errorf("expected to find server metrics")
	}
}

func assertOTelAttributes(
	t *testing.T,
	attrs []struct {
		Key   string
		Value struct {
			StringValue string
		}
	},
	targets []string,
) {
	found := 0
	for _, target := range targets {
		for _, attr := range attrs {
			if attr.Key == target {
				found++
				break
			}
		}
	}

	if found != len(targets) {
		t.Errorf("expected to find attributes %v", targets)
	}
}
