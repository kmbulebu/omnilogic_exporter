package main

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"

	"os"
	"path"

	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type omnilogic struct {
	*httptest.Server
	response []byte
}

func newOmnilogic(response []byte) *omnilogic {
	h := &omnilogic{response: response}
	h.Server = httptest.NewServer(handler(h))
	return h
}

func handler(h *omnilogic) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Write(h.response)
	}
}

func handlerStale(exit chan bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		<-exit
	}
}

func expectMetrics(t *testing.T, c prometheus.Collector, fixture string, metricNames ...string) {
	exp, err := os.Open(path.Join("test", fixture))

	if err != nil {
		t.Fatalf("Error opening fixture file %q: %v", fixture, err)
	}
	if err := testutil.CollectAndCompare(c, exp, metricNames...); err != nil {
		t.Fatal("Unexpected metrics returned:", err)
	}
}

func expectFile(t *testing.T, actual string, fixture string) {
	exp, err := ioutil.ReadFile(path.Join("test", fixture))
	if err != nil {
		t.Fatalf("Error opening fixture file %q: %v", fixture, err)
	}
	expStr := string(exp)
	if string(expStr) != actual {
		t.Fatalf("Actual: %q Want: %v", actual, expStr)
	}
}

func TestBuildLoginRequest(t *testing.T) {
	exporter, err := NewExporter("https://example.org", "poolgal@example.org", "MyPassword", 1*time.Second, log.NewNopLogger())

	if err != nil {
		t.Fatal("Error creating Exporter.", err)
	}

	loginRequest, err := exporter.buildLoginRequest()

	expectFile(t, loginRequest, "login_request.xml")
}

func TestParseLoginResponse(t *testing.T) {
	fixtureText, err := ioutil.ReadFile(path.Join("test", "login_response.xml"))

	if err != nil {
		t.Fatal("Could not open and read text fixture file, login_response.xml", err)
	}

	session, err := parseLoginResponse(string(fixtureText))

	if err != nil {
		t.Fatal("Error parsing text fixture file, login_response.xml", err)
	}

	if "0" != session.Status {
		t.Fatal("Session Status was not 0.", session)
	}

	if "12345" != session.UserID {
		t.Fatal("Session UserID was not 12345.")
	}

	if "deadbeefdeadbeefdeadbeefdeadbeef" != session.Token {
		t.Fatal("Session Token was not deadbeefdeadbeefdeadbeefdeadbeef.")
	}

	if "Successfully" != session.StatusMessage {
		t.Fatal("Session StatusMessage was not Successfully.")
	}

}

func TestBuildSiteListRequest(t *testing.T) {
	exporter, err := NewExporter("https://example.org", "poolgal@example.org", "MyPassword", 1*time.Second, log.NewNopLogger())

	if err != nil {
		t.Fatal("Error creating Exporter.", err)
	}

	exporter.session = &Session{
		UserID: "12345",
	}

	siteListRequest, err := exporter.buildSiteListRequest()

	expectFile(t, siteListRequest, "get_site_list_request.xml")
}

func TestParseSiteListResponse(t *testing.T) {

	fixtureText, err := ioutil.ReadFile(path.Join("test", "get_site_list_response.xml"))

	if err != nil {
		t.Fatal("Could not open and read text fixture file, get_site_list_response.xml", err)
	}

	sites, err := parseSiteListResponse(string(fixtureText))

	if err != nil {
		t.Fatal("Error parsing text fixture file, get_site_list_response.xml", err)
	}

	if sites == nil {
		t.Fatal("Should not have returned nil for sites.")
	}

	if len(sites) != 2 {
		t.Fatalf("Expected two sites but found %v", len(sites))
	}

	homeSite := sites[0]
	beachSite := sites[1]

	if "54321" != homeSite.MspSystemID {
		t.Fatal("Home site MspSystemID was not 54321.", homeSite)
	}

	if "Home" != homeSite.BackyardName {
		t.Fatal("Home site BackyardName was not Home.", homeSite)
	}

	if "1600 Pennsylvania Avenue, NW Washington, DC, United States" != homeSite.Address {
		t.Fatal("Home site Address was not correct.", homeSite)
	}

	if 2 != homeSite.Status {
		t.Fatal("Home site Status was not 2.", homeSite)
	}

	if "98765" != beachSite.MspSystemID {
		t.Fatal("Beach site MspSystemID was not 54321.", beachSite)
	}

	if "Beach" != beachSite.BackyardName {
		t.Fatal("Beach site BackyardName was not Home.", beachSite)
	}

	if "101 Oceanfront Lane, Virginia, VA, United States" != beachSite.Address {
		t.Fatal("Beach site Address was not correct.", beachSite)
	}

	if 1 != beachSite.Status {
		t.Fatal("Beach site Status was not 2.", beachSite)
	}

}

func TestSiteStatusMetrics(t *testing.T) {
	fixtureText, err := ioutil.ReadFile(path.Join("test", "get_site_list_response.xml"))

	if err != nil {
		t.Fatal("Could not open and read text fixture file, get_site_list_response.xml", err)
	}

	exporter, err := NewExporter(newOmnilogic(fixtureText).URL, "poolgal@example.org", "MyPassword", 1*time.Second, log.NewNopLogger())

	exporter.session = &Session{
		UserID: "12345",
		Token:  "deadbeef",
		Status: "0",
	}

	ch := make(chan prometheus.Metric, 100)

	exporter.RefreshSiteList(ch)

	if err != nil {
		t.Fatal("Error creating Exporter.", err)
	}

	expectMetrics(t, exporter, "status.metrics", prometheus.BuildFQName(namespace, "site", "system_status"))
}

func TestTelemetryDataRequest(t *testing.T) {
	exporter, err := NewExporter("https://example.org", "poolgal@example.org", "MyPassword", 1*time.Second, log.NewNopLogger())

	if err != nil {
		t.Fatal("Error creating Exporter.", err)
	}

	exporter.session = &Session{
		UserID: "12345",
	}

	telemetryDataRequest, err := exporter.buildTelemetryDataRequest("54321")

	expectFile(t, telemetryDataRequest, "get_telemetry_data_request.xml")
}
