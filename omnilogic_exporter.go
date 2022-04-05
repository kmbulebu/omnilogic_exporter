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
	"crypto/tls"
	// "errors"
	"fmt"
	"io"
	// "net"
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
	namespace = "omnilogic" // For Prometheus metrics.
)

var (
	serverLabelNames   = []string{"backend", "server"}
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

	omnilogicInfo = prometheus.NewDesc(prometheus.BuildFQName(namespace, "version", "info"), "OmniLogic version info.", []string{"release_date", "version"}, nil)
	omnilogicUp   = prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "up"), "Was the last scrape of OmniLogic successful.", nil, nil)
)

// Exporter collects OmniLogic stats from the given URI and exports them using
// the prometheus metrics package.
type Exporter struct {
	URI       string
	mutex     sync.RWMutex
	fetchInfo func() (io.ReadCloser, error)
	fetchStat func() (io.ReadCloser, error)

	up                             prometheus.Gauge
	totalScrapes, xmlParseFailures prometheus.Counter
	serverMetrics                  map[int]metricInfo
	logger                         log.Logger
}

// NewExporter returns an initialized Exporter.
func NewExporter(username string, password string, timeout time.Duration, logger log.Logger) (*Exporter, error) {
	u, err := url.Parse("https://example.org/")
	if err != nil {
		return nil, err
	}

	var fetchInfo func() (io.ReadCloser, error)
	var fetchStat func() (io.ReadCloser, error)
	switch u.Scheme {
	case "http", "https", "file":
		fetchStat = fetchHTTP("https://example.org/", true, timeout)
	case "unix":
		fetchInfo = fetchHTTP("https://example.org/", true, timeout)
		fetchStat = fetchHTTP("https://example.org/", true, timeout)
	default:
		return nil, fmt.Errorf("unsupported scheme: %q", u.Scheme)
	}

	return &Exporter{
		fetchInfo: fetchInfo,
		fetchStat: fetchStat,
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
		logger:               logger,
	}, nil
}

// Describe describes all the metrics ever exported by the OmniLogic exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.serverMetrics {
		ch <- m.Desc
	}
	ch <- omnilogicInfo
	ch <- omnilogicUp
	ch <- e.totalScrapes.Desc()
	ch <- e.xmlParseFailures.Desc()
}


func fetchHTTP(uri string, sslVerify bool, timeout time.Duration) func() (io.ReadCloser, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !sslVerify}}
	client := http.Client{
		Timeout:   timeout,
		Transport: tr,
	}

	return func() (io.ReadCloser, error) {
		resp, err := client.Get(uri)
		if err != nil {
			return nil, err
		}
		if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
}


func (e *Exporter) parseInfo(i io.Reader) (versionInfo, error) {
	var version, releaseDate string
	version = "1.2.3"
	releaseDate = "now"
	return versionInfo{ReleaseDate: releaseDate, Version: version}, nil //, s.Err()
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
}

func (e *Exporter) scrape(ch chan<- prometheus.Metric) (up float64) {
	e.totalScrapes.Inc()
	var err error

	if e.fetchInfo != nil {
		infoReader, err := e.fetchInfo()
		if err != nil {
			level.Error(e.logger).Log("msg", "Can't scrape OmniLogic", "err", err)
			return 0
		}
		defer infoReader.Close()

		info, err := e.parseInfo(infoReader)
		if err != nil {
			level.Debug(e.logger).Log("msg", "Failed parsing show info", "err", err)
		} else {
			ch <- prometheus.MustNewConstMetric(omnilogicInfo, prometheus.GaugeValue, 1, info.ReleaseDate, info.Version)
		}
	}

	body, err := e.fetchStat()
	if err != nil {
		level.Error(e.logger).Log("msg", "Can't scrape OmniLogic", "err", err)
		return 0
	}
	defer body.Close()

	return 1
}

type versionInfo struct {
	ReleaseDate string
	Version     string
}

func main() {

	var (
		webConfig                  = webflag.AddFlags(kingpin.CommandLine)
		listenAddress              = kingpin.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":9190").String()
		metricsPath                = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
		omniLogicTimeout           = kingpin.Flag("omnilogic.timeout", "Timeout for trying to get stats from OmniLogic.").Default("5s").Duration()
		omniLogicUserName          = kingpin.Flag("omnilogic.username", "UserName to login to OmniLogic.").Required().String()
		omniLogicPassword          = kingpin.Flag("omnilogic.password", "Password to login to OmniLogic.").Required().String()
	)

	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("omnilogic_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting omnilogic_exporter", "version", version.Info())
	level.Info(logger).Log("msg", "Build context", "context", version.BuildContext())

	exporter, err := NewExporter(*omniLogicUserName, *omniLogicPassword, *omniLogicTimeout, logger)
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
