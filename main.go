package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"errors"
	"path/filepath"
	"gopkg.in/yaml.v3"
	"github.com/distatus/battery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
   	"golang.org/x/sys/windows/svc"
    "golang.org/x/sys/windows/svc/debug"

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

type myService struct{}

func (m *myService) Execute(config Config, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)

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
    go startHTTPServer(&waitGroup, config.Port, config.Pattern)

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
    waitGroup.Wait()
    status <- svc.Status{State: svc.StopPending}
    return false, 1
}

func runService(config Config, isDebug bool) {
    if isDebug {
        err := debug.Run(config.Name, &myService{})
        if err != nil {
            log.Fatalln("Error running service in debug mode.")
        }
    } else {
        err := svc.Run(config, &myService{})
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
	
	ch <- prometheus.MustNewConstMetric(collector.batteryGauge, prometheus.GaugeValue, percent)
}

func startHTTPServer(waitGroup *sync.WaitGroup, port string, pattern string) {
	server := &http.Server{
		Addr: port,
	}
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		defer waitGroup.Done()
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
            log.Fatalf("HTTP server error: %v", err)
        }
        log.Println("Stopped serving new connections.")
    }()

    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
    defer shutdownRelease()

    if err := server.Shutdown(shutdownCtx); err != nil {
        log.Fatalf("HTTP shutdown error: %v", err)
    }
    log.Println("Graceful shutdown complete.")

}

func main() {
	fp, _ := filepath.Abs("./config.yaml")
	yamlFile, err := os.ReadFile(fp)
	if err != nil {
		log.Fatal("Cannot read config file...")
	}
	
	var config Config
	
	if err := yaml.Unmarshal(yamlFile, &config)
	err != nil {
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