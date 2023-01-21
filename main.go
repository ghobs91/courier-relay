package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cockroachdb/pebble"
	"github.com/fiatjaf/relayer"
	"github.com/hellofresh/health-go/v5"
	"github.com/kelseyhightower/envconfig"
	"github.com/nbd-wtf/go-nostr"
	"golang.org/x/exp/slices"
	"log"
	"os"
	"sync"
	"time"
)

var relay = &Relay{
	updates: make(chan nostr.Event),
}

type Relay struct {
	Secret            string `envconfig:"SECRET" required:"true"`
	DatabaseDirectory string `envconfig:"DB_DIR" default:"db"`
	Version           string `envconfig:"VERSION" default:"unknown"`

	updates     chan nostr.Event
	lastEmitted sync.Map
	db          *pebble.DB
	healthCheck *health.Health
}

func CreateHealthCheck() {
	h, _ := health.New(health.WithComponent(health.Component{
		Name:    "rsslay",
		Version: os.Getenv("VERSION"),
	}), health.WithChecks(health.Config{
		Name:      "self",
		Timeout:   time.Second * 5,
		SkipOnErr: false,
		Check: func(ctx context.Context) error {
			return nil
		},
	},
	))
	relay.healthCheck = h
}

func (r *Relay) Name() string {
	return "rsslay"
}

func (r *Relay) OnInitialized(s *relayer.Server) {
	s.Router().Path("/").HandlerFunc(handleWebpage)
	s.Router().Path("/create").HandlerFunc(handleCreateFeed)
	s.Router().Path("/favicon.ico").HandlerFunc(handleFavicon)
	s.Router().Path("/healthz").HandlerFunc(r.healthCheck.HandlerFunc)
	s.Router().Path("/api/feed").HandlerFunc(handleApiFeed)
}

func (r *Relay) Init() error {
	err := envconfig.Process("", r)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	}

	if db, err := pebble.Open(r.DatabaseDirectory, nil); err != nil {
		log.Fatalf("failed to open db: %v", err)
	} else {
		r.db = db
	}

	go func() {
		time.Sleep(20 * time.Minute)

		filters := relayer.GetListeningFilters()
		log.Printf("checking for updates; %d filters active", len(filters))

		for _, filter := range filters {
			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				for _, pubkey := range filter.Authors {
					if val, closer, err := r.db.Get([]byte(pubkey)); err == nil {
						defer closer.Close()

						var entity Entity
						if err := json.Unmarshal(val, &entity); err != nil {
							log.Printf("got invalid json from db at key %s: %v", pubkey, err)
							continue
						}

						feed, err := parseFeed(entity.URL)
						if err != nil {
							log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
							continue
						}

						for _, item := range feed.Items {
							defaultCreatedAt := time.Now()
							evt := itemToTextNote(pubkey, item, feed, defaultCreatedAt)
							last, ok := r.lastEmitted.Load(entity.URL)
							if !ok || time.Unix(last.(int64), 0).Before(evt.CreatedAt) {
								_ = evt.Sign(entity.PrivateKey)
								r.updates <- evt
								r.lastEmitted.Store(entity.URL, last)
							}
						}
					}
				}
			}
		}
	}()

	return nil
}

func (r *Relay) AcceptEvent(_ *nostr.Event) bool {
	return false
}

func (r *Relay) Storage() relayer.Storage {
	return store{r.db}
}

type store struct {
	db *pebble.DB
}

func (b store) Init() error { return nil }
func (b store) SaveEvent(_ *nostr.Event) error {
	return errors.New("blocked: we don't accept any events")
}

func (b store) DeleteEvent(_, _ string) error {
	return errors.New("blocked: we can't delete any events")
}

func (b store) QueryEvents(filter *nostr.Filter) ([]nostr.Event, error) {
	var evts []nostr.Event

	if filter.IDs != nil || len(filter.Tags) > 0 {
		return evts, nil
	}

	for _, pubkey := range filter.Authors {
		if val, closer, err := relay.db.Get([]byte(pubkey)); err == nil {
			defer closer.Close()

			var entity Entity
			if err := json.Unmarshal(val, &entity); err != nil {
				log.Printf("got invalid json from db at key %s: %v", pubkey, err)
				continue
			}

			feed, err := parseFeed(entity.URL)
			if err != nil {
				log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
				continue
			}

			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindSetMetadata) {
				evt := feedToSetMetadata(pubkey, feed)

				if filter.Since != nil && evt.CreatedAt.Before(*filter.Since) {
					continue
				}
				if filter.Until != nil && evt.CreatedAt.After(*filter.Until) {
					continue
				}

				_ = evt.Sign(entity.PrivateKey)
				evts = append(evts, evt)
			}

			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				var last uint32 = 0
				for _, item := range feed.Items {
					defaultCreatedAt := time.Now()
					evt := itemToTextNote(pubkey, item, feed, defaultCreatedAt)

					// Feed need to have a date for each entry...
					if evt.CreatedAt.Equal(defaultCreatedAt) {
						continue
					}

					if filter.Since != nil && evt.CreatedAt.Before(*filter.Since) {
						continue
					}
					if filter.Until != nil && evt.CreatedAt.After(*filter.Until) {
						continue
					}

					_ = evt.Sign(entity.PrivateKey)

					if evt.CreatedAt.After(time.Unix(int64(last), 0)) {
						last = uint32(evt.CreatedAt.Unix())
					}

					evts = append(evts, evt)
				}

				relay.lastEmitted.Store(entity.URL, last)
			}
		}
	}

	return evts, nil
}

func (r *Relay) InjectEvents() chan nostr.Event {
	return r.updates
}

func main() {
	CreateHealthCheck()
	if err := relayer.Start(relay); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}
