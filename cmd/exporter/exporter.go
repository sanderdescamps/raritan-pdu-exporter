package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tanenbaum/raritan-pdu-exporter/internal/exporter"
	"github.com/tanenbaum/raritan-pdu-exporter/internal/raritan"
	"github.com/tanenbaum/raritan-pdu-exporter/internal/rpc"
	"k8s.io/klog/v2"
)

var cltrs []*exporter.PrometheusCollector

func Run() {
	cltrs = []*exporter.PrometheusCollector{}
	config, err := LoadConfig(os.Args)
	if err != nil {
		klog.Exitf("%s", err)
	}

	Exporter(*config)
}

func createCollectors(ctx context.Context, conf Config) []*exporter.PrometheusCollector {
	cltrs = []*exporter.PrometheusCollector{}
	for _, pduConf := range conf.PduConfig {
		baseURL, err := url.Parse(pduConf.Url())
		if err != nil {
			klog.Exitf("Error parsing URL: %v", err)
		}

		q := raritan.Client{
			RPCClient: rpc.NewClient(time.Duration(pduConf.Timeout)*time.Second, rpc.Auth{
				Username: pduConf.Username,
				Password: pduConf.Password,
			}),
			BaseURL: *baseURL,
		}

		collector := exporter.NewPrometheusCollector(ctx, q, pduConf.Name, pduConf.StaticLabels)
		collector.Settings.UseConfigName = conf.ExporterLabels["use_config_name"]
		collector.Settings.SerialNumber = conf.ExporterLabels["serial_number"]
		collector.Settings.SNMPSysContact = conf.ExporterLabels["snmp_sys_contact"]
		collector.Settings.SNMPSysName = conf.ExporterLabels["snmp_sys_name"]
		collector.Settings.SNMPSydLocation = conf.ExporterLabels["snmp_sys_location"]
		collector.Settings.Interval = int(conf.Interval)
		cltrs = append(cltrs, collector)
	}
	return cltrs
}

func Exporter(conf Config) {
	ctx := context.Background()
	ctrlrs := createCollectors(ctx, conf)
	go func() {
		for _, c := range ctrlrs {
			c.Start()
		}
	}()

	r := mux.NewRouter()
	r.Use(logMW)
	klog.V(1).Infof("Starting Prometheus metrics server on %d", conf.Port)
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `PDU Metrics are at <a href="/metrics">/metrics<a>`)
	})
	r.HandleFunc("/metrics", metricsHandler)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", conf.Port), r); err != nil {
		klog.Errorf("HTTP server error: %v", err)
	}

}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()

	endpointFilter := []string{}
	if params.Has("name") {
		endpointFilter = append(endpointFilter, params.Get("name"))
	}

	for k, v := range params {
		if k == "name[]" {
			endpointFilter = append(endpointFilter, v...)
		}
	}

	registry := prometheus.NewRegistry()
	all := listContains(endpointFilter, "all") || len(endpointFilter) == 0
	for _, collector := range cltrs {
		if all || collector.Match(endpointFilter) {
			registry.MustRegister(collector)
		}
	}

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		klog.V(1).Infof("%s - %s (%s)", r.Method, r.URL.RequestURI(), r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
