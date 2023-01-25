package main

import (
	"context"
	"github.com/nbd-wtf/go-nostr"
	"log"
	"os"
	"strings"
)

func replayEventsToRelays(events []nostr.Event) {
	go func() {
		// publish the event to predefined relays
		eventCount := len(events)
		if eventCount == 0 {
			return
		}

		relaysEnv := os.Getenv("RELAYS_TO_PUBLISH_TO")
		relays := strings.Split(relaysEnv, ";")

		for _, url := range relays {
			relay, e := nostr.RelayConnect(context.Background(), url)
			if e != nil {
				log.Println(e)
				continue
			}
			statusSummary := 0
			for _, ev := range events {
				publishStatus := relay.Publish(context.Background(), ev)
				statusSummary = statusSummary | int(publishStatus)
			}
			log.Printf("Replayed %d events to %s with status summary %d\n", eventCount, url, statusSummary)
			_ = relay.Close()
		}
	}()
}
