// Copyright 2018 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	namespace    = "omnilogic" // For Prometheus metrics.
	omnilogicUrl = "https://www.haywardomnilogic.com/HAAPI/HomeAutomation/API.ashx"
)

var (
	omnilogicUp     = prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "up"), "Was the last scrape of OmniLogic successful.", nil, nil)
	omnilogicStatus = prometheus.NewDesc(prometheus.BuildFQName(namespace, "site", "system_status"), "OmniLogic site system status.", []string{"msp_system_id", "backyard_name"}, nil)
)

// Exporter collects OmniLogic stats from the given URI and exports them using
// the prometheus metrics package.
type Exporter struct {
	URI      string
	session  *Session
	sites    []*Site
	userName string
	password string
	timeout  time.Duration
	mutex    sync.RWMutex

	up                                            prometheus.Gauge
	totalScrapes, xmlParseFailures, loginFailures prometheus.Counter
	logger                                        log.Logger
}

// NewExporter returns an initialized Exporter.
func NewExporter(uri string, username string, password string, timeout time.Duration, logger log.Logger) (*Exporter, error) {
	// Check that the provided uri is valid
	_, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	return &Exporter{
		URI:      uri,
		userName: username,
		password: password,
		timeout:  timeout,
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the last scrape of omnilogic successful.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_scrapes_total",
			Help:      "Current total OmniLogic scrapes.",
		}),
		xmlParseFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_xml_parse_failures_total",
			Help:      "Number of errors while parsing XML.",
		}),
		loginFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_login_failures_total",
			Help:      "Number of errors while logging into Omnilogic.",
		}),
		logger: logger,
	}, nil
}

// Describe describes all the metrics ever exported by the OmniLogic exporter. It
// implements prometheus.Collector.
// Leaving this as an unchecked collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
}

func (e *Exporter) Login() error {
	loginRequest, err := e.buildLoginRequest()

	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: e.timeout,
	}

	level.Debug(e.logger).Log("msg", "Login Request Body", "loginRequest", loginRequest)

	req, err := http.NewRequest("POST", e.URI, strings.NewReader(loginRequest))

	if err != nil {
		return err
	}

	req.Header.Add("cache-control", "no-cache")
	req.Header.Add("content-type", "text/xml")

	level.Debug(e.logger).Log("msg", "Login Request Headers", "req.Header", fmt.Sprint(req.Header))

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	level.Debug(e.logger).Log("msg", "Login Response Status Code", "resp.StatusCode", fmt.Sprint(resp.StatusCode))

	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		resp.Body.Close()
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return err
	}

	e.session, err = parseLoginResponse(string(body))

	if err != nil {
		return err
	}

	level.Debug(e.logger).Log("msg", "Login Response Headers", "resp.Header", fmt.Sprint(resp.Header))
	level.Debug(e.logger).Log("msg", "Login Response Body", "resp.Body", string(body))

	switch e.session.Status {
	case "0":
		{
			level.Info(e.logger).Log("msg", "Login successful.", "UserID", e.session.UserID)
		}
	case "4":
		{
			level.Warn(e.logger).Log("msg", "Login Failed: Incorrect UserName or Password.", "StatusMessage", e.session.StatusMessage)
		}
	default:
		{
			level.Warn(e.logger).Log("msg", "Login failed.", "StatusMessage", e.session.StatusMessage)
		}
	}

	return nil
}

func (e *Exporter) RefreshSiteList(ch chan<- prometheus.Metric) error {
	siteListRequest, err := e.buildSiteListRequest()

	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: e.timeout,
	}

	level.Debug(e.logger).Log("msg", "RefreshSiteList Request Body", "siteListRequest", siteListRequest)
	req, err := http.NewRequest("POST", e.URI, strings.NewReader(siteListRequest))

	if err != nil {
		return err
	}

	req.Header.Add("cache-control", "no-cache")
	req.Header.Add("content-type", "text/xml")
	req.Header.Add("Token", e.session.Token)

	level.Debug(e.logger).Log("msg", "RefreshSiteList Request Headers", "req.Header", fmt.Sprint(req.Header))

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	level.Debug(e.logger).Log("msg", "RefreshSiteList Response Status Code", "resp.StatusCode", fmt.Sprint(resp.StatusCode))

	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		resp.Body.Close()
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return err
	}

	level.Debug(e.logger).Log("msg", "RefreshSiteList Response Headers", "resp.Header", fmt.Sprint(resp.Header))
	level.Debug(e.logger).Log("msg", "RefreshSiteList Response Body", "resp.Body", string(body))

	e.sites, err = parseSiteListResponse(string(body))

	if err != nil {
		return err
	}

	for _, site := range e.sites {
		ch <- prometheus.MustNewConstMetric(omnilogicStatus, prometheus.GaugeValue, site.Status, site.MspSystemID, site.BackyardName)
	}

	level.Info(e.logger).Log("msg", "Refresh site list successful.", "# Sites", len(e.sites))

	return nil
}

func (e *Exporter) RefreshTelemetryData(ch chan<- prometheus.Metric) error {

	for _, site := range e.sites {
		telemetryDataRequest, err := e.buildTelemetryDataRequest(site.MspSystemID)

		if err != nil {
			return err
		}

		client := &http.Client{
			Timeout: e.timeout,
		}
		level.Debug(e.logger).Log("msg", "RefreshTelemetryData Request Body", "siteListRequest", telemetryDataRequest)
		req, err := http.NewRequest("POST", e.URI, strings.NewReader(telemetryDataRequest))

		if err != nil {
			return err
		}

		req.Header.Add("cache-control", "no-cache")
		req.Header.Add("content-type", "text/xml")
		req.Header.Add("Token", e.session.Token)

		level.Debug(e.logger).Log("msg", "RefreshTelemetryData Request Headers", "req.Header", fmt.Sprint(req.Header))

		resp, err := client.Do(req)

		if err != nil {
			return err
		}

		level.Debug(e.logger).Log("msg", "RefreshTelemetryData Response Status Code", "resp.StatusCode", fmt.Sprint(resp.StatusCode))

		if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
			resp.Body.Close()
			return fmt.Errorf("HTTP status %d", resp.StatusCode)
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)

		level.Debug(e.logger).Log("msg", "RefreshTelemetryData Response Headers", "resp.Header", fmt.Sprint(resp.Header))
		level.Debug(e.logger).Log("msg", "RefreshTelemetryData Response Body", "resp.Body", string(body))

		if err != nil {
			return err
		}

		status, err := parseTelemetryDataResponse(string(body))

		if err != nil {
			return err
		}

		err = buildMetrics(ch, site.MspSystemID, *status)

		if err != nil {
			return err
		}

		level.Info(e.logger).Log("msg", "Refresh telemetry data successful.")

	}

	return nil
}

func (e *Exporter) buildTelemetryDataRequest(mspSystemId string) (string, error) {
	if e.session == nil || len(e.session.UserID) == 0 {
		return "", errors.New("session UserID is empty")
	}
	mspSystemIdParameter := NewParameter("int", "MspSystemID", mspSystemId)
	parameters := []*Parameter{mspSystemIdParameter}

	return buildRequestXml("GetTelemetryData", parameters)
}

func (e *Exporter) buildSiteListRequest() (string, error) {
	if e.session == nil || len(e.session.UserID) == 0 {
		return "", errors.New("session UserID is empty")
	}
	userIDParameter := NewParameter("string", "UserID", e.session.UserID)
	parameters := []*Parameter{userIDParameter}

	return buildRequestXml("GetSiteList", parameters)
}

func parseSiteListResponse(response string) ([]*Site, error) {
	siteListResponse, err := parseResponseXml(response)

	if err != nil {
		return nil, err
	}

	var status string
	var statusMessage string
	var sites []*Site

	// Consider switching to xpath queries to avoid this loop nesting
	for _, parameter := range siteListResponse.Parameters.Parameters {
		switch parameter.Name {
		case "Status":
			status = parameter.Value
		case "StatusMessage":
			statusMessage = parameter.Value
		case "List":
			{
				for _, item := range parameter.Items {
					site := Site{}
					for _, property := range item.Properties {
						switch property.Name {
						case "MspSystemID":
							site.MspSystemID = property.Value
						case "BackyardName":
							site.BackyardName = property.Value
						case "Address":
							site.Address = property.Value
						case "Status":
							status, err := strconv.ParseFloat(property.Value, 64)
							// TODO: Log the parsing failure.
							if err == nil {
								site.Status = status
							}
						} // Switch property name
					} // for each property
					sites = append(sites, &site)
				} // for each item
			} // Case "List"
		} // Switch parameter name
	} // for each parameter
	if status != "0" {
		return nil, fmt.Errorf("received error when requesting site list: %v", statusMessage)
	}

	return sites, nil
}

type Site struct {
	MspSystemID  string
	BackyardName string
	Address      string
	Status       float64
}

func (e *Exporter) buildLoginRequest() (string, error) {
	userNameParameter := NewParameter("string", "UserName", e.userName)
	passwordParameter := NewParameter("string", "Password", e.password)
	parameters := []*Parameter{userNameParameter, passwordParameter}

	return buildRequestXml("Login", parameters)
}

func parseLoginResponse(response string) (*Session, error) {
	loginResponse, err := parseResponseXml(response)

	if err != nil {
		return nil, err
	}

	session := Session{}

	for _, parameter := range loginResponse.Parameters.Parameters {
		switch parameter.Name {
		case "Status":
			session.Status = parameter.Value
		case "StatusMessage":
			session.StatusMessage = parameter.Value
		case "UserID":
			session.UserID = parameter.Value
		case "Token":
			session.Token = parameter.Value
		}
	}

	return &session, nil

}

type Session struct {
	UserID        string
	Token         string
	Status        string
	StatusMessage string
}

func parseResponseXml(response string) (*Response, error) {
	var responseXml Response

	if err := xml.Unmarshal([]byte(response), &responseXml); err != nil {
		return nil, err
	}

	return &responseXml, nil
}

func buildRequestXml(name string, parameters []*Parameter) (string, error) {
	request := NewRequest(name, parameters)
	result, err := xml.Marshal(request)
	return string(result), err
}

func NewRequest(name string, parameters []*Parameter) *Request {
	parametersXml := NewParameters(parameters)
	return &Request{
		Name:       name,
		Parameters: *parametersXml,
	}
}

func NewParameters(parameters []*Parameter) *Parameters {
	return &Parameters{
		Parameters: parameters,
	}
}

func NewParameter(DataType string, Name string, Value string) *Parameter {
	return &Parameter{
		DataType: DataType,
		Name:     Name,
		Value:    Value,
	}
}

type Parameter struct {
	XMLName  xml.Name `xml:"Parameter"`
	Name     string   `xml:"name,attr"`
	DataType string   `xml:"dataType,attr"`
	Items    []*Item  `xml:"Item"`
	Value    string   `xml:",chardata"`
}

type Parameters struct {
	XMLName    xml.Name     `xml:"Parameters"`
	Parameters []*Parameter `xml:"Parameter"`
}

type Item struct {
	XMLName    xml.Name    `xml:"Item"`
	Properties []*Property `xml:"Property"`
}

type Property struct {
	XMLName  xml.Name `xml:"Property"`
	Name     string   `xml:"name,attr"`
	DataType string   `xml:"dataType,attr"`
	Value    string   `xml:",chardata"`
}

type Request struct {
	XMLName    xml.Name   `xml:"Request"`
	Name       string     `xml:"Name"`
	Parameters Parameters `xml:"Parameters"`
}

type Response struct {
	XMLName    xml.Name   `xml:"Response"`
	Name       string     `xml:"Name"`
	Parameters Parameters `xml:"Parameters"`
}

// Collect fetches the stats from configured OmniLogic location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()

	up := e.scrape(ch)

	ch <- prometheus.MustNewConstMetric(omnilogicUp, prometheus.GaugeValue, up)
	ch <- e.totalScrapes
	ch <- e.xmlParseFailures
	ch <- e.loginFailures
}

func (e *Exporter) scrape(ch chan<- prometheus.Metric) (up float64) {
	e.totalScrapes.Inc()
	var err error

	// If not logged in, login.
	if e.session == nil || e.session.Status != "0" {
		err = e.Login()

		if err != nil {
			level.Error(e.logger).Log("msg", "Can't scrape OmniLogic. Login failed.", "err", err)
			e.loginFailures.Inc()
			return 0
		}
	}

	// Refresh list of Omnilogic sites and status
	err = e.RefreshSiteList(ch)

	if err != nil {
		level.Error(e.logger).Log("msg", "Can't scrape OmniLogic. Failed to refresh site list.", "err", err)
		return 0
	}

	err = e.RefreshTelemetryData(ch)

	if err != nil {
		level.Error(e.logger).Log("msg", "Can't scrape OmniLogic. Failed to refresh telemetry data for sites.", "err", err)
		return 0
	}

	return 1
}

func main() {

	var (
		webConfig         = webflag.AddFlags(kingpin.CommandLine)
		listenAddress     = kingpin.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":9190").String()
		metricsPath       = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
		omniLogicUrl      = kingpin.Flag("omnilogic.url", "The Omnilogic API URL.").Default("/metrics").Default(omnilogicUrl).String()
		omniLogicTimeout  = kingpin.Flag("omnilogic.timeout", "Timeout for trying to get stats from OmniLogic.").Default("5s").Duration()
		omniLogicUserName = kingpin.Flag("omnilogic.username", "UserName to login to OmniLogic.").Required().String()
		omniLogicPassword = kingpin.Flag("omnilogic.password", "Password to login to OmniLogic.").Required().String()
	)

	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("omnilogic_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting omnilogic_exporter", "version", version.Info())
	level.Info(logger).Log("msg", "Build context", "context", version.BuildContext())

	exporter, err := NewExporter(*omniLogicUrl, *omniLogicUserName, *omniLogicPassword, *omniLogicTimeout, logger)
	if err != nil {
		level.Error(logger).Log("msg", "Error creating an exporter", "err", err)
		os.Exit(1)
	}

	prometheus.MustRegister(exporter)
	prometheus.MustRegister(version.NewCollector("omnilogic_exporter"))

	level.Info(logger).Log("msg", "Listening on address", "address", *listenAddress)
	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Omnilogic Exporter</title></head>
             <body>
             <h1>Omnilogic Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	srv := &http.Server{Addr: *listenAddress}
	if err := web.ListenAndServe(srv, *webConfig, logger); err != nil {
		level.Error(logger).Log("msg", "Error starting HTTP server", "err", err)
		os.Exit(1)
	}
}
