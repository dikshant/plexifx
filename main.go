package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dikshant/plexifx/lifx"
	"github.com/dikshant/plexifx/webhook"
	"github.com/kelseyhightower/envconfig"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
)

// Config contains address of the webhook listener
type Config struct {
	Address           string        `default:"localhost:6060" desc:"Address for the webhook listener in host:port form." required:"true"`
	BroadcastAddress  string        `default:"255.255.255.255:56700" desc:"Address for Lifx broadcast host used for device discovery." required:"true"`
	BroadcastInterval time.Duration `default:"60s" desc:"Interval for sending out a broadcast for discovery. Default is 60 seconds." required:"true"`
}

func main() {
	var help bool
	flag.BoolVarP(&help, "help", "h", false, "Show the help information and exit")
	flag.Parse()
	var c Config
	err := envconfig.Process("PLEXIFX", &c)
	if err != nil {
		panic(fmt.Errorf("could not parse config: %s", err))
	}

	if help {
		err = envconfig.Usagef("PLEXIFX", &c, os.Stdout, envconfig.DefaultListFormat)
		if err != nil {
		}
		os.Exit(0)
	}

	logger, err := zap.NewProductionConfig().Build()
	if err != nil {
		panic(fmt.Errorf("could not instantiate logger: %s", err))
	}
	defer logger.Sync()

	lifx, err := lifx.New(logger, c.BroadcastAddress, c.BroadcastInterval)
	if err != nil {
		panic(fmt.Errorf("could not create a lifx instance: %s", err))
	}

	// Start our webhook listener
	webhook := webhook.New(c.Address, lifx, logger)
	cleanup(webhook.Close, logger)
	webhook.Listen()
}

func cleanup(closer func() error, log *zap.Logger) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for sig := range c {
			switch sig {
			case syscall.SIGTERM, syscall.SIGINT:
				log.Sugar().Infof("Received signal: %v. Exiting...", sig)
				err := closer()
				if err != nil {
					log.Sugar().Errorf("Did not gracefully exit: %s", err)
				}
			default:
				continue
			}
		}
	}()
}
