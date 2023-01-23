package main

import (
	"embed"
	"encoding/json"
	"github.com/cockroachdb/pebble"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"html/template"
	"log"
	"net/http"
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
	Count   uint64
	Entries []Entry
}

func handleWebpage(w http.ResponseWriter, _ *http.Request) {
	iter := relay.db.NewIter(nil)
	var count uint64 = 0
	var items []Entry
	for iter.First(); iter.Valid(); iter.Next() {
		var entity Entity
		err := iter.Error()
		point, hasRange := iter.HasPointAndRange()
		if err != nil && point && hasRange {
			continue
		}

		entry, err := iter.ValueAndErr()
		if err != nil {
			continue
		}

		if err := json.Unmarshal(entry, &entity); err != nil {
			continue
		}
		count += 1
		pubKey := string(iter.Key())
		nPubKey, _ := nip19.EncodePublicKey(pubKey)
		items = append(items, Entry{
			PubKey:  pubKey,
			NPubKey: nPubKey,
			Url:     entity.URL,
		})
	}

	data := PageData{
		Count:   count,
		Entries: items,
	}

	_ = t.ExecuteTemplate(w, "index.html.tmpl", data)
}

func handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	urlParam := r.URL.Query().Get("url")
	entry := createFeedEntry(urlParam)
	_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
}

func handleFavicon(w http.ResponseWriter, _ *http.Request) {
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
	urlParam := r.URL.Query().Get("url")
	entry := createFeedEntry(urlParam)
	w.Header().Set("Content-Type", "application/json")

	if entry.ErrorCode >= 400 {
		w.WriteHeader(entry.ErrorCode)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	response, _ := json.Marshal(entry)
	_, _ = w.Write(response)
}

func createFeedEntry(url string) *Entry {
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

	j, _ := json.Marshal(Entity{
		PrivateKey: sk,
		URL:        feedUrl,
	})

	foundEntry, _, err := relay.db.Get([]byte(publicKey))
	if err == pebble.ErrNotFound {
		log.Printf("not found feed at url %q as publicKey %s", feedUrl, publicKey)
		if err := relay.db.Set([]byte(publicKey), j, nil); err != nil {
			entry.ErrorCode = http.StatusInternalServerError
			entry.Error = true
			entry.ErrorMessage = "failure: " + err.Error()
			return &entry
		}
		log.Printf("saved feed at url %q as publicKey %s", feedUrl, publicKey)
	} else if len(foundEntry) > 0 {
		log.Printf("found feed at url %q as publicKey %s", feedUrl, publicKey)
	}

	entry.Url = feedUrl
	entry.PubKey = publicKey
	entry.NPubKey, _ = nip19.EncodePublicKey(publicKey)
	return &entry
}
