package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/dikshant/plexifx/lifx"
	"github.com/hekmon/plexwebhooks"
	"go.uber.org/zap"
)

// ErrInvalidHTTPMethod happens when we receive anything other than a post request
var ErrInvalidHTTPMethod = errors.New("invalid HTTP Method")

// ErrFailedToParseRequest happens when we can't parse the request
var ErrFailedToParseRequest = errors.New("failed to parse request")

// Webhook provides an HTTP server along with any webhook events received
type Webhook struct {
	lights *lifx.Lifx
	addr   string
	log    *zap.Logger
	server *http.Server
}

// New creates a new webhook listener
func New(addr string, lifx *lifx.Lifx, log *zap.Logger) *Webhook {
	return &Webhook{
		lights: lifx,
		addr:   addr,
		log:    log,
		server: &http.Server{Addr: addr},
	}
}

// Listen will start the server to listen for webhooks
func (w *Webhook) Listen() {
	mux := http.NewServeMux()
	mux.HandleFunc("/plex", w.eventHandler)
	w.server.Handler = mux
	w.server.ListenAndServe()
}

// Close stop the webhook listner and any dependent services
func (w *Webhook) Close() error {
	w.lights.Close()
	return w.server.Close()
}

func (w *Webhook) eventHandler(respWriter http.ResponseWriter, r *http.Request) {
	// Assuming plex payload is no more than 16KiB in size
	err := r.ParseMultipartForm(16 * 1024)
	if err != nil {
		w.log.With(zap.Error(err)).Error("Failed to parse incoming multi part form in the request. Discarding.")
		return
	}

	result, err := w.parse(r)
	if err != nil {
		w.log.With(zap.Error(err)).Error("Failed to parse incoming webhook request. Discarding.")
		return
	}

	// Only the server owner is supported for now
	// TODO: support for other users and client names
	if !result.Owner {
		return
	}

	switch result.Event {
	case plexwebhooks.EventTypePlay, plexwebhooks.EventTypeStop, plexwebhooks.EventTypeScrobble, plexwebhooks.EventTypePause, plexwebhooks.EventTypeResume:
		// Process actions on parsed events
		go w.doAction(context.Background(), result.Event)
	}
}

// parse verifies and parses the events specified and returns the payload object or an error
func (w *Webhook) parse(r *http.Request) (*plexwebhooks.Payload, error) {
	defer func() {
		_, _ = io.Copy(ioutil.Discard, r.Body)
		_ = r.Body.Close()
	}()

	// Disregard everything that is not a POST request
	if r.Method != http.MethodPost {
		return nil, ErrInvalidHTTPMethod
	}

	// Try and marshal the request
	var payload plexwebhooks.Payload
	if err := payload.UnmarshalJSON([]byte(r.MultipartForm.Value["payload"][0])); err != nil {
		return nil, fmt.Errorf("%s: %s", ErrFailedToParseRequest, err)
	}

	return &payload, nil
}

// doAction will carry out an appropirate action with the lights based on the event
func (w *Webhook) doAction(ctx context.Context, event plexwebhooks.EventType) {
	switch event {
	case plexwebhooks.EventTypePlay, plexwebhooks.EventTypeResume:
		w.log.Sugar().Infof("Received Plex event: %s, powering off lights.", event)
		w.lights.Power(ctx, false)
	case plexwebhooks.EventTypePause, plexwebhooks.EventTypeStop:
		w.log.Sugar().Infof("Received Plex event: %s, powering on lights", event)
		w.lights.Power(ctx, true)
	}
}
