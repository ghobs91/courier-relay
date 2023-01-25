package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"github.com/fiatjaf/relayer"
	"github.com/hellofresh/health-go/v5"
	"github.com/kelseyhightower/envconfig"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
	"golang.org/x/exp/slices"
	"log"
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

//go:embed schema.sql
var schemaSQL string

var relay = &Relay{
	updates: make(chan nostr.Event),
}

type Relay struct {
	Secret            string `envconfig:"SECRET" required:"true"`
	DatabaseDirectory string `envconfig:"DB_DIR" default:"db/rsslay.sqlite"`
	Version           string `envconfig:"VERSION" default:"unknown"`

	updates     chan nostr.Event
	lastEmitted sync.Map
	db          *sql.DB
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
	s.Router().Path("/search").HandlerFunc(handleSearch)
	s.Router().Path("/favicon.ico").HandlerFunc(handleFavicon)
	s.Router().Path("/healthz").HandlerFunc(r.healthCheck.HandlerFunc)
	s.Router().Path("/api/feed").HandlerFunc(handleApiFeed)
}

func (r *Relay) Init() error {
	flag.Parse()
	err := envconfig.Process("", r)
	if err != nil {
		return fmt.Errorf("couldn't process envconfig: %w", err)
	} else {
		fmt.Printf("Running VERSION %s:\n - DSN=%s\n - DB_DIR=%s\n\n", r.Version, *dsn, r.DatabaseDirectory)
	}

	r.db = InitDatabase(r)

	go func() {
		time.Sleep(20 * time.Minute)

		filters := relayer.GetListeningFilters()
		log.Printf("checking for updates; %d filters active", len(filters))

		for _, filter := range filters {
			if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindTextNote) {
				for _, pubkey := range filter.Authors {
					pubkey = strings.TrimSpace(pubkey)
					row := r.db.QueryRow("SELECT privatekey, url FROM feeds WHERE publickey=$1", pubkey)

					var entity Entity
					err := row.Scan(&entity.PrivateKey, &entity.URL)
					if err != nil && err == sql.ErrNoRows {
						continue
					} else if err != nil {
						log.Fatalf("failed when trying to retrieve row with pubkey '%s': %v", pubkey, err)
					}

					feed, err := parseFeed(entity.URL)
					if err != nil {
						log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
						continue
					}

					for _, item := range feed.Items {
						defaultCreatedAt := time.Now()
						evt := itemToTextNote(pubkey, item, feed, defaultCreatedAt, entity.URL)
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
	var evts []nostr.Event

	if filter.IDs != nil || len(filter.Tags) > 0 {
		return evts, nil
	}

	for _, pubkey := range filter.Authors {
		pubkey = strings.TrimSpace(pubkey)
		row := relay.db.QueryRow("SELECT privatekey, url FROM feeds WHERE publickey=$1", pubkey)

		var entity Entity
		err := row.Scan(&entity.PrivateKey, &entity.URL)
		if err != nil && err == sql.ErrNoRows {
			continue
		} else if err != nil {
			log.Fatalf("failed when trying to retrieve row with pubkey '%s': %v", pubkey, err)
		}

		feed, err := parseFeed(entity.URL)
		if err != nil {
			log.Printf("failed to parse feed at url %q: %v", entity.URL, err)
			continue
		}

		if filter.Kinds == nil || slices.Contains(filter.Kinds, nostr.KindSetMetadata) {
			evt := feedToSetMetadata(pubkey, feed, entity.URL)

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
				evt := itemToTextNote(pubkey, item, feed, defaultCreatedAt, entity.URL)

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

	return evts, nil
}

func (r *Relay) InjectEvents() chan nostr.Event {
	return r.updates
}

func main() {
	CreateHealthCheck()
	defer relay.db.Close()
	if err := relayer.Start(relay); err != nil {
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
	if _, err := sqlDb.Exec(schemaSQL); err != nil {
		log.Fatalf("cannot migrate schema: %v", err)
	}

	return sqlDb
}
