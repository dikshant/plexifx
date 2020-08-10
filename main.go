package main

import (
	"fmt"
	"os"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/dikshant/plexifx/lifx"
	"github.com/dikshant/plexifx/webhook"
	"github.com/kelseyhightower/envconfig"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
)

// Config contains address of the webhook listener
type Config struct {
	Address           string        `default:"localhost:6060" desc:"Address for the webhook listener in host:port form." required:"false"`
	DiscoveryInterval time.Duration `default:"60s" split_words:"true" desc:"How often to start a new discovery job." required:"false"`
	DiscoveryTimeout  time.Duration `default:"30s" split_words:"true" desc:"How long a discovery job should run for." required:"false"`
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

	if c.DiscoveryInterval <= c.DiscoveryTimeout {
		panic("discovery interval cannot be less than or equal to discovery timeout")
	}

	logger, err := zap.NewProductionConfig().Build()
	if err != nil {
		panic(fmt.Errorf("could not instantiate logger: %s", err))
	}
	defer logger.Sync()

	spew.Printf("Discovery interval %v: , Discovery timeout: %v\n", c.DiscoveryInterval, c.DiscoveryTimeout)
	// Start our webhook listener
	webhook.New(c.Address, lifx.New(logger, c.DiscoveryInterval, c.DiscoveryTimeout), logger).Listen()
}
