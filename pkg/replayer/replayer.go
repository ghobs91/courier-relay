package replayer

import (
	"context"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip42"
	"log"
	"sort"
	"sync"
	"time"
)

type ReplayParameters struct {
	MaxEventsToReplay        int
	RelaysToPublish          []string
	Mutex                    *sync.Mutex
	Queue                    *int
	WaitTime                 int64
	WaitTimeForRelayResponse int64
	Events                   []EventWithPrivateKey
}

type EventWithPrivateKey struct {
	Event      nostr.Event
	PrivateKey string
}

func ReplayEventsToRelays(parameters *ReplayParameters) {
	eventCount := len(parameters.Events)
	if eventCount == 0 {
		return
	}

	if eventCount > parameters.MaxEventsToReplay {
		sort.Slice(parameters.Events, func(i, j int) bool {
			return parameters.Events[i].Event.CreatedAt.After(parameters.Events[j].Event.CreatedAt)
		})
		parameters.Events = parameters.Events[:parameters.MaxEventsToReplay]
	}

	go func() {
		parameters.Mutex.Lock()
		// publish the event to predefined relays
		for _, url := range parameters.RelaysToPublish {
			relay, e := nostr.RelayConnect(context.Background(), url)
			if e != nil {
				log.Println(e)
				continue
			}

			challenge, shouldPerformAuthRequest := needsAuth(relay, parameters.WaitTimeForRelayResponse)

			statusSummary := 0
			for _, ev := range parameters.Events {
				if shouldPerformAuthRequest && !tryAuth(relay, *challenge, url, parameters.WaitTimeForRelayResponse, &ev) {
					continue
				}
				publishStatus := relay.Publish(context.Background(), ev.Event)
				statusSummary = statusSummary | int(publishStatus)
			}
			log.Printf("Replayed %d events to %s with status summary %d\n", len(parameters.Events), url, statusSummary)
			_ = relay.Close()
		}
		time.Sleep(time.Duration(parameters.WaitTime) * time.Millisecond)
		*parameters.Queue--
		parameters.Mutex.Unlock()
	}()
}

func needsAuth(relay *nostr.Relay, waitTime int64) (*string, bool) {
	afterCh := time.After(time.Duration(waitTime) * time.Millisecond)
	for {
		select {
		case d := <-relay.Challenges:
			log.Println("Got challenge:", d)
			return &d, true
		case <-afterCh:
			log.Println("No challenge received... Skipping auth")
			return nil, false
		}
	}
}

func tryAuth(relay *nostr.Relay, challenge string, url string, waitTime int64, ev *EventWithPrivateKey) bool {
	event := nip42.CreateUnsignedAuthEvent(challenge, ev.Event.PubKey, url)
	err := event.Sign(ev.PrivateKey)
	if err != nil {
		log.Printf("Failed to sign event while trying to authenticate. PubKey: %s\n", ev.Event.PubKey)
		return false
	}

	// Set-up context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(waitTime)*time.Millisecond)
	defer cancel()

	// Send the event by calling relay.Auth.
	// Returned status is either success, fail, or sent (if no reply given in the 3-second timeout).
	authStatus := relay.Auth(ctx, event)

	log.Printf("authenticated as %s: %s\n", ev.Event.PubKey, authStatus)
	if authStatus == nostr.PublishStatusSucceeded || authStatus == nostr.PublishStatusSent {
		return true
	}
	return false
}
