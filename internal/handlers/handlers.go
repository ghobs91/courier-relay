package handlers

import (
	"database/sql"
	"encoding/json"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip05"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/piraces/rsslay/pkg/feed"
	"github.com/piraces/rsslay/web/assets"
	"github.com/piraces/rsslay/web/templates"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var t = template.Must(template.ParseFS(templates.Templates, "*.tmpl"))

type Entry struct {
	PubKey       string
	NPubKey      string
	Url          string
	Error        bool
	ErrorMessage string
	ErrorCode    int
}

type PageData struct {
	Count         uint64
	FilteredCount uint64
	Entries       []Entry
}

func HandleWebpage(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	mustRedirect := handleOtherRegion(w, r)
	if mustRedirect {
		return
	}

	var count uint64
	row := db.QueryRow(`SELECT count(*) FROM feeds`)
	err := row.Scan(&count)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var items []Entry
	rows, err := db.Query(`SELECT publickey, url FROM feeds LIMIT 50`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.PubKey, &entry.Url); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		entry.NPubKey, _ = nip19.EncodePublicKey(entry.PubKey)
		items = append(items, entry)
	}
	if err := rows.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := PageData{
		Count:   count,
		Entries: items,
	}

	_ = t.ExecuteTemplate(w, "index.html.tmpl", data)
}

func HandleSearch(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	mustRedirect := handleOtherRegion(w, r)
	if mustRedirect {
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" || len(query) <= 4 {
		http.Error(w, "Please enter more than 5 characters to search", 400)
		return
	}

	var count uint64
	row := db.QueryRow(`SELECT count(*) FROM feeds`)
	err := row.Scan(&count)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var items []Entry
	rows, err := db.Query(`SELECT publickey, url FROM feeds WHERE url like '%' || $1 || '%' LIMIT 50`, query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.PubKey, &entry.Url); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		entry.NPubKey, _ = nip19.EncodePublicKey(entry.PubKey)
		items = append(items, entry)
	}
	if err := rows.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := PageData{
		Count:         count,
		FilteredCount: uint64(len(items)),
		Entries:       items,
	}

	_ = t.ExecuteTemplate(w, "search.html.tmpl", data)
}

func HandleCreateFeed(w http.ResponseWriter, r *http.Request, db *sql.DB, secret *string, dsn *string) {
	mustRedirect := handleRedirectToPrimaryNode(w, dsn)
	if mustRedirect {
		return
	}

	entry := createFeedEntry(r, db, secret)
	_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
}

func HandleFavicon(w http.ResponseWriter, r *http.Request) {
	mustRedirect := handleOtherRegion(w, r)
	if mustRedirect {
		return
	}

	w.Header().Set("Content-Type", "image/x-icon")
	_, _ = w.Write(assets.Favicon)
}

func HandleApiFeed(w http.ResponseWriter, r *http.Request, db *sql.DB, secret *string, dsn *string) {
	if r.Method == http.MethodGet || r.Method == http.MethodPost {
		handleCreateFeedEntry(w, r, db, secret, dsn)
	} else {
		http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
	}
}

func HandleNip05(w http.ResponseWriter, r *http.Request, db *sql.DB, ownerPubKey *string, enableAutoRegistration *bool) {
	name := r.URL.Query().Get("name")
	name, _ = url.QueryUnescape(name)
	w.Header().Set("Content-Type", "application/json")
	nip05WellKnownResponse := nip05.WellKnownResponse{
		Names: map[string]string{
			"_": *ownerPubKey,
		},
		Relays: nil,
	}

	var response []byte
	if name != "" && name != "_" && *enableAutoRegistration {
		row := db.QueryRow("SELECT publickey FROM feeds WHERE url like '%' || $1 || '%'", name)

		var entity feed.Entity
		err := row.Scan(&entity.PublicKey)
		if err == nil {
			nip05WellKnownResponse = nip05.WellKnownResponse{
				Names: map[string]string{
					name: entity.PublicKey,
				},
				Relays: nil,
			}
		}
	}

	response, _ = json.Marshal(nip05WellKnownResponse)
	_, _ = w.Write(response)
}

func handleCreateFeedEntry(w http.ResponseWriter, r *http.Request, db *sql.DB, secret *string, dsn *string) {
	mustRedirect := handleRedirectToPrimaryNode(w, dsn)
	if mustRedirect {
		return
	}

	entry := createFeedEntry(r, db, secret)
	w.Header().Set("Content-Type", "application/json")

	if entry.ErrorCode >= 400 {
		w.WriteHeader(entry.ErrorCode)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	response, _ := json.Marshal(entry)
	_, _ = w.Write(response)
}

func handleOtherRegion(w http.ResponseWriter, r *http.Request) bool {
	// If a different region is specified, redirect to that region.
	if region := r.URL.Query().Get("region"); region != "" && region != os.Getenv("FLY_REGION") {
		log.Printf("redirecting from %q to %q", os.Getenv("FLY_REGION"), region)
		w.Header().Set("fly-replay", "region="+region)
		return true
	}
	return false
}

func handleRedirectToPrimaryNode(w http.ResponseWriter, dsn *string) bool {
	// If this node is not primary, look up and redirect to the current primary.
	primaryFilename := filepath.Join(filepath.Dir(*dsn), ".primary")
	primary, err := os.ReadFile(primaryFilename)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	if string(primary) != "" {
		log.Printf("redirecting to primary instance: %q", string(primary))
		w.Header().Set("fly-replay", "instance="+string(primary))
		return true
	}

	return false
}

func createFeedEntry(r *http.Request, db *sql.DB, secret *string) *Entry {
	urlParam := r.URL.Query().Get("url")
	entry := Entry{
		Error: false,
	}
	feedUrl := feed.GetFeedURL(urlParam)
	if feedUrl == "" {
		entry.ErrorCode = http.StatusBadRequest
		entry.Error = true
		entry.ErrorMessage = "Could not find a feed URL in there..."
		return &entry
	}

	if _, err := feed.ParseFeed(feedUrl); err != nil {
		entry.ErrorCode = http.StatusBadRequest
		entry.Error = true
		entry.ErrorMessage = "Bad feed: " + err.Error()
		return &entry
	}

	sk := feed.PrivateKeyFromFeed(feedUrl, *secret)
	publicKey, err := nostr.GetPublicKey(sk)
	if err != nil {
		entry.ErrorCode = http.StatusInternalServerError
		entry.Error = true
		entry.ErrorMessage = "bad private key: " + err.Error()
		return &entry
	}

	publicKey = strings.TrimSpace(publicKey)
	defer insertFeed(err, feedUrl, publicKey, sk, db)

	entry.Url = feedUrl
	entry.PubKey = publicKey
	entry.NPubKey, _ = nip19.EncodePublicKey(publicKey)
	return &entry
}

func insertFeed(err error, feedUrl string, publicKey string, sk string, db *sql.DB) {
	row := db.QueryRow("SELECT privatekey, url FROM feeds WHERE publickey=$1", publicKey)

	var entity feed.Entity
	err = row.Scan(&entity.PrivateKey, &entity.URL)
	if err != nil && err == sql.ErrNoRows {
		log.Printf("not found feed at url %q as publicKey %s", feedUrl, publicKey)
		if _, err := db.Exec(`INSERT INTO feeds (publickey, privatekey, url) VALUES (?, ?, ?)`, publicKey, sk, feedUrl); err != nil {
			log.Printf("failure: " + err.Error())
		} else {
			log.Printf("saved feed at url %q as publicKey %s", feedUrl, publicKey)
		}
	} else if err != nil {
		log.Fatalf("failed when trying to retrieve row with pubkey '%s': %v", publicKey, err)
	} else {
		log.Printf("found feed at url %q as publicKey %s", feedUrl, publicKey)
	}
}
