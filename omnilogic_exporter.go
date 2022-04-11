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
	// "bufio"

	"errors"

	// "errors"
	"fmt"
	"io"

	// "net"
	"encoding/xml"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"

	// "github.com/prometheus/client_golang/prometheus/collectors"
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
	serverLabelNames = []string{"backend", "server"}
)

type metricInfo struct {
	Desc *prometheus.Desc
	Type prometheus.ValueType
}

func newServerMetric(metricName string, docString string, t prometheus.ValueType, constLabels prometheus.Labels) metricInfo {
	return metricInfo{
		Desc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "server", metricName),
			docString,
			serverLabelNames,
			constLabels,
		),
		Type: t,
	}
}

type metrics map[int]metricInfo

func (m metrics) String() string {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	s := make([]string, len(keys))
	for i, k := range keys {
		s[i] = strconv.Itoa(k)
	}
	return strings.Join(s, ",")
}

var (
	serverMetrics = metrics{
		2:  newServerMetric("current_queue", "Current number of queued requests assigned to this server.", prometheus.GaugeValue, nil),
		3:  newServerMetric("max_queue", "Maximum observed number of queued requests assigned to this server.", prometheus.GaugeValue, nil),
		4:  newServerMetric("current_sessions", "Current number of active sessions.", prometheus.GaugeValue, nil),
		5:  newServerMetric("max_sessions", "Maximum observed number of active sessions.", prometheus.GaugeValue, nil),
		6:  newServerMetric("limit_sessions", "Configured session limit.", prometheus.GaugeValue, nil),
		7:  newServerMetric("sessions_total", "Total number of sessions.", prometheus.CounterValue, nil),
		8:  newServerMetric("bytes_in_total", "Current total of incoming bytes.", prometheus.CounterValue, nil),
		9:  newServerMetric("bytes_out_total", "Current total of outgoing bytes.", prometheus.CounterValue, nil),
		13: newServerMetric("connection_errors_total", "Total of connection errors.", prometheus.CounterValue, nil),
		14: newServerMetric("response_errors_total", "Total of response errors.", prometheus.CounterValue, nil),
		15: newServerMetric("retry_warnings_total", "Total of retry warnings.", prometheus.CounterValue, nil),
		16: newServerMetric("redispatch_warnings_total", "Total of redispatch warnings.", prometheus.CounterValue, nil),
		17: newServerMetric("up", "Current health status of the server (1 = UP, 0 = DOWN).", prometheus.GaugeValue, nil),
		18: newServerMetric("weight", "Current weight of the server.", prometheus.GaugeValue, nil),
		21: newServerMetric("check_failures_total", "Total number of failed health checks.", prometheus.CounterValue, nil),
		24: newServerMetric("downtime_seconds_total", "Total downtime in seconds.", prometheus.CounterValue, nil),
		30: newServerMetric("server_selected_total", "Total number of times a server was selected, either for new sessions, or when re-dispatching.", prometheus.CounterValue, nil),
		33: newServerMetric("current_session_rate", "Current number of sessions per second over last elapsed second.", prometheus.GaugeValue, nil),
		35: newServerMetric("max_session_rate", "Maximum observed number of sessions per second.", prometheus.GaugeValue, nil),
		38: newServerMetric("check_duration_seconds", "Previously run health check duration, in seconds", prometheus.GaugeValue, nil),
		39: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.CounterValue, prometheus.Labels{"code": "1xx"}),
		40: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.CounterValue, prometheus.Labels{"code": "2xx"}),
		41: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.CounterValue, prometheus.Labels{"code": "3xx"}),
		42: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.CounterValue, prometheus.Labels{"code": "4xx"}),
		43: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.CounterValue, prometheus.Labels{"code": "5xx"}),
		44: newServerMetric("http_responses_total", "Total of HTTP responses.", prometheus.CounterValue, prometheus.Labels{"code": "other"}),
		49: newServerMetric("client_aborts_total", "Total number of data transfers aborted by the client.", prometheus.CounterValue, nil),
		50: newServerMetric("server_aborts_total", "Total number of data transfers aborted by the server.", prometheus.CounterValue, nil),
		58: newServerMetric("http_queue_time_average_seconds", "Avg. HTTP queue time for last 1024 successful connections.", prometheus.GaugeValue, nil),
		59: newServerMetric("http_connect_time_average_seconds", "Avg. HTTP connect time for last 1024 successful connections.", prometheus.GaugeValue, nil),
		60: newServerMetric("http_response_time_average_seconds", "Avg. HTTP response time for last 1024 successful connections.", prometheus.GaugeValue, nil),
		61: newServerMetric("http_total_time_average_seconds", "Avg. HTTP total time for last 1024 successful connections.", prometheus.GaugeValue, nil),
	}

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
	serverMetrics                                 map[int]metricInfo
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
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.serverMetrics {
		ch <- m.Desc
	}
	ch <- omnilogicUp
	ch <- omnilogicStatus
	ch <- e.totalScrapes.Desc()
	ch <- e.xmlParseFailures.Desc()
	ch <- e.loginFailures.Desc()
}

func (e *Exporter) Login() error {
	loginRequest, err := e.buildLoginRequest()

	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: e.timeout,
	}

	req, err := http.NewRequest("POST", e.URI, strings.NewReader(loginRequest))
	req.Header.Add("cache-control", "no-cache")
	req.Header.Add("content-type", "text/xml")

	resp, err := client.Do(req)

	if err != nil {
		return err
	}
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

func (e *Exporter) RefreshSiteList() error {
	siteListRequest, err := e.buildSiteListRequest()

	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: e.timeout,
	}

	req, err := http.NewRequest("POST", e.URI, strings.NewReader(siteListRequest))
	req.Header.Add("cache-control", "no-cache")
	req.Header.Add("content-type", "text/xml")
	req.Header.Add("Token", e.session.Token)

	level.Debug(e.logger).Log("msg", "RefreshSiteList Request", "req.Header", req.Header, "req.Body", req.Body)

	resp, err := client.Do(req)

	if err != nil {
		return err
	}
	if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
		resp.Body.Close()
		return fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	level.Debug(e.logger).Log("msg", "RefreshSiteList Response", "resp.Header", resp.Header, "resp.Body", string(body))

	if err != nil {
		return err
	}

	e.sites, err = parseSiteListResponse(string(body))

	if err != nil {
		return err
	}

	level.Info(e.logger).Log("msg", "Refresh site list successful.", "# Sites", len(e.sites))

	return nil
}

func (e *Exporter) buildSiteListRequest() (string, error) {
	if e.session == nil || len(e.session.UserID) == 0 {
		return "", errors.New("Session UserID is empty.")
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
	if "0" != status {
		return nil, fmt.Errorf("Received error when requesting site list: %v", statusMessage)
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
	err = e.RefreshSiteList()

	if err != nil {
		level.Error(e.logger).Log("msg", "Can't scrape OmniLogic. Failed to refresh site list.", "err", err)
		return 0
	}

	for _, site := range e.sites {
		ch <- prometheus.MustNewConstMetric(omnilogicStatus, prometheus.GaugeValue, site.Status, site.MspSystemID, site.BackyardName)
	}

	return 1
}

type versionInfo struct {
	ReleaseDate string
	Version     string
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
