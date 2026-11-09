package sms

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/twilio/twilio-go"
	twilioclient "github.com/twilio/twilio-go/client"
	"github.com/warpstreamlabs/bento/public/service"
)

const (
	fieldAccountSID = "account_sid"
	fieldAuthToken  = "auth_token"
	fieldRegion     = "region"
	fieldEdge       = "edge"
	fieldTimeout    = "timeout"

	envAccountSID = "TWILIO_ACCOUNT_SID"
	envAuthToken  = "TWILIO_AUTH_TOKEN"
)

// sessionFields returns the credentials and connection fields shared by every
// component in this module.
func sessionFields() []*service.ConfigField {
	return []*service.ConfigField{
		service.NewStringField(fieldAccountSID).
			Description("The Twilio account SID used both for authentication and as the resource path. When left empty the environment variable `TWILIO_ACCOUNT_SID` is used.").
			Default(""),
		service.NewStringField(fieldAuthToken).
			Description("The Twilio auth token used for authentication. When left empty the environment variable `TWILIO_AUTH_TOKEN` is used.").
			Default("").
			Secret(),
		service.NewStringField(fieldEdge).
			Description("The [Twilio edge location](https://www.twilio.com/docs/global-infrastructure/edge-locations) to use for requests.").
			Default("").
			Advanced(),
		service.NewStringField(fieldRegion).
			Description("The [Twilio region](https://www.twilio.com/docs/global-infrastructure/using-a-specific-twilio-region) to use for requests.").
			Default("").
			Advanced(),
		service.NewDurationField(fieldTimeout).
			Description("The maximum period to wait for a Twilio API request before abandoning it and reattempting.").
			Default("10s").
			Advanced(),
	}
}

type sessionConfig struct {
	accountSID string
	authToken  string
	region     string
	edge       string
	timeout    time.Duration
}

func sessionConfigFromParsed(conf *service.ParsedConfig) (sessionConfig, error) {
	var c sessionConfig
	var err error

	if c.timeout, err = conf.FieldDuration(fieldTimeout); err != nil {
		return c, err
	}
	if c.accountSID, err = conf.FieldString(fieldAccountSID); err != nil {
		return c, err
	}
	if c.authToken, err = conf.FieldString(fieldAuthToken); err != nil {
		return c, err
	}
	if c.region, err = conf.FieldString(fieldRegion); err != nil {
		return c, err
	}
	if c.edge, err = conf.FieldString(fieldEdge); err != nil {
		return c, err
	}

	if c.accountSID == "" {
		c.accountSID = os.Getenv(envAccountSID)
	}
	if c.authToken == "" {
		c.authToken = os.Getenv(envAuthToken)
	}
	if c.accountSID == "" {
		return c, fmt.Errorf("a Twilio account SID is required: set '%s' or the environment variable '%s'", fieldAccountSID, envAccountSID)
	}
	if c.authToken == "" {
		return c, fmt.Errorf("a Twilio auth token is required: set '%s' or the environment variable '%s'", fieldAuthToken, envAuthToken)
	}
	return c, nil
}

// newRestClient builds a Twilio REST client from the session configuration.
func (c sessionConfig) newRestClient() *twilio.RestClient {
	base := &twilioclient.Client{
		Credentials: twilioclient.NewCredentials(c.accountSID, c.authToken),
		HTTPClient: &http.Client{
			Timeout: c.timeout,
		},
	}
	base.SetAccountSid(c.accountSID)

	rc := twilio.NewRestClientWithParams(twilio.ClientParams{Client: base})
	if c.edge != "" {
		rc.SetEdge(c.edge)
	}
	if c.region != "" {
		rc.SetRegion(c.region)
	}
	return rc
}
