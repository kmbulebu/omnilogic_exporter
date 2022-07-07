package main

import (
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	gaugeMetrics = map[string]prometheus.Gauge{}
)

func getGaugeMetric(namespace string, subsystem string, name string, mspSystemId, itemSystemId string) prometheus.Gauge {
	key := prometheus.BuildFQName(namespace, subsystem, name) + "_" + itemSystemId
	gauge, exists := gaugeMetrics[key]
	if !exists {
		labels := map[string]string{}
		if len(itemSystemId) > 0 {
			labels["system_id"] = itemSystemId
		}
		if len(mspSystemId) > 0 {
			labels["msp_system_id"] = mspSystemId
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

func buildMetrics(ch chan<- prometheus.Metric, mspSystemId string, telemetryDataResponse Status) error {
	items := telemetryDataResponse.DataItems

	floatRegex, _ := regexp.Compile("^[+-]?([0-9]+([.][0-9]*)?|[.][0-9]+)$")

	yesNoRegex, _ := regexp.Compile("^(?:yes|no)$")

	for _, item := range items {
		for k, v := range item.attributes {

			// If it has a value, try and parse it.
			if len(v) > 0 {

				if floatRegex.MatchString(v) {
					// It's a number, treat as a guage.
					// We have to assume the number can go up or down.
					floatValue, err := strconv.ParseFloat(v, 64)
					// A possibly poor assumption that negative values are invalid (e.g. airtemp)
					if err == nil && floatValue >= 0 {
						gaugeMetric := getGaugeMetric(namespace, item.name, k, mspSystemId, item.systemId)
						gaugeMetric.Set(floatValue)
						ch <- gaugeMetric
					}
				} else if yesNoRegex.MatchString(strings.ToLower(v)) {
					// Matches yes or no, treat as a guage with a value of 1 or 0.
					floatValue := 0.0
					if strings.ToLower(v) == "yes" {
						floatValue = 1.0
					}
					gaugeMetric := getGaugeMetric(namespace, item.name, k, mspSystemId, item.systemId)
					gaugeMetric.Set(floatValue)
					ch <- gaugeMetric
				}
			}

		}
	}
	return nil
}
