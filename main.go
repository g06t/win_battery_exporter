package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/distatus/battery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Port string `yaml:"port"`
	Log string `yaml:"log"`
	Pattern string `yaml:"pattern"`
	Name string `yaml:"name"`
}

type Collector struct {
	batteryGauge	*prometheus.Desc
}

type myService struct{
	config Config
	collector *Collector
}

func (m *myService) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {

    const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue

    status <- svc.Status{State: svc.StartPending}

    status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	if m.collector == nil {
		collector := newCollector()
		prometheus.MustRegister(collector)
	}

	server := &http.Server{
		Addr: m.config.Port,
	}
	http.Handle(m.config.Pattern, promhttp.Handler())
	go func() {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
            log.Fatalf("ERROR: HTTP server error: %v", err)
        }
        log.Println("INFO: Stopped serving new connections.")
    }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
loop:
    for {
        select {
        case c := <-r:
            switch c.Cmd {
            case svc.Interrogate:
                status <- c.CurrentStatus
            case svc.Stop, svc.Shutdown:
                log.Print("INFO: Shutting service.")
				server.Shutdown(ctx)
                break loop
            case svc.Pause:
                status <- svc.Status{State: svc.Paused, Accepts: cmdsAccepted}
            case svc.Continue:
                status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
            default:
                log.Printf("WARN: Unexpected service control request #%d", c)
            }
        }
    }
    status <- svc.Status{State: svc.StopPending}
    return false, 0
}

func runService(config Config, isDebug bool) {
    svce := &myService{config: config}
	if isDebug {
        err := debug.Run(config.Name, svce)
        if err != nil {
            log.Fatalln("ERROR: Error running service in debug mode.")
        }
    } else {
        err := svc.Run(config.Name, svce)
        if err != nil {
            log.Fatalln("ERROR: Error running service in Service Control mode.")
        }
    }
}

func newCollector() *Collector {
	return &Collector{
		batteryGauge: prometheus.NewDesc("current_battery_percent",
		 "Display current total battery level.", []string{"hostname"}, nil),
	}
}

func (collector *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.batteryGauge
}

func (collector *Collector) Collect(ch chan<- prometheus.Metric) {
	
	var totalCurrentCapacity float64 = 0.0
	var totalFullCapacity float64 = 0.0

	hostname, err := os.Hostname()
    if err != nil {
    	log.Println("WARN: Could not get Hostname.")
		hostname = "unknown" 	
    }

	batteries, err := battery.GetAll()
		if err != nil {
			log.Println("WARN: Could not get battery info.")
		}

	if len(batteries) < 1 {
		ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, 0.0, hostname)
		return
	}
		
	for _, battery := range batteries {
		totalCurrentCapacity += battery.Current
		totalFullCapacity += battery.Full
	}
	if totalFullCapacity <= 0 {
		log.Println("WARN: Full capacity <= 0, returning 0")
		ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, 0.0, hostname)
		return
	}

	percent := (totalCurrentCapacity / totalFullCapacity)*100

	if percent < 0 {
		ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, 0.0, hostname)
		return
	} else if percent > 100 {
		ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, 100.0, hostname)
		return
	}

	ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, percent, hostname)
}

func main() {
	confPtr := flag.String("path", "C:/Program Files/win_battery_exporter/config.yaml", "Specify path to config file.")

	var config Config
	
	fp, _ := filepath.Abs(*confPtr)
	yamlFile, err := os.ReadFile(fp)
	if err != nil {
		config.Port = ":9183"
		config.Name = "win_battery_exporter"
		config.Pattern = "/metrics"
		config.Log = "C:/Program Files/win_battery_exporter/debug.log"
	}
	
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		config.Port = ":9183"
		config.Name = "win_battery_exporter"
		config.Pattern = "/metrics"
		config.Log = "C:/Program Files/win_battery_exporter/debug.log"
	}
	
	f, err := os.OpenFile(config.Log, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
    if err != nil {
        log.Fatalln(fmt.Errorf("error opening file: %v", err))
    }
    defer f.Close()

	log.SetOutput(f)



    runService(config, false)

}