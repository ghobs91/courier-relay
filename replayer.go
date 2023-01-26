package main

import (
	"context"
	"github.com/nbd-wtf/go-nostr"
	"log"
	"sort"
	"time"
)

func replayEventsToRelays(relay *Relay, events []nostr.Event) {
	eventCount := len(events)
	if eventCount == 0 {
		return
	}

	if eventCount > relay.MaxEventsToReplay {
		sort.Slice(events, func(i, j int) bool {
			return events[i].CreatedAt.After(events[j].CreatedAt)
		})
		events = events[:relay.MaxEventsToReplay]
	}

	go func() {
		relay.mu.Lock()
		// publish the event to predefined relays
		for _, url := range relay.RelaysToPublish {
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
		time.Sleep(time.Duration(relay.DefaultWaitTimeBetweenBatches) * time.Millisecond)
		relay.mu.Unlock()
		relay.routineQueueLength--
	}()
}
