package main

import (
	"fmt"
	"time"

	"github.com/dikshant/plexifx/webhook"
	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
)

// Config contains address of the webhook listener
type Config struct {
	Address                 string        `default:"0.0.0.0:6060"`
	DeviceDiscoveryInterval time.Duration `default:"30m"`
}

func main() {
	var c Config
	err := envconfig.Process("PLEXLIF", &c)
	if err != nil {
		panic(fmt.Errorf("could not parse config: %s", err))
	}

	logger, err := zap.NewProductionConfig().Build()
	if err != nil {
		panic(fmt.Errorf("could not instantiate logger: %s", err))
	}
	defer logger.Sync()
	webhook.New(c.Address, c.DeviceDiscoveryInterval, logger).Listen()
}
