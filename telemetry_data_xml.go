package main

import (
	"encoding/xml"

	"github.com/iancoleman/strcase"
)

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
