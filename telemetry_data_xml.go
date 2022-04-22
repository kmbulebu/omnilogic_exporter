package main

import (
	"encoding/xml"
	"strconv"

	"github.com/iancoleman/strcase"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	gaugeMetrics = map[string]prometheus.Gauge{}
)

func getGaugeMetric(namespace string, subsystem string, name string, systemId string) prometheus.Gauge {
	key := prometheus.BuildFQName(namespace, subsystem, name) + "_" + systemId
	gauge, exists := gaugeMetrics[key]
	if !exists {
		labels := map[string]string{}
		if len(systemId) > 0 {
			labels["systemId"] = systemId
		}
		opts := prometheus.GaugeOpts{
			Namespace:   namespace,
			Subsystem:   subsystem,
			Name:        name,
			Help:        "",
			ConstLabels: labels,
		}
		gauge = prometheus.NewGauge(opts)
		gaugeMetrics[key] = gauge
	}
	return gauge
}

type Status struct {
	XMLName   xml.Name            `xml:"STATUS"`
	DataItems []TelemetryDataItem `xml:",any"`
}

type TelemetryDataItem struct {
	name       string
	systemId   string
	attributes map[string]string
}

func (i *TelemetryDataItem) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	i.attributes = make(map[string]string)

	i.name = strcase.ToSnake(start.Name.Local)

	for _, attr := range start.Attr {
		if attr.Name.Local == "systemId" {
			// Assign systemID from attribute value.
			i.systemId = attr.Value
		} else {
			// All others, convert name to snake case and map value
			i.attributes[strcase.ToSnake(attr.Name.Local)] = attr.Value
		}
	}

	// Signal we're done parsing this element.
	d.Skip()

	return nil
}

func parseTelemetryDataResponse(response string) (*Status, error) {
	var statusXml Status
	if err := xml.Unmarshal([]byte(response), &statusXml); err != nil {
		return nil, err
	}

	return &statusXml, nil
}

func buildMetrics(ch chan<- prometheus.Metric, telemetryDataResponse Status) error {
	items := telemetryDataResponse.DataItems
	for _, item := range items {
		for k, v := range item.attributes {
			// Build name
			// I'm thinking we build a map and store it as a global. Wrap it with a function.
			// The key is the name of the metric, built from the Omnilogic status row.
			// The value is the Metric.
			if len(v) > 0 {
				floatValue, err := strconv.ParseFloat(v, 64)
				if err == nil {
					gaugeMetric := getGaugeMetric(namespace, item.name, k, item.systemId)
					gaugeMetric.Set(floatValue)
					ch <- gaugeMetric
				}
			}

		}
	}
	return nil
}
