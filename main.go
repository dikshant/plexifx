package main

import (
	"fmt"
	"time"

	"github.com/dikshant/plexifx/lifx"
	"github.com/dikshant/plexifx/webhook"
	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
)

// Config contains address of the webhook listener
type Config struct {
	Address                 string        `default:"0.0.0.0:6060"`
	DeviceDiscoveryInterval time.Duration `default:"10s"`
	DeviceDiscoveryTimeout  time.Duration `default:"5s"`
}

func main() {
	var c Config
	err := envconfig.Process("PLEXLIF", &c)
	if err != nil {
		panic(fmt.Errorf("could not parse config: %s", err))
	}

	if c.DeviceDiscoveryInterval <= c.DeviceDiscoveryTimeout {
		panic("discovery interval cannot be less than or equal to discovery timeout")
	}

	logger, err := zap.NewProductionConfig().Build()
	if err != nil {
		panic(fmt.Errorf("could not instantiate logger: %s", err))
	}
	defer logger.Sync()

	// Start our webhook listener
	webhook.New(c.Address, lifx.New(logger, c.DeviceDiscoveryInterval, c.DeviceDiscoveryTimeout), logger).Listen()
}
