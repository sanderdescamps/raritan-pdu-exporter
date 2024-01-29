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
	infoLock     sync.Mutex

	systemMetrics     []SensorLog
	systemMetricsLock sync.Mutex
	// VariableLabels     prometheus.Labels
	// VariableLabelsLock sync.Mutex
	sensors     []Sensor
	sensorsLock sync.Mutex
	metrics     []SensorLog
	metricsLock sync.Mutex

	tickerRefresh *time.Ticker
	tickerPoll    *time.Ticker
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
		infoLock:          sync.Mutex{},
		systemMetricsLock: sync.Mutex{},
		sensorsLock:       sync.Mutex{},
		StaticLabels:      staticLabels,
	}
}

func (c *PrometheusCollector) Start() error {
	c.tickerRefresh = time.NewTicker(time.Duration(c.Settings.Interval*10) * time.Second)
	c.tickerPoll = time.NewTicker(time.Duration(c.Settings.Interval) * time.Second)
	failed := make(chan bool, 1)
	restore := make(chan bool, 1)
	failMode := false

	err := c.refreshInfo()
	if err != nil {
		failed <- true
	} else {
		c.pollMetrics()
	}

	go func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-failed:
				klog.Errorf("collector entered failmode for %s\n", c.DisplayName())
				c.tickerRefresh.Reset(time.Duration(c.Settings.Interval) * time.Second)
				c.tickerPoll.Stop()
				failMode = true
			case <-restore:
				klog.Errorf("collector exit failmode for %s\n", c.DisplayName())
				c.tickerRefresh.Reset(time.Duration(c.Settings.Interval*10) * time.Second)
				c.tickerPoll.Reset(time.Duration(c.Settings.Interval) * time.Second)
				failMode = false
			case <-c.tickerRefresh.C:
				err := c.refreshInfo()
				if err == nil {
					if failMode {
						restore <- true
					}
				} else if !failMode {
					failed <- true
				}
			case <-c.tickerPoll.C:
				err := c.pollMetrics()
				if err != nil {
					klog.Errorf("%s", err)
					failed <- true
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

	defer func() {
		c.infoLock.Lock()
		defer c.infoLock.Unlock()
		c.pduInfo = pduInfo
		c.snmpInfo = snmpInfo

		c.systemMetricsLock.Lock()
		defer c.systemMetricsLock.Unlock()
		c.systemMetrics = systemMetrics

		c.sensorsLock.Lock()
		defer c.sensorsLock.Unlock()
		if len(sensors) > 0 {
			c.sensors = sensors
		} else {
			c.sensors = nil
		}
	}()

	conErr := c.client.TCPConnectionCheck()
	systemMetrics = append(systemMetrics, SensorLog{ //Backwards compatibillity
		Sensor: "pdu_active",
		Type:   "status",
		Time:   time.Now(),
		Value:  IfThenElse(conErr == nil, 1.0, 0.0),
	})
	systemMetrics = append(systemMetrics, SensorLog{
		Sensor: "tcp connection",
		Type:   "exporter_status",
		Time:   time.Now(),
		Value:  IfThenElse(conErr == nil, 1.0, 0.0),
	})
	if conErr != nil {
		return fmt.Errorf("failed to connect to %s[%s]", c.Name, c.client.BaseURL.String())
	}

	//PDU Info
	var err error
	pduInfo, err = c.client.GetPDUInfo()
	systemMetrics = append(systemMetrics, SensorLog{
		Sensor: "pdu info",
		Type:   "exporter_status",
		Label:  "pdu info",
		Time:   time.Now(),
		Value:  IfThenElse(err == nil, 1.0, 0.0),
	})
	if err != nil {
		return fmt.Errorf("failed to get pdu info for %s[%s]", c.Name, c.client.BaseURL.String())
	}

	//PDU SNMP Info
	if c.Settings.SNMPSydLocation || c.Settings.SNMPSysContact || c.Settings.SNMPSysName {
		snmpInfo, err = c.client.GetSNMPInfo()
		systemMetrics = append(systemMetrics, SensorLog{
			Sensor: "snmp info",
			Type:   "exporter_status",
			Label:  "snmp info",
			Time:   time.Now(),
			Value:  IfThenElse(err == nil, 1.0, 0.0),
		})
		if err != nil {
			return fmt.Errorf("failed to get snmp info for %s[%s]", c.Name, c.client.BaseURL.String())
		}
	}

	// Inlets
	iis, err := c.client.GetInletsInfo()
	systemMetrics = append(systemMetrics, SensorLog{
		Sensor: "inlet info",
		Type:   "exporter_status",
		Time:   time.Now(),
		Value:  IfThenElse(err == nil, 1.0, 0.0),
	})
	if err != nil {
		return fmt.Errorf("failed to get inlet info for %s[%s]", c.Name, c.client.BaseURL.String())
	} else {
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
	systemMetrics = append(systemMetrics, SensorLog{
		Sensor: "outlet info",
		Type:   "exporter_status",
		Time:   time.Now(),
		Value:  IfThenElse(err == nil, 1.0, 0.0),
	})
	if err != nil {
		return fmt.Errorf("failed to get outlet info for %s[%s]", c.Name, c.client.BaseURL.String())
	} else {
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
	systemMetrics = append(systemMetrics, SensorLog{
		Sensor: "ocp info",
		Type:   "exporter_status",
		Time:   time.Now(),
		Value:  IfThenElse(err == nil, 1.0, 0.0),
	})
	if err != nil {
		return fmt.Errorf("failed to get OCP info for %s[%s]", c.Name, c.client.BaseURL.String())
	} else {
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

	klog.Infof("%d sensors found for %s\n", len(sensors), c.DisplayName())

	return nil
}

func (c *PrometheusCollector) pollMetrics() error {
	cSensor := c.sensors
	if cSensor == nil {
		return fmt.Errorf("no sensors available for %s", c.Name)
	}
	reources := make([]raritan.Resource, len(cSensor))
	for i, sensor := range cSensor {
		reources[i] = sensor.Resource
	}
	sensorReadings, err := c.client.GetSensorReadings(reources)
	if err != nil {
		return fmt.Errorf("error getting sensor data: %w", err)
	}
	logs := []SensorLog{}
	for i, r := range sensorReadings {
		if !r.Available {
			klog.V(4).Infof("sensor %s not available", cSensor[i].Resource.RID)
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

	func() {
		klog.Infof("poll all sensors for %s\n", c.DisplayName())
		c.metricsLock.Lock()
		defer c.metricsLock.Unlock()
		c.metrics = logs
	}()
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
		return c.Name
	} else {
		return c.client.BaseURL.Host
	}
}
