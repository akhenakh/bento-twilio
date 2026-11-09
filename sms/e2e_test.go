//go:build e2e

package sms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	twilio "github.com/twilio/twilio-go"
	twilioclient "github.com/twilio/twilio-go/client"
	"github.com/warpstreamlabs/bento/public/service"
)

// rewriteTransport redirects requests made to the Twilio API to a local test
// server, keeping the path, query and body untouched.
type rewriteTransport struct {
	target *url.URL
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestEndToEndSendSMS(t *testing.T) {
	var (
		gotPath  string
		gotForm  url.Values
		gotUser  string
		gotPass  string
		formErr  error
		requests int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotPath = r.URL.Path
		gotUser, gotPass, _ = r.BasicAuth()
		formErr = r.ParseForm()
		gotForm = r.PostForm

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sid":          "SMendtoend",
			"status":       "queued",
			"num_segments": "1",
		})
	}))
	defer srv.Close()

	w, err := newSMSWriter(parseConfig(t, `
account_sid: ACe2e
auth_token: e2esecret
from: "+15551234567"
to: '{{ .phone }}'
body: 'Alert for {{ .site }}'
media_urls:
  - '{{ .chart }}'
`), service.MockResources())
	require.NoError(t, err)

	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	base := &twilioclient.Client{
		Credentials: twilioclient.NewCredentials("ACe2e", "e2esecret"),
		HTTPClient: &http.Client{
			Transport: rewriteTransport{target: target},
		},
	}
	base.SetAccountSid("ACe2e")
	w.client = twilio.NewRestClientWithParams(twilio.ClientParams{Client: base}).Api

	err = w.Write(context.Background(), service.NewMessage([]byte(`{
		"phone": "+15550000000",
		"site": "example.org",
		"chart": "https://example.com/chart.png"
	}`)))
	require.NoError(t, err)
	require.NoError(t, formErr)

	assert.Equal(t, 1, requests)
	assert.Equal(t, "/2010-04-01/Accounts/ACe2e/Messages.json", gotPath)
	assert.Equal(t, "ACe2e", gotUser)
	assert.Equal(t, "e2esecret", gotPass)

	assert.Equal(t, "+15550000000", gotForm.Get("To"))
	assert.Equal(t, "+15551234567", gotForm.Get("From"))
	assert.Equal(t, "Alert for example.org", gotForm.Get("Body"))
	assert.Equal(t, []string{"https://example.com/chart.png"}, gotForm["MediaUrl"])
}
