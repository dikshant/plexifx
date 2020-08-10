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
	mu              sync.RWMutex
	incomingDevices chan lifxlan.Device
	devices         map[string]*deviceWrapper
	done            chan struct{}
}

type deviceWrapper struct {
	device   lifxlan.Device
	conn     net.Conn
	lastSeen *time.Time
}

// New returns a new Lifx light
func New(log *zap.Logger, broadcastAddress string, broadcastInterval time.Duration) (*Lifx, error) {
	lifx := &Lifx{
		devices:         make(map[string]*deviceWrapper),
		mu:              sync.RWMutex{},
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

	//Resolve broadcast address so we can send a message
	broadcast, err := net.ResolveUDPAddr(
		"udp",
		broadcastAddress,
	)
	if err != nil {
		lifx.log.Sugar().Errorf("failed to resolve broadcast address: %s", err)
	}

	// Message to be used for during broadcasts broadcast
	msg, err := lifxlan.GenerateMessage(lifxlan.Tagged, 0, lifxlan.AllDevices, 0, 0, lifxlan.GetService, nil)
	if err != nil {
		lifx.log.Sugar().Errorf("failed to generate broadcast message: %s", err)
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
				lifx.log.Info("Sending broadcast for discovery.")
				n, err := conn.WriteTo(msg, broadcast)
				if err != nil {
					lifx.log.Sugar().Errorf("failed to write broadcast message: %s", err)
				}
				if n < len(msg) {
					lifx.log.Sugar().Errorf(
						"lifxlan.Discover: only wrote %d out of %d bytes",
						n,
						len(msg),
					)
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
				lifx.log.Sugar().Errorf("Connecting to device failed: %v\n", err)
			}
			connectionTime := time.Now()
			// Save the device and its connection if the device does not already exist
			lifx.mu.Lock()
			if _, ok := lifx.devices[device.Target().String()]; !ok {
				err := device.GetLabel(context.Background(), conn)
				if err != nil {
					lifx.log.Sugar().Errorf("Failed to get device name: %s\n", err)
				}
				lifx.log.Sugar().Infof("Found a new device: %s", device.Label())
				lifx.devices[device.Target().String()] = &deviceWrapper{
					device:   device,
					conn:     conn,
					lastSeen: &connectionTime,
				}
				lifx.log.Sugar().Infof("Total devices so far: %d", len(lifx.devices))
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
	for macAddr, devInfo := range lifx.devices {
		if devInfo == nil {
			continue
		}

		wg.Add(1)
		go func(devInfo *deviceWrapper, macAddr string) {
			defer wg.Done()

			if devInfo.conn == nil {
				c, err := devInfo.device.Dial()
				if err != nil {
					lifx.log.Sugar().Errorf("Connecting to device failed: %v. Cannot power on.\n", err)
					return
				}
				// Update the last seen time which is the last time a connection was made to the device
				lastSeen := time.Now()
				devInfo.lastSeen = &lastSeen
				devInfo.conn = c
				// Update the device's stored conection
				lifx.devices[macAddr] = devInfo
			}
			// Set the power state for every device
			err := devInfo.device.SetPower(ctx, devInfo.conn, p, true)
			if err == nil {
				multierror.Append(errors, err)
				return
			}
			// Update the last seen time if no error
			lastSeen := time.Now()
			devInfo.lastSeen = &lastSeen
			// Update the device's stored device
			lifx.devices[macAddr] = devInfo
		}(devInfo, macAddr)
	}
	wg.Wait()
	return fmt.Errorf("failed to turn on one or more lights: %s", errors)
}

// Close stops the discovery process
func (lifx *Lifx) Close() {
	// Close all stored device connections
	lifx.mu.Lock()
	defer lifx.mu.Unlock()
	for macAddr, devInfo := range lifx.devices {
		if devInfo == nil {
			continue
		}
		if devInfo.conn == nil {
			continue
		}
		err := devInfo.conn.Close()
		if err != nil {
			lifx.log.With(zap.Error(err), zap.Field{Key: "macAddress", String: macAddr}).Error("Could not gracefully close device connection.")
		}
	}
	// Close our device channel
	close(lifx.incomingDevices)
	// Close our done channel to signal a stop
	close(lifx.done)
}
