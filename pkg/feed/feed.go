package feed

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/piraces/rsslay/pkg/helpers"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	strip "github.com/grokify/html-strip-tags-go"
	"github.com/mmcdole/gofeed"
	"github.com/nbd-wtf/go-nostr"
	"github.com/rif/cache2go"
)

var (
	fp        = gofeed.NewParser()
	feedCache = cache2go.New(512, time.Minute*19)
	client    = &http.Client{
		Timeout: 5 * time.Second,
	}
)

type Entity struct {
	PublicKey  string
	PrivateKey string
	URL        string
}

var types = []string{
	"rss+xml",
	"atom+xml",
	"feed+json",
	"text/xml",
	"application/xml",
}

func GetFeedURL(url string) string {
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode >= 300 {
		return ""
	}

	ct := resp.Header.Get("Content-Type")
	for _, typ := range types {
		if strings.Contains(ct, typ) {
			return url
		}
	}

	if strings.Contains(ct, "text/html") {
		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return ""
		}

		for _, typ := range types {
			href, _ := doc.Find(fmt.Sprintf("link[type*='%s']", typ)).Attr("href")
			if href == "" {
				continue
			}
			if !strings.HasPrefix(href, "http") {
				href, _ = helpers.UrlJoin(url, href)
			}
			return href
		}
	}

	return ""
}

func ParseFeed(url string) (*gofeed.Feed, error) {
	if feed, ok := feedCache.Get(url); ok {
		return feed.(*gofeed.Feed), nil
	}

	feed, err := fp.ParseURL(url)
	if err != nil {
		return nil, err
	}

	// cleanup a little so we don't store too much junk
	for i := range feed.Items {
		feed.Items[i].Content = ""
	}
	feedCache.Set(url, feed)

	return feed, nil
}

func FeedToSetMetadata(pubkey string, feed *gofeed.Feed, originalUrl string, enableAutoRegistration bool, defaultProfilePictureUrl string) nostr.Event {
	// Handle Nitter special cases (http schema)
	if strings.Contains(feed.Description, "Twitter feed") {
		if strings.HasPrefix(originalUrl, "https://") {
			feed.Description = strings.ReplaceAll(feed.Description, "http://", "https://")
			feed.Title = strings.ReplaceAll(feed.Title, "http://", "https://")
			feed.Image.URL = strings.ReplaceAll(feed.Image.URL, "http://", "https://")
			feed.Link = strings.ReplaceAll(feed.Link, "http://", "https://")
		}
	}

	metadata := map[string]string{
		"name":  feed.Title,
		"about": feed.Description + "\n\n" + feed.Link,
	}

	if enableAutoRegistration {
		metadata["nip05"] = fmt.Sprintf("%s@%s", feed.Link, "rsslay.nostr.moe")
	}

	if feed.Image != nil {
		metadata["picture"] = feed.Image.URL
	} else if defaultProfilePictureUrl != "" {
		metadata["picture"] = defaultProfilePictureUrl
	}

	content, _ := json.Marshal(metadata)

	createdAt := time.Now()
	if feed.PublishedParsed != nil {
		createdAt = *feed.PublishedParsed
	}

	evt := nostr.Event{
		PubKey:    pubkey,
		CreatedAt: createdAt,
		Kind:      nostr.KindSetMetadata,
		Tags:      nostr.Tags{},
		Content:   string(content),
	}
	evt.ID = string(evt.Serialize())

	return evt
}

func ItemToTextNote(pubkey string, item *gofeed.Item, feed *gofeed.Feed, defaultCreatedAt time.Time, originalUrl string) nostr.Event {
	content := ""
	if item.Title != "" {
		content = "**" + item.Title + "**\n\n"
	}
	description := strip.StripTags(item.Description)

	if !strings.EqualFold(item.Title, description) {
		content += description
	}

	shouldUpgradeLinkSchema := false

	// Handle Nitter special cases (duplicates and http schema)
	if strings.Contains(feed.Description, "Twitter feed") {
		content = ""
		shouldUpgradeLinkSchema = true

		if strings.HasPrefix(originalUrl, "https://") {
			description = strings.ReplaceAll(description, "http://", "https://")
		}

		if strings.Contains(item.Title, "RT by @") {
			if len(item.DublinCoreExt.Creator) > 0 {
				content = "**" + "RT " + item.DublinCoreExt.Creator[0] + ":**\n\n"
			}
		} else if strings.Contains(item.Title, "R to @") {
			fields := strings.Fields(item.Title)
			if len(fields) >= 2 {
				replyingToHandle := fields[2]
				content = "**" + "Response to " + replyingToHandle + ":**\n\n"
			}
		}
		content += description
	}

	if len(content) > 250 {
		content += content[0:249] + "…"
	}

	if shouldUpgradeLinkSchema {
		item.Link = strings.ReplaceAll(item.Link, "http://", "https://")
	}
	content += "\n\n" + item.Link

	createdAt := defaultCreatedAt
	if item.UpdatedParsed != nil {
		createdAt = *item.UpdatedParsed
	}
	if item.PublishedParsed != nil {
		createdAt = *item.PublishedParsed
	}

	evt := nostr.Event{
		PubKey:    pubkey,
		CreatedAt: createdAt,
		Kind:      nostr.KindTextNote,
		Tags:      nostr.Tags{},
		Content:   content,
	}
	evt.ID = string(evt.Serialize())

	return evt
}

func PrivateKeyFromFeed(url string, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(url))
	r := m.Sum(nil)
	return hex.EncodeToString(r)
}

func DeleteInvalidFeed(url string, db *sql.DB) {
	if _, err := db.Exec(`DELETE FROM feeds WHERE url=?`, url); err != nil {
		log.Printf("failure to delete invalid feed: " + err.Error())
	} else {
		log.Printf("deleted invalid feed with url %q", url)
	}
}
