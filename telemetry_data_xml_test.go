package main

import (
	"io/ioutil"
	"path"
	"testing"
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
