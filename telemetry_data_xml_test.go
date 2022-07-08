package main

import (
	"io/ioutil"
	"path"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestUnMarshalTelemetryDataItem(t *testing.T) {
	fixtureText, err := ioutil.ReadFile(path.Join("test", "get_telemetry_data_response.xml"))

	if err != nil {
		t.Fatal("Could not open and read text fixture file, get_telemetry_data_response.xml", err)
	}

	telemetryData, err := parseTelemetryDataResponse(string(fixtureText))

	if err != nil {
		t.Fatal("Error parsing telemetry data response.", err)
	}

	if len(telemetryData.DataItems) != 12 {
		t.Fatalf("Expected 12 data items but found %v", len(telemetryData.DataItems))
	}

}

func TestTelemetryDataItem2(t *testing.T) {
	fixtureText, err := ioutil.ReadFile(path.Join("test", "get_telemetry_data_response2.xml"))

	if err != nil {
		t.Fatal("Could not open and read text fixture file, get_telemetry_data_response2.xml", err)
	}

	telemetryData, err := parseTelemetryDataResponse(string(fixtureText))

	if err != nil {
		t.Fatal("Error parsing telemetry data response.", err)
	}

	if len(telemetryData.DataItems) != 19 {
		t.Fatalf("Expected 19 data items but found %v", len(telemetryData.DataItems))
	}

	metrics := make(chan prometheus.Metric, 100)
	buildMetrics(metrics, "54321", *telemetryData)

	// CSAD dupes should be removed
	if len(metrics) != 56 {
		t.Fatalf("Expected 56 data items but found %v", len(metrics))
	}

}
