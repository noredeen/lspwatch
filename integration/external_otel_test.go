package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TODO: Don't hardcode paths.

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
//	           **otel collector**

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
	bytes, err := os.ReadFile("./testdata/client_messages.json")
	if err != nil {
		t.Fatalf("failed to read client messages: %v", err)
	}

	var messages []string
	err = json.Unmarshal(bytes, &messages)
	if err != nil {
		t.Fatalf("failed to unmarshal client messages: %v", err)
	}

	// Launch lspwatch process
	cmd := exec.Command(
		"../build/lspwatch",
		"--config",
		"./external_otel_lspwatch.yaml",
		"--log",
		"--",
		"./build/mock_language_server",
	)
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
		err = cmd.Process.Signal(os.Interrupt)
		if err != nil {
			t.Fatalf("failed to signal lspwatch to shutdown: %v", err)
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

	var exportedObject otelExportedObject
	err = decoder.Decode(&exportedObject)
	if err != nil {
		t.Errorf("failed to decode otel metrics: %v", err)
	}
}
