package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	api "github.com/twilio/twilio-go/rest/api/v2010"
	"github.com/warpstreamlabs/bento/public/service"
)

const (
	fieldFrom               = "from"
	fieldTo                 = "to"
	fieldBody               = "body"
	fieldMediaURLs          = "media_urls"
	fieldMessagingServiceID = "messaging_service_sid"
	fieldFallbackFrom       = "fallback_from"
	fieldStatusCallback     = "status_callback"
	fieldContentSID         = "content_sid"
	fieldContentVariables   = "content_variables"
	fieldValidityPeriod     = "validity_period"
	fieldSmartEncoded       = "smart_encoded"
	fieldShortenURLs        = "shorten_urls"
	fieldSendAsMMS          = "send_as_mms"
	fieldMaxPrice           = "max_price"

	maxMediaURLs = 10
)

func smsOutputSpec() *service.ConfigSpec {
	return service.NewConfigSpec().
		Stable().
		Categories("Services", "Twilio").
		Summary("Sends an SMS or MMS message to a single recipient through the Twilio Messages API.").
		Description(`
Sends a message via the [Twilio Messages API](https://www.twilio.com/docs/messaging/api/message-resource#create-a-message-resource) for each message consumed by the output.

The recipient (`+"`to`"+`), sender (`+"`from`"+`), bodies and media URLs are processed as [Go templates](https://pkg.go.dev/text/template) where the message payload is the data context. When the payload is a JSON object its fields are available directly, e.g. `+"`{{ .customer.phone }}`"+`. A small set of helper functions is available in addition to the built-ins: `+"`toJson`"+`, `+"`base64`"+`, `+"`upper`"+`, `+"`lower`"+`, `+"`trim`"+`, `+"`join`"+`, `+"`replace`"+`, `+"`default`"+`, `+"`env`"+`, `+"`now`"+` and `+"`formatTime`"+`.

At least one of `+"`body`"+`, `+"`media_urls`"+` or `+"`content_sid`"+` must be configured. Recipients and senders must be in [E.164](https://www.twilio.com/docs/glossary/what-e164) format (for example `+"`+15551234567`"+`) unless an alphanumeric sender ID or short code is used. Messages longer than 160 GSM-7 characters are segmented and billed accordingly by Twilio.`).
		Example(
			"Sending an alert",
			"Each message payload becomes the template context, so a JSON document such as `{\"phone\":\"+15551234567\",\"site\":\"example.org\"}` can be delivered directly.",
			`
output:
  twilio_sms:
    account_sid: ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
    auth_token: your_auth_token
    from: "+15551234567"
    to: '{{ .phone }}'
    body: 'Alert for {{ .site }}: a threshold was crossed.'
`,
		).
		Example(
			"Sending an MMS with media",
			"Up to ten media URLs can be attached to a message. Twilio fetches the media itself, so the URLs must be publicly reachable by Twilio.",
			`
output:
  twilio_sms:
    from: "+15551234567"
    to: '{{ .phone }}'
    body: 'Latest chart for {{ .site }}'
    media_urls:
      - '{{ .chart_url }}'
`,
		).
		Footnotes(`
## Credentials

The `+"`account_sid`"+` and `+"`auth_token`"+` fields can be set explicitly or provided through the `+"`TWILIO_ACCOUNT_SID`"+` and `+"`TWILIO_AUTH_TOKEN`"+` environment variables. Auth tokens are sent using HTTP basic authentication and are never logged.

## Delivery

The Twilio API responds as soon as it accepts the message for delivery. It does not guarantee that the message reaches the handset. Use `+"`status_callback`"+` to receive delivery status updates, and keep in mind that Twilio retries and message ordering are outside of Bento's control.

Messages that are rejected by Twilio (invalid number, insufficient funds, queued message limit reached, etc.) cause the write to fail so that Bento can retry or route the message according to your error handling configuration.`+service.OutputPerformanceDocs(true, false)).
		Fields(
			service.NewStringField(fieldFrom).
				Description("The sender's Twilio phone number in [E.164](https://www.twilio.com/docs/glossary/what-e164) format, alphanumeric sender ID, short code, or channel address, rendered as a Go template. Required unless `messaging_service_sid` is set.").
				Optional().
				Example("+15551234567"),
			service.NewStringField(fieldTo).
				Description("The recipient's phone number in [E.164](https://www.twilio.com/docs/glossary/what-e164) format, rendered as a Go template.").
				Example("{{ .phone }}"),
			service.NewStringField(fieldBody).
				Description("The text body of the message, rendered as a Go template. At least one of `body`, `media_urls` or `content_sid` must be configured.").
				Optional().
				Example("Alert for {{ .site }}: {{ .message }}"),
			service.NewStringListField(fieldMediaURLs).
				Description("A list of media URLs to attach to the message as MMS (up to 10), each rendered as a Go template. Twilio fetches the media itself, so the URLs must be publicly reachable.").
				Optional().
				Advanced(),
			service.NewStringField(fieldMessagingServiceID).
				Description("The SID of a [Messaging Service](https://www.twilio.com/docs/messaging/services) to send through. When set, Twilio selects a sender from the service's sender pool unless `from` is also provided.").
				Optional().
				Advanced().
				Example("MGxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
			service.NewStringField(fieldFallbackFrom).
				Description("A fallback SMS sender used when the recipient cannot be reached over RCS, rendered as a Go template. Only valid together with a Messaging Service that contains an RCS sender.").
				Optional().
				Advanced(),
			service.NewStringField(fieldStatusCallback).
				Description("A URL that Twilio requests with [message status updates](https://www.twilio.com/docs/messaging/api/message-resource#twilios-request-to-the-statuscallback-url), rendered as a Go template.").
				Optional().
				Advanced().
				Example("https://example.com/twilio/status"),
			service.NewStringField(fieldContentSID).
				Description("The SID of a [Content API](https://www.twilio.com/docs/content) template to send instead of a plain body.").
				Optional().
				Advanced().
				Example("HXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"),
			service.NewStringField(fieldContentVariables).
				Description("A JSON object of [template variables](https://www.twilio.com/docs/content/using-variables-with-content-api) used with `content_sid`, rendered as a Go template. The rendered value must be a JSON object, e.g. `{\"1\":\"Fabrice\"}`.").
				Optional().
				Advanced().
				Secret(),
			service.NewIntField(fieldValidityPeriod).
				Description("The maximum number of seconds a message may remain queued before it is discarded by Twilio. Must be between 1 and 36000, values above 5 are recommended. When unset Twilio's default is used.").
				Optional().
				Advanced(),
			service.NewBoolField(fieldSmartEncoded).
				Description("Whether Twilio should detect Unicode characters that have a similar GSM-7 equivalent and replace them, reducing the number of message segments.").
				Default(false).
				Advanced(),
			service.NewBoolField(fieldShortenURLs).
				Description("Whether Twilio should [shorten links](https://www.twilio.com/docs/messaging/features/link-shortening) in the body. Requires `messaging_service_sid` with link shortening configured.").
				Default(false).
				Advanced(),
			service.NewBoolField(fieldSendAsMMS).
				Description("Whether Twilio should deliver the message as a single MMS message regardless of the presence of media.").
				Default(false).
				Advanced(),
			service.NewFloatField(fieldMaxPrice).
				Description("The maximum price in USD that Twilio may charge for the message. When unset Twilio's default is used.").
				Optional().
				Advanced(),
			service.NewOutputMaxInFlightField(),
		).
		Fields(sessionFields()...)
}

func init() {
	err := service.RegisterOutput("twilio_sms", smsOutputSpec(),
		func(conf *service.ParsedConfig, mgr *service.Resources) (service.Output, int, error) {
			maxInFlight, err := conf.FieldMaxInFlight()
			if err != nil {
				return nil, 0, err
			}
			w, err := newSMSWriter(conf, mgr)
			if err != nil {
				return nil, 0, err
			}
			return w, maxInFlight, nil
		})
	if err != nil {
		panic(err)
	}
}

// messageCreator is the subset of the Twilio Messages API used by the
// component. It exists so that the writer can be tested without hitting
// Twilio.
type messageCreator interface {
	CreateMessage(params *api.CreateMessageParams) (*api.ApiV2010Message, error)
}

type smsConfig struct {
	session sessionConfig

	from     *template.Template
	to       *template.Template
	body     *template.Template
	mediaURL []*template.Template

	messagingServiceSID string
	fallbackFrom        *template.Template
	statusCallback      *template.Template
	contentSID          string
	contentVariables    *template.Template

	validityPeriod int
	smartEncoded   bool
	shortenURLs    bool
	sendAsMMS      bool
	maxPrice       float64
}

func smsConfigFromParsed(conf *service.ParsedConfig) (smsConfig, error) {
	var c smsConfig
	var err error

	if c.session, err = sessionConfigFromParsed(conf); err != nil {
		return c, err
	}

	if c.from, err = optionalTemplate(conf, fieldFrom); err != nil {
		return c, err
	}
	if c.to, err = requiredTemplate(conf, fieldTo); err != nil {
		return c, err
	}
	if c.body, err = optionalTemplate(conf, fieldBody); err != nil {
		return c, err
	}

	if conf.Contains(fieldMediaURLs) {
		urls, err := conf.FieldStringList(fieldMediaURLs)
		if err != nil {
			return c, err
		}
		if len(urls) > maxMediaURLs {
			return c, fmt.Errorf("'%s' accepts at most %d entries, got %d", fieldMediaURLs, maxMediaURLs, len(urls))
		}
		if c.mediaURL, err = parseTemplateList(fieldMediaURLs, urls); err != nil {
			return c, err
		}
	}

	if conf.Contains(fieldMessagingServiceID) {
		if c.messagingServiceSID, err = conf.FieldString(fieldMessagingServiceID); err != nil {
			return c, err
		}
	}
	if c.fallbackFrom, err = optionalTemplate(conf, fieldFallbackFrom); err != nil {
		return c, err
	}
	if c.statusCallback, err = optionalTemplate(conf, fieldStatusCallback); err != nil {
		return c, err
	}
	if conf.Contains(fieldContentSID) {
		if c.contentSID, err = conf.FieldString(fieldContentSID); err != nil {
			return c, err
		}
	}
	if c.contentVariables, err = optionalTemplate(conf, fieldContentVariables); err != nil {
		return c, err
	}
	if c.contentVariables != nil && c.contentSID == "" {
		return c, fmt.Errorf("'%s' requires '%s' to be set", fieldContentVariables, fieldContentSID)
	}

	if conf.Contains(fieldValidityPeriod) {
		if c.validityPeriod, err = conf.FieldInt(fieldValidityPeriod); err != nil {
			return c, err
		}
		if c.validityPeriod < 1 || c.validityPeriod > 36000 {
			return c, fmt.Errorf("'%s' must be between 1 and 36000 seconds", fieldValidityPeriod)
		}
	}
	if c.smartEncoded, err = conf.FieldBool(fieldSmartEncoded); err != nil {
		return c, err
	}
	if c.shortenURLs, err = conf.FieldBool(fieldShortenURLs); err != nil {
		return c, err
	}
	if c.sendAsMMS, err = conf.FieldBool(fieldSendAsMMS); err != nil {
		return c, err
	}
	if conf.Contains(fieldMaxPrice) {
		if c.maxPrice, err = conf.FieldFloat(fieldMaxPrice); err != nil {
			return c, err
		}
		if c.maxPrice < 0 {
			return c, fmt.Errorf("'%s' must not be negative", fieldMaxPrice)
		}
	}

	if c.from == nil && c.messagingServiceSID == "" {
		return c, fmt.Errorf("either '%s' or '%s' must be set", fieldFrom, fieldMessagingServiceID)
	}
	if c.body == nil && len(c.mediaURL) == 0 && c.contentSID == "" {
		return c, fmt.Errorf("at least one of '%s', '%s' or '%s' must be set", fieldBody, fieldMediaURLs, fieldContentSID)
	}
	if c.shortenURLs && c.messagingServiceSID == "" {
		return c, fmt.Errorf("'%s' requires '%s' to be set", fieldShortenURLs, fieldMessagingServiceID)
	}
	return c, nil
}

func requiredTemplate(conf *service.ParsedConfig, field string) (*template.Template, error) {
	s, err := conf.FieldString(field)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("'%s' must not be empty", field)
	}
	return parseTemplate(field, s)
}

func optionalTemplate(conf *service.ParsedConfig, field string) (*template.Template, error) {
	if !conf.Contains(field) {
		return nil, nil
	}
	s, err := conf.FieldString(field)
	if err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	return parseTemplate(field, s)
}

type smsWriter struct {
	conf   smsConfig
	client messageCreator
	log    *service.Logger
}

func newSMSWriter(conf *service.ParsedConfig, mgr *service.Resources) (*smsWriter, error) {
	c, err := smsConfigFromParsed(conf)
	if err != nil {
		return nil, err
	}
	return &smsWriter{
		conf: c,
		log:  mgr.Logger(),
	}, nil
}

func (w *smsWriter) Connect(ctx context.Context) error {
	if w.client != nil {
		return nil
	}
	w.client = w.conf.session.newRestClient().Api
	return nil
}

func (w *smsWriter) Write(ctx context.Context, msg *service.Message) error {
	if w.client == nil {
		return service.ErrNotConnected
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	data, err := messageData(msg)
	if err != nil {
		return fmt.Errorf("extracting message payload: %w", err)
	}

	to, err := render(w.conf.to, data)
	if err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldTo, err)
	}
	if to = strings.TrimSpace(to); to == "" {
		return fmt.Errorf("'%s' rendered to an empty number", fieldTo)
	}

	from, err := render(w.conf.from, data)
	if err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldFrom, err)
	}
	if from = strings.TrimSpace(from); from == "" && w.conf.messagingServiceSID == "" {
		return fmt.Errorf("'%s' rendered to an empty number", fieldFrom)
	}

	body, err := render(w.conf.body, data)
	if err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldBody, err)
	}

	mediaURLs, err := renderList(w.conf.mediaURL, data)
	if err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldMediaURLs, err)
	}
	if strings.TrimSpace(body) == "" && len(mediaURLs) == 0 && w.conf.contentSID == "" {
		return fmt.Errorf("both '%s' and '%s' rendered to empty values and no '%s' is set", fieldBody, fieldMediaURLs, fieldContentSID)
	}

	params := &api.CreateMessageParams{}
	params.SetTo(to)
	if from != "" {
		params.SetFrom(from)
	}
	if w.conf.messagingServiceSID != "" {
		params.SetMessagingServiceSid(w.conf.messagingServiceSID)
	}
	if body != "" {
		params.SetBody(body)
	}
	if len(mediaURLs) > 0 {
		params.SetMediaUrl(mediaURLs)
	}
	if w.conf.contentSID != "" {
		params.SetContentSid(w.conf.contentSID)
	}
	if vars, err := render(w.conf.contentVariables, data); err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldContentVariables, err)
	} else if vars = strings.TrimSpace(vars); vars != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(vars), &obj); err != nil {
			return fmt.Errorf("'%s' must render to a JSON object: %w", fieldContentVariables, err)
		}
		params.SetContentVariables(vars)
	}
	if callback, err := render(w.conf.statusCallback, data); err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldStatusCallback, err)
	} else if callback = strings.TrimSpace(callback); callback != "" {
		params.SetStatusCallback(callback)
	}
	if fallback, err := render(w.conf.fallbackFrom, data); err != nil {
		return fmt.Errorf("rendering '%s': %w", fieldFallbackFrom, err)
	} else if fallback = strings.TrimSpace(fallback); fallback != "" {
		params.SetFallbackFrom(fallback)
	}
	if w.conf.validityPeriod > 0 {
		params.SetValidityPeriod(w.conf.validityPeriod)
	}
	if w.conf.smartEncoded {
		params.SetSmartEncoded(true)
	}
	if w.conf.shortenURLs {
		params.SetShortenUrls(true)
	}
	if w.conf.sendAsMMS {
		params.SetSendAsMms(true)
	}
	if w.conf.maxPrice > 0 {
		params.SetMaxPrice(float32(w.conf.maxPrice))
	}

	resp, err := w.client.CreateMessage(params)
	if err != nil {
		return fmt.Errorf("sending SMS to %s via Twilio: %w", to, err)
	}

	if resp != nil {
		w.log.Debugf("Sent SMS to %s (sid=%s status=%s segments=%s)",
			to, stringValue(resp.Sid), stringValue(resp.Status), stringValue(resp.NumSegments))
	} else {
		w.log.Debugf("Sent SMS to %s", to)
	}
	return nil
}

func (w *smsWriter) Close(ctx context.Context) error {
	w.client = nil
	return nil
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
