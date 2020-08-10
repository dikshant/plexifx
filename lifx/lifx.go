package lifx

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/fishy/lifxlan"
	multierror "github.com/hashicorp/go-multierror"
	"go.uber.org/zap"
)

// Lifx provides methods to conduct actions on Lifx devices
type Lifx struct {
	log     *zap.Logger
	mu      sync.RWMutex
	devices map[string]map[lifxlan.Device]net.Conn
}

// New returns a new Lifx light
func New(log *zap.Logger, interval time.Duration, timeout time.Duration) *Lifx {
	lifx := &Lifx{
		devices: make(map[string]map[lifxlan.Device]net.Conn),
		mu:      sync.RWMutex{},
		log:     log,
	}

	go func() {
		ticker := time.NewTicker(interval)
		for ; true; <-ticker.C {
			go lifx.discover(interval, timeout)
		}
	}()

	return lifx
}

func (lifx *Lifx) discover(interval time.Duration, timeout time.Duration) {

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lifx.log.Info("Discovering devices.")
	incomingDevices := make(chan lifxlan.Device)
	go func() {
		if err := lifxlan.Discover(ctx, incomingDevices, ""); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			lifx.log.Sugar().Errorf("Discovery failed: %v\n", err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			lifx.mu.RLock()
			lifx.log.Info("Discovery complete.")
			lifx.log.Sugar().Infof("Total %d devices.", len(lifx.devices))
			lifx.mu.RUnlock()
			return
		case device := <-incomingDevices:
			if device == nil {
				continue
			}
			conn, err := device.Dial()
			if err != nil {
				lifx.log.Sugar().Errorf("Connecting to device failed: %v\n", err)
			}
			lifx.mu.Lock()
			// Save the device if it does not exist
			if _, ok := lifx.devices[device.Target().String()]; !ok {
				err := device.GetLabel(ctx, conn)
				if err != nil {
					lifx.log.Sugar().Errorf("Failed to get device name: %s\n", err)
				}
				lifx.log.Sugar().Infof("Found a new device: %s", device.Label())
				lifx.devices[device.Target().String()] = map[lifxlan.Device]net.Conn{
					device: conn,
				}
			}
			lifx.mu.Unlock()
		}
	}
}

// Power will turn the light on or off depending on the bool power
func (lifx *Lifx) Power(ctx context.Context, power bool) error {
	p := lifxlan.PowerOff
	if power {
		p = lifxlan.PowerOn
	}

	var wg sync.WaitGroup
	var errors error

	lifx.mu.Lock()
	defer lifx.mu.Unlock()
	for _, devInfo := range lifx.devices {
		wg.Add(1)
		for device, conn := range devInfo {
			go func(device lifxlan.Device, conn net.Conn) {
				defer wg.Done()
				if conn == nil {
					c, err := device.Dial()
					if err != nil {
						lifx.log.Sugar().Errorf("Connecting to device failed: %v. Cannot power on.\n", err)
						return
					}
					conn = c
					// Update the stored conection
					devInfo[device] = c
				}
				err := device.SetPower(ctx, conn, p, true)
				if err != nil {
					multierror.Append(errors, err)
				}
			}(device, conn)
		}
	}
	wg.Wait()
	return fmt.Errorf("failed to turn on one or more lights: %s", errors)
}
