package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	api "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/warpstreamlabs/bento/public/service"
)

type mockMessageCreator struct {
	lastParams *api.CreateMessageParams
	resp       *api.ApiV2010Message
	err        error
	calls      int
}

func (m *mockMessageCreator) CreateMessage(params *api.CreateMessageParams) (*api.ApiV2010Message, error) {
	m.calls++
	m.lastParams = params
	if m.err != nil {
		return nil, m.err
	}
	if m.resp != nil {
		return m.resp, nil
	}
	return &api.ApiV2010Message{}, nil
}

func strPtr(s string) *string { return &s }

func parseConfig(t *testing.T, yaml string) *service.ParsedConfig {
	t.Helper()
	conf, err := smsOutputSpec().ParseYAML(yaml, nil)
	require.NoError(t, err)
	return conf
}

func newTestWriter(t *testing.T, yaml string) (*smsWriter, *mockMessageCreator) {
	t.Helper()
	w, err := newSMSWriter(parseConfig(t, yaml), service.MockResources())
	require.NoError(t, err)

	mock := &mockMessageCreator{
		resp: &api.ApiV2010Message{
			Sid:         strPtr("SM123"),
			Status:      strPtr("queued"),
			NumSegments: strPtr("1"),
		},
	}
	w.client = mock
	return w, mock
}

func TestSMSOutputWrite(t *testing.T) {
	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: '{{ .phone }}'
body: 'Alert for {{ .site }}: {{ .message }}'
media_urls:
  - '{{ .chart }}'
status_callback: https://example.com/status
validity_period: 300
smart_encoded: true
send_as_mms: true
max_price: 0.02
timeout: 5s
`)

	msg := service.NewMessage([]byte(`{
		"phone": "+15550000000",
		"site": "example.org",
		"message": "CPU above 90%",
		"chart": "https://example.com/chart.png"
	}`))
	require.NoError(t, w.Write(context.Background(), msg))

	p := mock.lastParams
	require.NotNil(t, p)
	require.NotNil(t, p.To)
	assert.Equal(t, "+15550000000", *p.To)
	require.NotNil(t, p.From)
	assert.Equal(t, "+15551234567", *p.From)
	require.NotNil(t, p.Body)
	assert.Equal(t, "Alert for example.org: CPU above 90%", *p.Body)
	require.NotNil(t, p.MediaUrl)
	assert.Equal(t, []string{"https://example.com/chart.png"}, *p.MediaUrl)
	require.NotNil(t, p.StatusCallback)
	assert.Equal(t, "https://example.com/status", *p.StatusCallback)
	require.NotNil(t, p.ValidityPeriod)
	assert.Equal(t, 300, *p.ValidityPeriod)
	require.NotNil(t, p.SmartEncoded)
	assert.True(t, *p.SmartEncoded)
	require.NotNil(t, p.SendAsMms)
	assert.True(t, *p.SendAsMms)
	require.NotNil(t, p.MaxPrice)
	assert.InDelta(t, 0.02, *p.MaxPrice, 1e-6)
}

func TestSMSOutputWriteNonJSONPayload(t *testing.T) {
	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
body: 'Received: {{ .value }}'
`)

	require.NoError(t, w.Write(context.Background(), service.NewMessage([]byte("hello"))))

	require.NotNil(t, mock.lastParams)
	require.NotNil(t, mock.lastParams.Body)
	assert.Equal(t, "Received: hello", *mock.lastParams.Body)
}

func TestSMSOutputWriteEnvTemplate(t *testing.T) {
	t.Setenv("BENTO_TWILIO_TEST_SENDER", "+15559999999")

	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
from: '{{ env "BENTO_TWILIO_TEST_SENDER" }}'
to: "+15550000000"
body: 'hello'
`)

	require.NoError(t, w.Write(context.Background(), service.NewMessage([]byte(`{}`))))

	require.NotNil(t, mock.lastParams.From)
	assert.Equal(t, "+15559999999", *mock.lastParams.From)
}

func TestSMSOutputWriteMessagingService(t *testing.T) {
	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
messaging_service_sid: MGtest
to: '{{ .phone }}'
body: 'hello'
shorten_urls: true
`)

	require.NoError(t, w.Write(context.Background(), service.NewMessage([]byte(`{"phone":"+15550000000"}`))))

	p := mock.lastParams
	require.NotNil(t, p)
	assert.Nil(t, p.From)
	require.NotNil(t, p.MessagingServiceSid)
	assert.Equal(t, "MGtest", *p.MessagingServiceSid)
	require.NotNil(t, p.ShortenUrls)
	assert.True(t, *p.ShortenUrls)
}

func TestSMSOutputWriteContentTemplate(t *testing.T) {
	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: '{{ .phone }}'
content_sid: HXtest
content_variables: '{"1":"{{ .name }}"}'
`)

	require.NoError(t, w.Write(context.Background(), service.NewMessage([]byte(`{"phone":"+15550000000","name":"Fabrice"}`))))

	p := mock.lastParams
	require.NotNil(t, p)
	assert.Nil(t, p.Body)
	require.NotNil(t, p.ContentSid)
	assert.Equal(t, "HXtest", *p.ContentSid)
	require.NotNil(t, p.ContentVariables)
	assert.JSONEq(t, `{"1":"Fabrice"}`, *p.ContentVariables)
}

func TestSMSOutputWriteMediaOnly(t *testing.T) {
	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
media_urls:
  - https://example.com/a.png
  - '{{ .empty }}'
`)

	require.NoError(t, w.Write(context.Background(), service.NewMessage([]byte(`{"empty":""}`))))

	p := mock.lastParams
	require.NotNil(t, p)
	assert.Nil(t, p.Body)
	require.NotNil(t, p.MediaUrl)
	assert.Equal(t, []string{"https://example.com/a.png"}, *p.MediaUrl)
}

func TestSMSOutputWriteErrors(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		payload string
		wantErr string
	}{
		{
			name: "empty recipient",
			config: `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: '{{ .phone }}'
body: hello
`,
			payload: `{}`,
			wantErr: "'to' rendered to an empty number",
		},
		{
			name: "empty sender and body",
			config: `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
body: '{{ .body }}'
media_urls:
  - '{{ .media }}'
`,
			payload: `{}`,
			wantErr: "both 'body' and 'media_urls' rendered to empty values",
		},
		{
			name: "invalid content variables",
			config: `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
content_sid: HXtest
content_variables: '{{ .vars }}'
`,
			payload: `{"vars":"not-json"}`,
			wantErr: "'content_variables' must render to a JSON object",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w, _ := newTestWriter(t, test.config)
			err := w.Write(context.Background(), service.NewMessage([]byte(test.payload)))
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestSMSOutputWriteAPIError(t *testing.T) {
	w, mock := newTestWriter(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
body: hello
`)
	mock.err = errors.New("twilio is unhappy")

	err := w.Write(context.Background(), service.NewMessage([]byte(`{}`)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "twilio is unhappy")
	assert.Contains(t, err.Error(), "+15550000000")
}

func TestSMSOutputNotConnected(t *testing.T) {
	w, err := newSMSWriter(parseConfig(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
body: hello
`), service.MockResources())
	require.NoError(t, err)

	err = w.Write(context.Background(), service.NewMessage([]byte(`{}`)))
	require.ErrorIs(t, err, service.ErrNotConnected)
}

func TestSMSOutputConnect(t *testing.T) {
	w, err := newSMSWriter(parseConfig(t, `
account_sid: ACtest
auth_token: token
from: "+15551234567"
to: "+15550000000"
body: hello
`), service.MockResources())
	require.NoError(t, err)

	require.NoError(t, w.Connect(context.Background()))
	assert.NotNil(t, w.client)
	// Calling Connect again must keep the existing client.
	client := w.client
	require.NoError(t, w.Connect(context.Background()))
	assert.Same(t, client, w.client)
	require.NoError(t, w.Close(context.Background()))
	assert.Nil(t, w.client)
}

func TestSMSConfigValidation(t *testing.T) {
	const creds = `
account_sid: ACtest
auth_token: token
`

	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "missing sender",
			config: `
to: "+15550000000"
body: hello
`,
			wantErr: "either 'from' or 'messaging_service_sid' must be set",
		},
		{
			name: "missing content",
			config: `
from: "+15551234567"
to: "+15550000000"
`,
			wantErr: "at least one of 'body', 'media_urls' or 'content_sid' must be set",
		},
		{
			name: "content variables without content sid",
			config: `
from: "+15551234567"
to: "+15550000000"
body: hello
content_variables: '{"1":"a"}'
`,
			wantErr: "'content_variables' requires 'content_sid' to be set",
		},
		{
			name: "shorten urls without messaging service",
			config: `
from: "+15551234567"
to: "+15550000000"
body: hello
shorten_urls: true
`,
			wantErr: "'shorten_urls' requires 'messaging_service_sid' to be set",
		},
		{
			name: "validity period out of range",
			config: `
from: "+15551234567"
to: "+15550000000"
body: hello
validity_period: 36001
`,
			wantErr: "'validity_period' must be between 1 and 36000 seconds",
		},
		{
			name: "too many media urls",
			config: `
from: "+15551234567"
to: "+15550000000"
body: hello
media_urls: ["1","2","3","4","5","6","7","8","9","10","11"]
`,
			wantErr: "'media_urls' accepts at most 10 entries, got 11",
		},
		{
			name: "negative max price",
			config: `
from: "+15551234567"
to: "+15550000000"
body: hello
max_price: -1
`,
			wantErr: "'max_price' must not be negative",
		},
		{
			name: "invalid template",
			config: `
from: "+15551234567"
to: "+15550000000"
body: '{{ .oops '
`,
			wantErr: "parsing template",
		},
		{
			name: "empty recipient",
			config: `
from: "+15551234567"
to: "  "
body: hello
`,
			wantErr: "'to' must not be empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newSMSWriter(parseConfig(t, creds+test.config), service.MockResources())
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
		})
	}
}

func TestSMSConfigCredentials(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		c, err := sessionConfigFromParsed(parseConfig(t, `
from: "+15551234567"
to: "+15550000000"
body: hello
account_sid: ACexplicit
auth_token: explicit-token
region: ie1
edge: dublin
timeout: 3s
`))
		require.NoError(t, err)
		assert.Equal(t, "ACexplicit", c.accountSID)
		assert.Equal(t, "explicit-token", c.authToken)
		assert.Equal(t, "ie1", c.region)
		assert.Equal(t, "dublin", c.edge)
		assert.Equal(t, 3*time.Second, c.timeout)
	})

	t.Run("environment fallback", func(t *testing.T) {
		t.Setenv(envAccountSID, "ACenv")
		t.Setenv(envAuthToken, "env-token")

		c, err := sessionConfigFromParsed(parseConfig(t, `
from: "+15551234567"
to: "+15550000000"
body: hello
`))
		require.NoError(t, err)
		assert.Equal(t, "ACenv", c.accountSID)
		assert.Equal(t, "env-token", c.authToken)
	})

	t.Run("missing", func(t *testing.T) {
		t.Setenv(envAccountSID, "")
		t.Setenv(envAuthToken, "")

		_, err := sessionConfigFromParsed(parseConfig(t, `
from: "+15551234567"
to: "+15550000000"
body: hello
`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account SID is required")
	})
}
