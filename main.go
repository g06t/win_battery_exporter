package main

import (
	"context"
	"errors"
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
}

func (m *myService) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {

    const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue
    tick := time.Tick(5 * time.Second)

    status <- svc.Status{State: svc.StartPending}

    status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

    label, err := os.Hostname()
    if err != nil {
    	log.Println("Could not get Hostname.") 	
    }
    label += "_battery_percent"
	collector := newCollector(label)
	prometheus.MustRegister(collector)
	server := &http.Server{
		Addr: m.config.Port,
	}
	http.Handle(m.config.Pattern, promhttp.Handler())
	go func() {
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
            log.Fatalf("HTTP server error: %v", err)
        }
        log.Println("Stopped serving new connections.")
    }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
loop:
    for {
        select {
        case <-tick:
            log.Print("Tick Handled...!")
        case c := <-r:
            switch c.Cmd {
            case svc.Interrogate:
                status <- c.CurrentStatus
            case svc.Stop, svc.Shutdown:
                log.Print("Shutting service...!")
				server.Shutdown(ctx)
                break loop
            case svc.Pause:
                status <- svc.Status{State: svc.Paused, Accepts: cmdsAccepted}
            case svc.Continue:
                status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
            default:
                log.Printf("Unexpected service control request #%d", c)
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
            log.Fatalln("Error running service in debug mode.")
        }
    } else {
        err := svc.Run(config.Name, svce)
        if err != nil {
            log.Fatalln("Error running service in Service Control mode.")
        }
    }
}

func newCollector(s string) *Collector {
	return &Collector{
		batteryGauge: prometheus.NewDesc(s,
		 "Display current total battery level.", nil, nil),
	}
}

func (collector *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.batteryGauge
}

func (collector *Collector) Collect(ch chan<- prometheus.Metric) {
	
	var totalCurrentCapacity float64 = 0.0
	var totalFullCapacity float64 = 0.0
	batteries, err := battery.GetAll()
		if err != nil {
			log.Println("Could not get battery info...")
		}
	for _, battery := range batteries {
		totalCurrentCapacity += battery.Current
		totalFullCapacity += battery.Full
	}
	if totalFullCapacity <= 0 {
		log.Println("WARN: Full capacity <= 0, returning 0")
		ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, 0.0)
	}
	percent := (totalCurrentCapacity / totalFullCapacity)*100
	if percent < 0 || percent > 100 {
		log.Println("WARN: Percent value out of range, returning 0")
		percent = 0.0
	}
	ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, percent)
}

func main() {
	fp, _ := filepath.Abs("E:/Program Files/service/config.yaml")
	yamlFile, err := os.ReadFile(fp)
	if err != nil {
		log.Fatal("Cannot read config file...")
	}
	
	var config Config
	
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		log.Fatal("Failed to unmarshal yaml file...")
	}
	
	f, err := os.OpenFile(config.Log, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
    if err != nil {
        log.Fatalln(fmt.Errorf("error opening file: %v", err))
    }
    defer f.Close()

    log.SetOutput(f)
    runService(config, false)

}