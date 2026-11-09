package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"hash"
	"net/url"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/warpstreamlabs/bento/public/service"
)

const testURL = "https://example.com/twilio/webhook"

func parseConfig(t *testing.T, yaml string) *service.ParsedConfig {
	t.Helper()
	conf, err := webhookProcessorSpec().ParseYAML(yaml, nil)
	require.NoError(t, err)
	return conf
}

func newTestProcessor(t *testing.T, yaml string) *webhookProcessor {
	t.Helper()
	p, err := newWebhookProcessor(parseConfig(t, yaml), service.MockResources())
	require.NoError(t, err)
	return p
}

func signRequest(t *testing.T, token, webhookURL string, params url.Values, newHash func() hash.Hash) string {
	t.Helper()

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var payload = webhookURL
	for _, k := range keys {
		payload += k + params.Get(k)
	}

	mac := hmac.New(newHash, []byte(token))
	_, err := mac.Write([]byte(payload))
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func signedMessage(t *testing.T, token string, params url.Values) *service.Message {
	t.Helper()
	msg := service.NewMessage([]byte(params.Encode()))
	msg.MetaSetMut(metaSignature, signRequest(t, token, testURL, params, sha1.New))
	return msg
}

func process(t *testing.T, p *webhookProcessor, msg *service.Message) service.MessageBatch {
	t.Helper()
	out, err := p.Process(context.Background(), msg)
	require.NoError(t, err)
	return out
}

func structured(t *testing.T, msg *service.Message) map[string]any {
	t.Helper()
	event, err := msg.AsStructured()
	require.NoError(t, err)
	m, ok := event.(map[string]any)
	require.True(t, ok, "expected a structured object, got %T", event)
	return m
}

func TestWebhookStatusEvent(t *testing.T) {
	p := newTestProcessor(t, `
url: https://example.com/twilio/webhook
auth_token: test-token
`)

	msg := signedMessage(t, "test-token", url.Values{
		"MessageSid":    {"SM123"},
		"MessageStatus": {"undelivered"},
		"ErrorCode":     {"30003"},
		"ErrorMessage":  {"Unreachable destination handset"},
		"From":          {"+15551234567"},
		"To":            {"+15550000000"},
		"AccountSid":    {"AC123"},
		"ApiVersion":    {"2010-04-01"},
	})

	out := process(t, p, msg)
	require.Len(t, out, 1)

	assert.Equal(t, map[string]any{
		"type":          "status",
		"message_sid":   "SM123",
		"account_sid":   "AC123",
		"from":          "+15551234567",
		"to":            "+15550000000",
		"status":        "undelivered",
		"error_code":    30003,
		"error_message": "Unreachable destination handset",
		"api_version":   "2010-04-01",
	}, structured(t, out[0]))
}

func TestWebhookStatusEventWithoutError(t *testing.T) {
	p := newTestProcessor(t, `
url: https://example.com/twilio/webhook
auth_token: test-token
`)

	msg := signedMessage(t, "test-token", url.Values{
		"SmsSid":    {"SM456"},
		"SmsStatus": {"delivered"},
		"To":        {"+15550000000"},
	})

	out := process(t, p, msg)
	require.Len(t, out, 1)

	assert.Equal(t, map[string]any{
		"type":        "status",
		"message_sid": "SM456",
		"to":          "+15550000000",
		"status":      "delivered",
	}, structured(t, out[0]))
}

func TestWebhookInboundOptOut(t *testing.T) {
	p := newTestProcessor(t, `
url: https://example.com/twilio/webhook
auth_token: test-token
`)

	msg := signedMessage(t, "test-token", url.Values{
		"MessageSid":    {"SM789"},
		"MessageStatus": {"received"},
		"Body":          {"STOP"},
		"OptOutType":    {"STOP"},
		"From":          {"+15550000000"},
		"To":            {"+15551234567"},
		"AccountSid":    {"AC123"},
	})

	out := process(t, p, msg)
	require.Len(t, out, 1)

	assert.Equal(t, map[string]any{
		"type":         "inbound",
		"message_sid":  "SM789",
		"account_sid":  "AC123",
		"from":         "+15550000000",
		"to":           "+15551234567",
		"status":       "received",
		"body":         "STOP",
		"opt_out_type": "STOP",
	}, structured(t, out[0]))
}

func TestWebhookInboundWithoutOptOut(t *testing.T) {
	p := newTestProcessor(t, `
url: https://example.com/twilio/webhook
auth_token: test-token
`)

	msg := signedMessage(t, "test-token", url.Values{
		"MessageSid": {"SM999"},
		"SmsStatus":  {"received"},
		"Body":       {"hello from a human"},
		"From":       {"+15550000000"},
		"To":         {"+15551234567"},
	})

	out := process(t, p, msg)
	require.Len(t, out, 1)

	event := structured(t, out[0])
	assert.Equal(t, "inbound", event["type"])
	assert.Equal(t, "hello from a human", event["body"])
	_, hasOptOut := event["opt_out_type"]
	assert.False(t, hasOptOut)
}

func TestWebhookForcedKind(t *testing.T) {
	p := newTestProcessor(t, `
url: https://example.com/twilio/webhook
auth_token: test-token
kind: status
`)

	msg := signedMessage(t, "test-token", url.Values{
		"MessageSid":    {"SM111"},
		"MessageStatus": {"delivered"},
		"Body":          {"this would normally be inbound"},
	})

	out := process(t, p, msg)
	require.Len(t, out, 1)

	event := structured(t, out[0])
	assert.Equal(t, "status", event["type"])
	_, hasBody := event["body"]
	assert.False(t, hasBody)
}

func TestWebhookSharedKeySignature(t *testing.T) {
	p := newTestProcessor(t, `
url: https://example.com/twilio/webhook
auth_token: shared-key-secret
`)

	params := url.Values{
		"MessageSid":    {"SM222"},
		"MessageStatus": {"delivered"},
	}
	msg := service.NewMessage([]byte(params.Encode()))
	msg.MetaSetMut(metaSignature, signRequest(t, "shared-key-secret", testURL, params, sha256.New))
	msg.MetaSetMut(metaSignatureSettings, "key=webhooks_sharedkey_abc,algorithm=HMAC_SHA256")

	out := process(t, p, msg)
	require.Len(t, out, 1)
}

func TestWebhookDropsInvalidRequests(t *testing.T) {
	const config = `
url: https://example.com/twilio/webhook
auth_token: test-token
`

	tests := []struct {
		name string
		msg  func(t *testing.T) *service.Message
	}{
		{
			name: "missing signature",
			msg: func(t *testing.T) *service.Message {
				return service.NewMessage([]byte(`MessageStatus=delivered`))
			},
		},
		{
			name: "invalid signature",
			msg: func(t *testing.T) *service.Message {
				m := signedMessage(t, "wrong-token", url.Values{"MessageStatus": {"delivered"}})
				return m
			},
		},
		{
			name: "unknown payload",
			msg: func(t *testing.T) *service.Message {
				return signedMessage(t, "test-token", url.Values{"Unrelated": {"value"}})
			},
		},
		{
			name: "malformed body",
			msg: func(t *testing.T) *service.Message {
				m := service.NewMessage([]byte("body=%zz"))
				m.MetaSetMut(metaSignature, signRequest(t, "test-token", testURL, url.Values{"body": {"%zz"}}, sha1.New))
				return m
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := newTestProcessor(t, config)
			assert.Empty(t, process(t, p, test.msg(t)))
		})
	}
}

func TestWebhookConfig(t *testing.T) {
	t.Run("missing auth token", func(t *testing.T) {
		t.Setenv(envAuthToken, "")
		_, err := newWebhookProcessor(parseConfig(t, `
url: https://example.com/twilio/webhook
`), service.MockResources())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "auth token is required")
	})

	t.Run("environment fallback", func(t *testing.T) {
		t.Setenv(envAuthToken, "env-token")
		p, err := newWebhookProcessor(parseConfig(t, `
url: https://example.com/twilio/webhook
`), service.MockResources())
		require.NoError(t, err)
		assert.NotNil(t, p.validator)
	})

	t.Run("relative url", func(t *testing.T) {
		_, err := newWebhookProcessor(parseConfig(t, `
url: /twilio/webhook
auth_token: test-token
`), service.MockResources())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be an absolute URL")
	})

	t.Run("missing url", func(t *testing.T) {
		_, err := webhookProcessorSpec().ParseYAML(`auth_token: test-token`, nil)
		require.Error(t, err)
	})
}
