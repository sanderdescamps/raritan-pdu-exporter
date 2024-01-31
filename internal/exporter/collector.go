package exporter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/iancoleman/strcase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tanenbaum/raritan-pdu-exporter/internal/raritan"
	"k8s.io/klog/v2"
)

const (
	namespace               = "pdu"
	ALLOWED_SETTINGS_BOOL   = "serial_number,snmp_sys_contact,snmp_sys_name,snmp_sys_location"
	ALLOWED_SETTINGS_INT    = "interval"
	METRIC_TCP_CONNECTIVITY = "TCP connectivity"
	METRIC_PDU_INFO         = "PDU info"
	METRIC_SNMP_INFO        = "SNMP info"
	METRIC_INLET_INFO       = "inlet info"
	METRIC_OUTLET_INFO      = "outlet info"
	METRIC_OCP_INFO         = "ocp info"
)

type Sensor struct {
	Type     string
	Label    string
	Sensor   string
	Resource raritan.Resource
}

type SensorLog struct {
	Type   string
	Label  string
	Sensor string
	Time   time.Time
	Value  float64
}

type Settings struct {
	UseConfigName   bool
	SerialNumber    bool
	SNMPSysContact  bool
	SNMPSysName     bool
	SNMPSydLocation bool
	Interval        int
}

type PrometheusCollector struct {
	ctx          context.Context
	client       raritan.Client
	Name         string
	Settings     Settings
	StaticLabels prometheus.Labels
	pduInfo      *raritan.PDUInfo
	snmpInfo     *raritan.SNMPInfo

	systemMetrics     []SensorLog
	systemMetricsLock sync.Mutex
	sensors           []Sensor
	metrics           []SensorLog
	metricsLock       sync.Mutex

	ticker *time.Ticker
}

func NewPrometheusCollector(ctx context.Context, client raritan.Client, name string, staticLabels map[string]string) *PrometheusCollector {
	return &PrometheusCollector{
		ctx:    ctx,
		client: client,
		Name:   name,
		Settings: Settings{
			UseConfigName:   true,
			SerialNumber:    true,
			SNMPSysContact:  false,
			SNMPSysName:     false,
			SNMPSydLocation: false,
			Interval:        30,
		},
		metricsLock:       sync.Mutex{},
		systemMetricsLock: sync.Mutex{},
		StaticLabels:      staticLabels,
	}
}

func (c *PrometheusCollector) Start() error {
	c.ticker = time.NewTicker(time.Duration(c.Settings.Interval) * time.Second)
	counter := 0
	trigger := make(chan bool, 1)
	trigger <- true

	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-c.ticker.C:
				trigger <- true
			case <-trigger:
				if counter%10 == 0 {
					err := c.refreshInfo()
					if err != nil {
						klog.Errorf("%s", err)
						counter = 0
					}
				}
				if c.sensors != nil {
					err := c.pollMetrics()
					if err != nil {
						klog.Errorf("%s", err)
						counter = 0
					} else {
						counter = (counter + 1) % 10
					}
				}
			}
		}
	}()
	return nil
}

func (c *PrometheusCollector) variableLabels() prometheus.Labels {
	vLabels := prometheus.Labels{
		"pdu_name": c.Name,
	}

	cPduInfo := c.pduInfo
	if cPduInfo != nil && !c.Settings.UseConfigName {
		vLabels["pdu_name"] = cPduInfo.Name
	} else {
		vLabels["pdu_name"] = c.Name
	}

	cSNMPInfo := c.snmpInfo
	if cSNMPInfo != nil && c.Settings.SNMPSysName {
		vLabels["snmp_sys_name"] = cSNMPInfo.SysName
	}
	if cSNMPInfo != nil && c.Settings.SNMPSydLocation {
		vLabels["snmp_sys_location"] = cSNMPInfo.SysLocation
	}
	if cSNMPInfo != nil && c.Settings.SNMPSysContact {
		vLabels["snmp_sys_contact"] = cSNMPInfo.SysContact
	}
	return vLabels
}

func (c *PrometheusCollector) refreshInfo() error {
	systemMetrics := []SensorLog{}
	sensors := []Sensor{}
	var pduInfo *raritan.PDUInfo
	var snmpInfo *raritan.SNMPInfo
	successTcpConnect := false
	successPduInfo := false
	successSnmpInfo := false
	successInletInfo := false
	successOutletInfo := false
	successOcpInfo := false

	defer func() {
		c.pduInfo = pduInfo
		c.snmpInfo = snmpInfo

		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "tcp connection",
			Type:   "exporter_status",
			Time:   time.Now(),
			Value:  boolToFloat64(successTcpConnect),
		})
		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "pdu info",
			Type:   "exporter_status",
			Label:  "pdu info",
			Time:   time.Now(),
			Value:  boolToFloat64(successPduInfo),
		})
		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "snmp info",
			Type:   "exporter_status",
			Label:  "snmp info",
			Time:   time.Now(),
			Value:  boolToFloat64(successSnmpInfo),
		})
		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "inlet info",
			Type:   "exporter_status",
			Time:   time.Now(),
			Value:  boolToFloat64(successInletInfo),
		})
		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "outlet info",
			Type:   "exporter_status",
			Time:   time.Now(),
			Value:  boolToFloat64(successOutletInfo),
		})
		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "ocp info",
			Type:   "exporter_status",
			Time:   time.Now(),
			Value:  boolToFloat64(successOcpInfo),
		})

		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "pdu_active",
			Type:   "status",
			Time:   time.Now(),
			Value:  boolToFloat64(successTcpConnect && successPduInfo && successInletInfo && successOutletInfo && successOcpInfo),
		})

		c.systemMetricsLock.Lock()
		defer c.systemMetricsLock.Unlock()
		c.systemMetrics = systemMetrics

		if len(sensors) > 0 {
			c.sensors = sensors
		} else {
			c.sensors = nil
		}
	}()

	conErr := c.client.TCPConnectionCheck()
	if conErr != nil {
		return fmt.Errorf("%s: failed to connect: %s", c.DisplayName(), conErr.Error())
	} else {
		successTcpConnect = true
	}

	//PDU Info
	var err error
	pduInfo, err = c.client.GetPDUInfo()
	if err != nil {
		return fmt.Errorf("%s: failed to get pdu info: %s", c.DisplayName(), err.Error())
	} else {
		successPduInfo = true
	}

	//PDU SNMP Info
	if c.Settings.SNMPSydLocation || c.Settings.SNMPSysContact || c.Settings.SNMPSysName {
		snmpInfo, err = c.client.GetSNMPInfo()
		if err != nil {
			return fmt.Errorf("%s: failed to get snmp info: %s", c.DisplayName(), err.Error())
		} else {
			successSnmpInfo = true
		}
	}

	// Inlets
	iis, err := c.client.GetInletsInfo()
	if err != nil {
		return fmt.Errorf("%s: failed to get inlet info: %s", c.DisplayName(), err.Error())
	} else {
		successInletInfo = true
		for _, i := range iis {
			for k, v := range i.Sensors {
				sensors = append(sensors, Sensor{
					Label:    i.Label,
					Type:     "inlet",
					Sensor:   k,
					Resource: v,
				})
			}
		}
	}

	// Outlets
	ois, err := c.client.GetOutletsInfo()
	if err != nil {
		return fmt.Errorf("%s: failed to get outlet info: %s", c.DisplayName(), err.Error())
	} else {
		successOutletInfo = true
		for _, o := range ois {
			for k, v := range o.Sensors {
				sensors = append(sensors, Sensor{
					Label:    o.Label,
					Type:     "outlet",
					Sensor:   k,
					Resource: v,
				})
			}
		}
	}

	// OCP
	ocp, err := c.client.GetOCPInfo()
	if err != nil {
		return fmt.Errorf("%s: failed to get OCP info: %s", c.DisplayName(), err.Error())
	} else {
		successOcpInfo = true
		for _, o := range ocp {
			for k, v := range o.Sensors {
				sensors = append(sensors, Sensor{
					Label:    o.Label,
					Type:     "ocp",
					Sensor:   k,
					Resource: v,
				})
			}
		}
	}

	klog.Infof("%s: successfully refreshed sensors (%d)\n", c.DisplayName(), len(sensors))

	return nil
}

func (c *PrometheusCollector) pollMetrics() error {
	logs := []SensorLog{}
	defer func() {
		c.metricsLock.Lock()
		defer c.metricsLock.Unlock()
		if len(logs) > 0 {
			c.metrics = logs
		} else {
			c.metrics = nil
		}
	}()

	cSensor := c.sensors
	if cSensor == nil || len(cSensor) < 1 {
		return fmt.Errorf("%s: no sensors available", c.DisplayName())
	}
	reources := make([]raritan.Resource, len(cSensor))
	for i, sensor := range cSensor {
		reources[i] = sensor.Resource
	}
	sensorReadings, err := c.client.GetSensorReadings(reources)
	if err != nil {
		return fmt.Errorf("%s: error getting sensor data %w", c.DisplayName(), err)
	}

	for i, r := range sensorReadings {
		if !r.Available {
			klog.Infof("%s: sensor %s not available", c.DisplayName(), cSensor[i].Resource.RID)
			continue
		}
		newLog := SensorLog{
			Type:   cSensor[i].Type,
			Label:  cSensor[i].Label,
			Value:  r.Value,
			Time:   time.Unix(int64(r.Timestamp), 0),
			Sensor: cSensor[i].Sensor,
		}

		logs = append(logs, newLog)
	}

	return nil
}

func (c *PrometheusCollector) Describe(desc chan<- *prometheus.Desc) {}

func (c *PrometheusCollector) Collect(metric chan<- prometheus.Metric) {
	metrics := func() []SensorLog {
		c.metricsLock.Lock()
		defer c.metricsLock.Unlock()
		c.systemMetricsLock.Lock()
		defer c.systemMetricsLock.Unlock()
		return append(c.metrics, c.systemMetrics...)
	}()
	for _, l := range metrics {
		help := fmt.Sprintf("%s sensor reading for %s", l.Type, l.Sensor)
		fqName := prometheus.BuildFQName(namespace, strings.ToLower(l.Type), strcase.ToSnake(l.Sensor))

		vLabels := c.variableLabels()
		if l.Label != "" {
			vLabels["label"] = l.Label
		}

		labelKeys := []string{}
		labelValues := []string{}
		for k, v := range vLabels {
			labelKeys = append(labelKeys, k)
			labelValues = append(labelValues, v)
		}

		desc := prometheus.NewDesc(fqName, help, labelKeys, c.StaticLabels)
		metric <- prometheus.NewMetricWithTimestamp(l.Time,
			prometheus.MustNewConstMetric(
				desc, prometheus.GaugeValue, l.Value, labelValues...,
			),
		)
	}
}

func (c *PrometheusCollector) Match(patterns []string) bool {
	return matchAnyFilter(c.Name, patterns)
}

func (c *PrometheusCollector) DisplayName() string {
	if c.Name != "" {
		return fmt.Sprintf("%s[%s]", c.Name, c.client.BaseURL.Host)
	} else {
		return c.client.BaseURL.Host
	}
}
