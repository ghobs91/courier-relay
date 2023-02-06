package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"github.com/fiatjaf/relayer"
	_ "github.com/fiatjaf/relayer"
	"github.com/hellofresh/health-go/v5"
	"github.com/kelseyhightower/envconfig"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
	"github.com/piraces/rsslay/internal/handlers"
	"github.com/piraces/rsslay/pkg/feed"
	"github.com/piraces/rsslay/pkg/replayer"
	"github.com/piraces/rsslay/scripts"
	"golang.org/x/exp/slices"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// Command line flags.
var (
	dsn = flag.String("dsn", "", "datasource name")
)

type Relay struct {
	Secret                          string   `envconfig:"SECRET" required:"true"`
	DatabaseDirectory               string   `envconfig:"DB_DIR" default:"db/rsslay.sqlite"`
	DefaultProfilePictureUrl        string   `envconfig:"DEFAULT_PROFILE_PICTURE_URL" default:"https://i.imgur.com/MaceU96.png"`
	Version                         string   `envconfig:"VERSION" default:"unknown"`
	ReplayToRelays                  bool     `envconfig:"REPLAY_TO_RELAYS" default:"false"`
	RelaysToPublish                 []string `envconfig:"RELAYS_TO_PUBLISH_TO" default:""`
	DefaultWaitTimeBetweenBatches   int64    `envconfig:"DEFAULT_WAIT_TIME_BETWEEN_BATCHES" default:"60000"`
	DefaultWaitTimeForRelayResponse int64    `envconfig:"DEFAULT_WAIT_TIME_FOR_RELAY_RESPONSE" default:"3000"`
	MaxEventsToReplay               int      `envconfig:"MAX_EVENTS_TO_REPLAY" default:"20"`
	EnableAutoNIP05Registration     bool     `envconfig:"ENABLE_AUTO_NIP05_REGISTRATION" default:"false"`
	MainDomainName                  string   `envconfig:"MAIN_DOMAIN_NAME" default:""`
	OwnerPublicKey                  string   `envconfig:"OWNER_PUBLIC_KEY" default:""`
	MaxSubroutines                  int      `envconfig:"MAX_SUBROUTINES" default:"20"`

	updates            chan nostr.Event
	lastEmitted        sync.Map
	db                 *sql.DB
	healthCheck        *health.Health
	mutex              sync.Mutex
	routineQueueLength int
}

var relayInstance = &Relay{
	updates: make(chan nostr.Event),
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
	relayInstance.healthCheck = h
}

func (r *Relay) Name() string {
	return "rsslay"
}

func (r *Relay) OnInitialized(s *relayer.Server) {
	s.Router().Path("/").HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleWebpage(writer, request, r.db)
	})
	s.Router().Path("/create").HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleCreateFeed(writer, request, r.db, &r.Secret, dsn)
	})
	s.Router().Path("/search").HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleSearch(writer, request, r.db)
	})
	s.Router().Path("/favicon.ico").HandlerFunc(handlers.HandleFavicon)
	s.Router().Path("/healthz").HandlerFunc(relayInstance.healthCheck.HandlerFunc)
	s.Router().Path("/api/feed").HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleApiFeed(writer, request, r.db, &r.Secret, dsn)
	})
	s.Router().Path("/.well-known/nostr.json").HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlers.HandleNip05(writer, request, r.db, &r.OwnerPublicKey, &r.EnableAutoNIP05Registration)
	})
}

func (r *Relay) Init() error {
	flag.Parse()
	err := envconfig.Process("", r)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	} else {
		log.Printf("Running VERSION %s:\n - DSN=%s\n - DB_DIR=%s\n\n", r.Version, *dsn, r.DatabaseDirectory)
	}

	r.db = InitDatabase(r)

	go func() {
		time.Sleep(20 * time.Minute)

		filters := relayer.GetListeningFilters()
		log.Printf("checking for updates; %d filters active", len(filters))

		var events []replayer.EventWithPrivateKey
		for _, filter := range filters {
			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				for _, pubkey := range filter.Authors {
					pubkey = strings.TrimSpace(pubkey)
					row := r.db.QueryRow("SELECT privatekey, url FROM feeds WHERE publickey=$1", pubkey)

					var entity feed.Entity
					err := row.Scan(&entity.PrivateKey, &entity.URL)
					if err != nil && err == sql.ErrNoRows {
						continue
					} else if err != nil {
						log.Fatalf("failed when trying to retrieve row with pubkey '%s': %v", pubkey, err)
					}

					parsedFeed, err := feed.ParseFeed(entity.URL)
					if err != nil {
						log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
						feed.DeleteInvalidFeed(entity.URL, r.db)
						continue
					}

					for _, item := range parsedFeed.Items {
						defaultCreatedAt := time.Now()
						evt := feed.ItemToTextNote(pubkey, item, parsedFeed, defaultCreatedAt, entity.URL)
						last, ok := r.lastEmitted.Load(entity.URL)
						if !ok || time.Unix(int64(last.(uint32)), 0).Before(evt.CreatedAt) {
							_ = evt.Sign(entity.PrivateKey)
							r.updates <- evt
							r.lastEmitted.Store(entity.URL, last.(uint32))
							events = append(events, replayer.EventWithPrivateKey{Event: evt, PrivateKey: entity.PrivateKey})
						}
					}
				}
			}
		}
		if relayInstance.ReplayToRelays && relayInstance.routineQueueLength < relayInstance.MaxSubroutines && len(events) > 0 {
			r.routineQueueLength++
			replayer.ReplayEventsToRelays(&replayer.ReplayParameters{
				MaxEventsToReplay:        relayInstance.MaxEventsToReplay,
				RelaysToPublish:          relayInstance.RelaysToPublish,
				Mutex:                    &relayInstance.mutex,
				Queue:                    &relayInstance.routineQueueLength,
				WaitTime:                 relayInstance.DefaultWaitTimeBetweenBatches,
				WaitTimeForRelayResponse: relayInstance.DefaultWaitTimeForRelayResponse,
				Events:                   events,
			})
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
	db *sql.DB
}

func (b store) Init() error { return nil }
func (b store) SaveEvent(_ *nostr.Event) error {
	return errors.New("blocked: we don't accept any events")
}

func (b store) DeleteEvent(_, _ string) error {
	return errors.New("blocked: we can't delete any events")
}

func (b store) QueryEvents(filter *nostr.Filter) ([]nostr.Event, error) {
	var events []nostr.Event
	var eventsToReplay []replayer.EventWithPrivateKey

	if filter.IDs != nil || len(filter.Tags) > 0 {
		return events, nil
	}

	for _, pubkey := range filter.Authors {
		pubkey = strings.TrimSpace(pubkey)
		row := relayInstance.db.QueryRow("SELECT privatekey, url FROM feeds WHERE publickey=$1", pubkey)

		var entity feed.Entity
		err := row.Scan(&entity.PrivateKey, &entity.URL)
		if err != nil && err == sql.ErrNoRows {
			continue
		} else if err != nil {
			log.Fatalf("failed when trying to retrieve row with pubkey '%s': %v", pubkey, err)
		}

		parsedFeed, err := feed.ParseFeed(entity.URL)
		if err != nil {
			log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
			feed.DeleteInvalidFeed(entity.URL, relayInstance.db)
			continue
		}

		if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindSetMetadata) {
			evt := feed.FeedToSetMetadata(pubkey, parsedFeed, entity.URL, relayInstance.EnableAutoNIP05Registration, relayInstance.DefaultProfilePictureUrl)

			if filter.Since != nil && evt.CreatedAt.Before(*filter.Since) {
				continue
			}
			if filter.Until != nil && evt.CreatedAt.After(*filter.Until) {
				continue
			}

			_ = evt.Sign(entity.PrivateKey)
			events = append(events, evt)
			eventsToReplay = append(eventsToReplay, replayer.EventWithPrivateKey{Event: evt, PrivateKey: entity.PrivateKey})
		}

		if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
			var last uint32 = 0
			for _, item := range parsedFeed.Items {
				defaultCreatedAt := time.Now()
				evt := feed.ItemToTextNote(pubkey, item, parsedFeed, defaultCreatedAt, entity.URL)

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

				events = append(events, evt)
				eventsToReplay = append(eventsToReplay, replayer.EventWithPrivateKey{Event: evt, PrivateKey: entity.PrivateKey})
			}

			relayInstance.lastEmitted.Store(entity.URL, last)
		}
	}

	if relayInstance.ReplayToRelays && relayInstance.routineQueueLength < relayInstance.MaxSubroutines && len(eventsToReplay) > 0 {
		relayInstance.routineQueueLength++
		replayer.ReplayEventsToRelays(&replayer.ReplayParameters{
			MaxEventsToReplay:        relayInstance.MaxEventsToReplay,
			RelaysToPublish:          relayInstance.RelaysToPublish,
			Mutex:                    &relayInstance.mutex,
			Queue:                    &relayInstance.routineQueueLength,
			WaitTime:                 relayInstance.DefaultWaitTimeBetweenBatches,
			WaitTimeForRelayResponse: relayInstance.DefaultWaitTimeForRelayResponse,
			Events:                   eventsToReplay,
		})
	}

	return events, nil
}

func (r *Relay) InjectEvents() chan nostr.Event {
	return r.updates
}

func main() {
	CreateHealthCheck()
	defer relayInstance.db.Close()
	if err := relayer.Start(relayInstance); err != nil {
		log.Fatalf("server terminated: %v", err)
	}
}

func InitDatabase(r *Relay) *sql.DB {
	finalConnection := dsn
	if *dsn == "" {
		log.Print("dsn required is not present... defaulting to DB_DIR")
		finalConnection = &r.DatabaseDirectory
	}

	// Create empty dir if not exists
	dbPath := path.Dir(*finalConnection)
	err := os.MkdirAll(dbPath, 0660)
	if err != nil {
		log.Printf("unable to initialize DB_DIR at: %s. Error: %v", dbPath, err)
	}

	// Connect to SQLite database.
	sqlDb, err := sql.Open("sqlite3", *finalConnection)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	log.Printf("database opened at %s", *finalConnection)

	// Run migration.
	if _, err := sqlDb.Exec(scripts.SchemaSQL); err != nil {
		log.Fatalf("cannot migrate schema: %v", err)
	}

	return sqlDb
}
