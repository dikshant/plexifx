package lifx

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/fishy/lifxlan"
	multierror "github.com/hashicorp/go-multierror"
	"go.uber.org/zap"
)

// Lifx provides methods to conduct actions on Lifx devices
type Lifx struct {
	log               *zap.Logger
	mu                sync.RWMutex
	devices           []lifxlan.Device
	discoveryInterval time.Duration
}

// New returns a new Lifx light
func New(log *zap.Logger, interval time.Duration) *Lifx {
	return &Lifx{
		devices:           []lifxlan.Device{},
		mu:                sync.RWMutex{},
		log:               log,
		discoveryInterval: interval,
	}
}

// Discover runs every 10 minutes and updates the list of available lights
func (lifx *Lifx) Discover() {
	ticker := time.NewTicker(lifx.discoveryInterval)
	incomingDevices := make(chan lifxlan.Device)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// fire ticket immediately and once every 10 minutes there after
	for ; true; <-ticker.C {
		go func() {
			if err := lifxlan.Discover(ctx, incomingDevices, ""); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
				log.Printf("Discovery failed: %v\n", err)
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case device := <-incomingDevices:
				lifx.mu.Lock()
				lifx.devices = append(lifx.devices, device)
				lifx.mu.Unlock()
			}
		}
	}
}

// Lights returns a map of available lights
func (lifx *Lifx) Lights() []lifxlan.Device {
	lifx.mu.RLock()
	defer lifx.mu.RUnlock()
	return lifx.devices
}

// On will turn on the lights
func (lifx *Lifx) On(ctx context.Context) error {
	lifx.mu.RLock()
	defer lifx.mu.RUnlock()

	var wg sync.WaitGroup
	var errors error
	for _, device := range lifx.devices {
		wg.Add(1)
		go func(device lifxlan.Device) {
			err := device.SetPower(ctx, nil, lifxlan.PowerOn, true)
			if err != nil {
				multierror.Append(errors, err)
			}
			wg.Done()
		}(device)
	}
	wg.Wait()
	return fmt.Errorf("failed to turn on one or more lights: %s", errors)
}

// Off will turn off the lights
func (lifx *Lifx) Off(ctx context.Context) error {
	lifx.mu.RLock()
	defer lifx.mu.RUnlock()

	var errors error
	var wg sync.WaitGroup
	for _, device := range lifx.devices {
		wg.Add(1)
		go func(device lifxlan.Device) {
			err := device.SetPower(ctx, nil, lifxlan.PowerOff, true)
			if err != nil {
				multierror.Append(errors, err)
			}
			wg.Done()
		}(device)
	}
	wg.Wait()
	return fmt.Errorf("failed to turn off one or more lights: %s", errors)
}
