package main

import (
	"embed"
	"encoding/json"
	"github.com/nbd-wtf/go-nostr"
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
	Url          string
	Error        bool
	ErrorMessage string
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
		if err := json.Unmarshal(iter.Value(), &entity); err != nil {
			continue
		}
		count += 1
		items = append(items, Entry{
			PubKey: string(iter.Key()),
			Url:    entity.URL,
		})
	}

	data := PageData{
		Count:   count,
		Entries: items,
	}

	_ = t.ExecuteTemplate(w, "index.html.tmpl", data)
}

func handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")

	entry := Entry{
		Error: false,
	}
	feedUrl := getFeedURL(url)
	if feedUrl == "" {
		w.WriteHeader(400)
		entry.Error = true
		entry.ErrorMessage = "Could not find a feed URL in there..."
		_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
		return
	}

	if _, err := parseFeed(feedUrl); err != nil {
		w.WriteHeader(400)
		entry.Error = true
		entry.ErrorMessage = "Bad feed: " + err.Error()
		_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
		return
	}

	sk := privateKeyFromFeed(feedUrl)
	publicKey, err := nostr.GetPublicKey(sk)
	if err != nil {
		w.WriteHeader(500)
		entry.Error = true
		entry.ErrorMessage = "bad private key: " + err.Error()
		_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
		return
	}

	j, _ := json.Marshal(Entity{
		PrivateKey: sk,
		URL:        feedUrl,
	})

	if err := relay.db.Set([]byte(publicKey), j, nil); err != nil {
		w.WriteHeader(500)
		entry.Error = true
		entry.ErrorMessage = "failure: " + err.Error()
		_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
		return
	}

	log.Printf("saved feed at url %q as publicKey %s", feedUrl, publicKey)

	entry.Url = feedUrl
	entry.PubKey = publicKey
	_ = t.ExecuteTemplate(w, "created.html.tmpl", entry)
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "image/x-icon")
	_, _ = w.Write(favicon)
}
