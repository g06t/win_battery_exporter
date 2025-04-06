package main

import (
	"fmt"
	"net/http"
	"time"
 	"golang.org/x/sys/windows/svc"
    "golang.org/x/sys/windows/svc/debug"
    "log"
    "os"
	"github.com/distatus/battery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type myService struct{}

func (m *myService) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {

    const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue
    tick := time.Tick(5 * time.Second)

    status <- svc.Status{State: svc.StartPending}

    status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

    recordMetrics()
    go func() {
        http.Handle("/metrics", promhttp.HandlerFor(newRegistry, promhttp.HandlerOpts{}))
        log.Println("Starting HTTP server on :9090")
        http.ListenAndServe(":9090", nil)
    }()

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

    status <- svc.Status{State: svc.StopPending}
    return false, 1
}

func runService(name string, isDebug bool) {
    if isDebug {
        err := debug.Run(name, &myService{})
        if err != nil {
            log.Fatalln("Error running service in debug mode.")
        }
    } else {
        err := svc.Run(name, &myService{})
        if err != nil {
            log.Fatalln("Error running service in Service Control mode.")
        }
    }
}

func fetchBattery() float64{
	var totalCurrentCapacity float64 = 0.0
    var totalFullCapacity float64 = 0.0

	batteries, err := battery.GetAll()
		if err != nil {
			log.Printf("Could not get battery info.: %v", err)
		}
	if len(batteries) == 0 {
        log.Println("No batteries found.")
    }

    for _, battery := range batteries {
        if battery.Full > 0 && battery.Current >= 0 {
            totalCurrentCapacity += battery.Current
            totalFullCapacity += battery.Full
        }
	}
    if totalFullCapacity <= 0{
        return 0.0
    }
	percent := (totalCurrentCapacity / totalFullCapacity) * 100
    return percent
}

func recordMetrics() {
	go func() {
		for {
			batterygauge.Set(fetchBattery())
			time.Sleep(5 * time.Second)
		}
	}()
}

var (
    newRegistry = prometheus.NewRegistry()
)

var (
	batterygauge = promauto.With(newRegistry).NewGauge(prometheus.GaugeOpts{
		Name: "battery_percentage",
		Help: "Current battery percentage.",
	})
)

func main() {
    f, err := os.OpenFile("E:/Program Files/service/debug.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
    if err != nil {
        log.Fatalln(fmt.Errorf("error opening file: %v", err))
    }
    defer f.Close()

    log.SetOutput(f)
    runService("win_battery_exporter", false)

}