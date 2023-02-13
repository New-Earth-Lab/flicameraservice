package main

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/New-Earth-Lab/flicamera/flicamera"
	"github.com/lirm/aeron-go/aeron"
	"github.com/lirm/aeron-go/aeron/logging"
)

var wg sync.WaitGroup
var logger = logging.MustGetLogger("flicamera")
var interrupt = make(chan os.Signal, 1)
var done = make(chan bool)

const (
	// channel         = "aeron:ipc?fc=max"
	channel         = "aeron:udp?endpoint=localhost:20121|fc=max"
	driverTimeoutMs = 10000
	streamID        = 1001
	loggingOn       = false
)

func init() {
	signal.Notify(interrupt, os.Interrupt)
	signal.Notify(interrupt, syscall.SIGTERM)
}

func main() {
	wg.Add(1)

	// Setup Aeron context
	if !loggingOn {
		logging.SetLevel(logging.INFO, "aeron")
		logging.SetLevel(logging.INFO, "memmap")
		logging.SetLevel(logging.INFO, "driver")
		logging.SetLevel(logging.INFO, "counters")
		logging.SetLevel(logging.INFO, "logbuffers")
		logging.SetLevel(logging.INFO, "buffer")
		logging.SetLevel(logging.INFO, "rb")
	}

	errorHandler := func(err error) {
		done <- true
		logger.Warning(err)
	}
	to := time.Duration(time.Millisecond.Nanoseconds() * driverTimeoutMs)
	ctx := aeron.NewContext().MediaDriverTimeout(to).ErrorHandler(errorHandler)

	a, err := aeron.Connect(ctx)
	if err != nil {
		logger.Fatalf("Failed to connect to media driver: %s\n", err.Error())
	}
	defer a.Close()

	publication, err := a.AddPublication(channel, streamID)
	if err != nil {
		logger.Fatalf(err.Error())
	}
	defer publication.Close()
	logger.Infof("Publication found %v", publication)

	go Camera(publication, done)

loop:
	for {
		select {
		case <-interrupt:
			done <- true
			break loop
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	wg.Wait()
}

func Camera(publication *aeron.Publication, done chan bool) {
	defer wg.Done()

	cam, err := flicamera.NewFliCamera(publication)
	if err != nil {
		logger.Fatalf(err.Error())
	}

	err = cam.StartCamera()
	if err != nil {
		logger.Fatalf(err.Error())
	}

	for {
		select {
		case <-done:
			logger.Infof("Shutting down")
			cam.StopCamera()
			cam.Shutdown()
			return
		default:
			time.Sleep(100 * time.Microsecond)
		}
	}

}
