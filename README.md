# Plex Webhooks + Lifx

Webhook listener to control Lifx lights based on Plex media playback events.

This is a simple webhook listener that can be configured against a Plex server. When setup, Plex will publish webhooks on certain events which is used to trigger changes to state of the Lifx bulbs. Right now it will periodically discover devices on the network using the Lifx LAN v2 protocol. Every time a media is Started on plex it will turn off all the lights. If media is paused or stopped, the lights will automatically turn on.

There are a couple of things I am working to expand on. First off is a simple web UI to see of all the available lights and select the ones to be triggered during plex events.
