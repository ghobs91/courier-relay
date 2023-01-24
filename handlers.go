package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*
var resources embed.FS

//go:embed assets/favicon.ico
var favicon []byte

var t = template.Must(template.ParseFS(resources, "templates/*"))

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

func handleWebpage(w http.ResponseWriter, r *http.Request) {
	mustRedirect := handleOtherRegion(w, r)
	if mustRedirect {
		return
	}

	var count uint64
	row := relay.db.QueryRow(`SELECT count(*) FROM feeds`)
	err := row.Scan(&count)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var items []Entry
	rows, err := relay.db.Query(`SELECT publickey, url FROM feeds LIMIT 50`)
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

func handleSearch(w http.ResponseWriter, r *http.Request) {
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
	row := relay.db.QueryRow(`SELECT count(*) FROM feeds`)
	err := row.Scan(&count)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var items []Entry
	rows, err := relay.db.Query(`SELECT publickey, url FROM feeds WHERE url like '%' || $1 || '%' LIMIT 50`, query)
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

func handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	mustRedirect := handleRedirectToPrimaryNode(w)
	if mustRedirect {
		return
	}

	entry := createFeedEntry(r)
	_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	mustRedirect := handleOtherRegion(w, r)
	if mustRedirect {
		return
	}

	w.Header().Set("Content-Type", "image/x-icon")
	_, _ = w.Write(favicon)
}

func handleApiFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodPost {
		handleCreateFeedEntry(w, r)
	} else {
		http.Error(w, "Method not supported", http.StatusMethodNotAllowed)
	}
}

func handleCreateFeedEntry(w http.ResponseWriter, r *http.Request) {
	mustRedirect := handleRedirectToPrimaryNode(w)
	if mustRedirect {
		return
	}

	entry := createFeedEntry(r)
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

func handleRedirectToPrimaryNode(w http.ResponseWriter) bool {
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

func createFeedEntry(r *http.Request) *Entry {
	url := r.URL.Query().Get("url")
	entry := Entry{
		Error: false,
	}
	feedUrl := getFeedURL(url)
	if feedUrl == "" {
		entry.ErrorCode = http.StatusBadRequest
		entry.Error = true
		entry.ErrorMessage = "Could not find a feed URL in there..."
		return &entry
	}

	if _, err := parseFeed(feedUrl); err != nil {
		entry.ErrorCode = http.StatusBadRequest
		entry.Error = true
		entry.ErrorMessage = "Bad feed: " + err.Error()
		return &entry
	}

	sk := privateKeyFromFeed(feedUrl)
	publicKey, err := nostr.GetPublicKey(sk)
	if err != nil {
		entry.ErrorCode = http.StatusInternalServerError
		entry.Error = true
		entry.ErrorMessage = "bad private key: " + err.Error()
		return &entry
	}

	publicKey = strings.TrimSpace(publicKey)
	defer insertFeed(err, feedUrl, publicKey, sk)

	entry.Url = feedUrl
	entry.PubKey = publicKey
	entry.NPubKey, _ = nip19.EncodePublicKey(publicKey)
	return &entry
}

func insertFeed(err error, feedUrl string, publicKey string, sk string) {
	row := relay.db.QueryRow("SELECT privatekey, url FROM feeds WHERE publickey=$1", publicKey)

	var entity Entity
	err = row.Scan(&entity.PrivateKey, &entity.URL)
	if err != nil && err == sql.ErrNoRows {
		log.Printf("not found feed at url %q as publicKey %s", feedUrl, publicKey)
		if _, err := relay.db.Exec(`INSERT INTO feeds (publickey, privatekey, url) VALUES (?, ?, ?)`, publicKey, sk, feedUrl); err != nil {
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
