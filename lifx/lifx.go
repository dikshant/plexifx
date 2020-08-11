package lifx

import (
	"bytes"
	"context"
	"encoding/binary"
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
	log             *zap.Logger
	incomingDevices chan lifxlan.Device
	devices         *sync.Map
	done            chan struct{}
}

type deviceWrapper struct {
	device lifxlan.Device
	conn   net.Conn
}

// New returns a new Lifx light
func New(log *zap.Logger, broadcastAddress string, broadcastInterval time.Duration) (*Lifx, error) {
	lifx := &Lifx{
		devices:         &sync.Map{},
		incomingDevices: make(chan lifxlan.Device),
		log:             log,
		done:            make(chan struct{}),
	}

	err := lifx.discover(broadcastAddress, broadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("could not start discovery process: %s", err)
	}
	go lifx.process()

	return lifx, nil
}

func (lifx *Lifx) discover(broadcastAddress string, broadcastInterval time.Duration) error {

	_, port, err := net.SplitHostPort(broadcastAddress)
	if err != nil {
		return fmt.Errorf("invalid host port for brioadcast address: %s", err)
	}

	// Start a broadcast listener
	conn, err := net.ListenPacket("udp", ":"+port)
	if err != nil {
		return fmt.Errorf("failed to listen for UDP packets on address: %s", err)
	}

	go func() {
		// Send a new braodacst every broadcast interval
		ticker := time.NewTicker(broadcastInterval)
		for ; true; <-ticker.C {
			select {
			case <-lifx.done:
				conn.Close()
				return
			default:
				//Resolve broadcast address so we can send a message
				broadcast, err := net.ResolveUDPAddr(
					"udp",
					broadcastAddress,
				)
				if err != nil {
					lifx.log.Sugar().Errorf("Failed to resolve broadcast address: %s", err)
				}

				// Message to be used for during broadcasts broadcast
				msg, err := lifxlan.GenerateMessage(lifxlan.Tagged, 0, lifxlan.AllDevices, 0, 0, lifxlan.GetService, nil)
				if err != nil {
					lifx.log.Sugar().Errorf("Failed to generate broadcast message: %s", err)
				}

				// Send broadcast
				lifx.log.Info("Sending broadcast packet for discovering devices.")
				n, err := conn.WriteTo(msg, broadcast)
				if err != nil {
					lifx.log.Sugar().Errorf("Failed to write broadcast message: %s", err)
					return
				}
				if n < len(msg) {
					lifx.log.Sugar().Errorf("Only wrote %d out of %d bytes", n, len(msg))
					return
				}
			}
		}
	}()

	// listen for any new devices after broadcasting
	go lifx.listenForDevices(conn)
	return nil
}

// Discover will publish discovered devices
func (lifx *Lifx) listenForDevices(conn net.PacketConn) error {
	buf := make([]byte, lifxlan.ResponseReadBufferSize)
	lifx.log.Info("Listening for devices.")
	for {
		select {
		case <-lifx.done:
			return conn.Close()
		default:
		}
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			lifx.log.Sugar().Errorf("Failed to read from connection: %s", err)
			continue
		}

		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			lifx.log.Sugar().Errorf("Failed to parse address: %s", err)
			continue
		}

		resp, err := lifxlan.ParseResponse(buf[:n])
		if err != nil {
			lifx.log.Sugar().Errorf("Failed to parse response: %s", err)
			continue
		}
		if resp.Message != lifxlan.StateService {
			// We don't care about other types of responses
			continue
		}

		var d lifxlan.RawStateServicePayload
		r := bytes.NewReader(resp.Payload)
		// Marshal incoming data into a Lifx device
		if err := binary.Read(r, binary.LittleEndian, &d); err != nil {
			lifx.log.Sugar().Errorf("Failed to marshal binary data into a Lifx device. %s", err)
			continue
		}
		switch d.Service {
		default:
			// Unknown service, ignore.
			continue
		case lifxlan.ServiceUDP:
			lifx.incomingDevices <- lifxlan.NewDevice(
				net.JoinHostPort(host, fmt.Sprintf("%d", d.Port)),
				d.Service,
				resp.Target,
			)
		}
	}
}

// process any devices coming from the incoming devices channel
func (lifx *Lifx) process() {
	for {
		select {
		case <-lifx.done:
			return
		case device := <-lifx.incomingDevices:
			if device == nil {
				continue
			}
			// Dial to each device so we can store the connection for later use
			conn, err := device.Dial()
			if err != nil {
				lifx.log.Sugar().Errorf("Connecting to device failed: %v", err)
			}
			err = device.GetLabel(context.Background(), conn)
			if err != nil {
				lifx.log.Sugar().Errorf("Failed to get device name: %s", err)
			}

			// Store if the device isn't present
			_, loaded := lifx.devices.LoadOrStore(device.Target().String(), &deviceWrapper{
				device: device,
				conn:   conn,
			})
			if !loaded {
				lifx.log.Sugar().Infof("Found a new device: %s", device.Label().String())
			}
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

	lifx.devices.Range(func(k, v interface{}) bool {
		wg.Add(1)
		go func(devInfo *deviceWrapper, macAddr string) {
			defer wg.Done()

			if devInfo.conn == nil {
				c, err := devInfo.device.Dial()
				if err != nil {
					lifx.log.Sugar().Errorf("Connecting to device failed: %v. Cannot power on.", err)
					return
				}
				// Update the device's stored conection
				devInfo.conn = c
				lifx.devices.Store(k, devInfo)
			}
			// Set the power state for every device
			err := devInfo.device.SetPower(ctx, devInfo.conn, p, false)
			if err == nil {
				multierror.Append(errors, err)
				return
			}
		}(v.(*deviceWrapper), k.(string))
		return true
	})

	wg.Wait()
	return fmt.Errorf("failed to turn on one or more lights: %s", errors)
}

// Close stops the discovery process
func (lifx *Lifx) Close() {
	// Close all stored device connections
	var wg sync.WaitGroup
	lifx.devices.Range(func(k, v interface{}) bool {
		wg.Add(1)
		go func(devInfo *deviceWrapper) {
			defer wg.Done()
			err := devInfo.conn.Close()
			if err != nil {
				lifx.log.With(zap.Error(err), zap.Field{Key: "macAddress", String: k.(string)}).Error("Could not gracefully close device connection.")
			}
		}(v.(*deviceWrapper))
		lifx.devices.Delete(k)
		return true
	})
	lifx.log.Info("Waiting to close Lifx connections.")
	wg.Wait()
	lifx.log.Info("Finished closing Lifx connections.")
	// Close our done channel to signal a stop
	close(lifx.done)
	// Close our device channel
	close(lifx.incomingDevices)
}
