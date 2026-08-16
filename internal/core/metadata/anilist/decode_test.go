package anilist

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubResponse builds the response decode would be handed, without a server.
func stubResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

// The outage of 2026-08-15: HTTP 403 carrying a GraphQL envelope that says why.
func TestDecodeReportsTheEnvelopeMessageOnANon200(t *testing.T) {
	body := `{"errors":[{"message":"The AniList API has been temporarily disabled due to severe stability issues.","status":403}],"data":null}`
	err := decode(stubResponse(http.StatusForbidden, body), new(struct{}))

	want := "anilist: status 403: The AniList API has been temporarily disabled due to severe stability issues."
	if err == nil || err.Error() != want {
		t.Fatalf("decode = %v, want %q", err, want)
	}
}

// A provider that is down often answers with a proxy's error page, which carries
// no message to extract — reporting nothing would be worse than the raw dump.
func TestDecodeFallsBackToTheBodyWhenItIsNotAnEnvelope(t *testing.T) {
	body := "<html><head><title>Gateway timed out</title></head></html>"
	err := decode(stubResponse(http.StatusBadGateway, body), new(struct{}))

	want := "anilist: status 502: " + body
	if err == nil || err.Error() != want {
		t.Fatalf("decode = %v, want %q", err, want)
	}
}

func TestDecodeFallsBackWhenTheEnvelopeCarriesNoMessage(t *testing.T) {
	body := `{"errors":[],"data":null}`
	err := decode(stubResponse(http.StatusInternalServerError, body), new(struct{}))

	want := "anilist: status 500: " + body
	if err == nil || err.Error() != want {
		t.Fatalf("decode = %v, want %q", err, want)
	}
}

// The bound is a literal: read off maxErrBytes, this assertion would move with
// the mutation it exists to catch and could never fail.
func TestDecodeBoundsANon200Body(t *testing.T) {
	err := decode(stubResponse(http.StatusBadGateway, strings.Repeat("x", 64<<10)), new(struct{}))
	if err == nil {
		t.Fatal("decode = nil, want an error")
	}
	if got := len(err.Error()); got > 2112 { // 2048 bytes of body, plus slack for the prefix
		t.Fatalf("error is %d bytes, want at most 2048 of body behind a short prefix", got)
	}
}

func TestDecodeReportsAGraphQLErrorOnA200(t *testing.T) {
	body := `{"data":null,"errors":[{"message":"Not Found"}]}`
	err := decode(stubResponse(http.StatusOK, body), new(struct{}))

	if err == nil || err.Error() != "anilist: Not Found" {
		t.Fatalf("decode = %v, want %q", err, "anilist: Not Found")
	}
}

func TestDecodeUnmarshalsTheDataFieldOnA200(t *testing.T) {
	var out struct {
		Media struct {
			ID int `json:"id"`
		} `json:"Media"`
	}
	if err := decode(stubResponse(http.StatusOK, `{"data":{"Media":{"id":7}}}`), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Media.ID != 7 {
		t.Fatalf("decoded id %d, want 7", out.Media.ID)
	}
}
