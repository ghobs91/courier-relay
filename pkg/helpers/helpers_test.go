package helpers

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"
)

const sampleInvalidUrl = "https:// nostr.example/"
const sampleValidUrl = "https://nostr.example"

func TestJoinWithInvalidUrlReturnsNil(t *testing.T) {
	join, err := UrlJoin(sampleInvalidUrl)
	assert.Equal(t, join, "")
	assert.ErrorContains(t, err, "invalid character")
}

func TestJoinWithValidUrlAndNoExtraElementsReturnsBaseUrl(t *testing.T) {
	join, err := UrlJoin(sampleValidUrl)
	assert.Equal(t, sampleValidUrl, join)
	assert.NoError(t, err)
}

func TestJoinWithValidUrlAndExtraElementsReturnsValidUrl(t *testing.T) {
	join, err := UrlJoin(sampleValidUrl, "rss")
	expectedJoinResult := fmt.Sprintf("%s/%s", sampleValidUrl, "rss")
	assert.Equal(t, expectedJoinResult, join)
	assert.NoError(t, err)
}
