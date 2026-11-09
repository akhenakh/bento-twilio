package webhook

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	twilioclient "github.com/twilio/twilio-go/client"
	"github.com/warpstreamlabs/bento/public/service"
)

const (
	fieldAuthToken = "auth_token"
	fieldURL       = "url"
	fieldKind      = "kind"

	kindAuto    = "auto"
	kindStatus  = "status"
	kindInbound = "inbound"

	envAuthToken = "TWILIO_AUTH_TOKEN"

	metaSignature         = twilioclient.SignatureHeader
	metaSignatureSettings = twilioclient.SignatureSettingsHeader
)

func webhookProcessorSpec() *service.ConfigSpec {
	return service.NewConfigSpec().
		Stable().
		Categories("Services", "Twilio").
		Summary("Validates a Twilio webhook request and turns it into a JSON event.").
		Description(`
Validates and normalises webhook requests from Twilio: message status callbacks and inbound SMS messages. The processor is designed to sit directly after an `+"`http_server`"+` input, which exposes the `+"`X-Twilio-Signature`"+` and `+"`X-Twilio-Webhooks-Signature-Settings`"+` headers as message metadata.

Every request is authenticated with its Twilio signature before the payload is parsed. Requests with a missing or invalid signature are dropped (and logged) rather than returned as errors, so that Twilio receives a success response and does not retry a forged or malformed request.

The original payload is replaced with a normalised JSON object that can be routed to any downstream component (NATS, Kafka, a database, and so on). Message status callbacks produce events with `+"`type: status`"+` and inbound messages produce events with `+"`type: inbound`"+`; inbound `+"`OptOutType`"+` values (`+"`STOP`"+`, `+"`START`"+`, `+"`HELP`"+`) are passed through so that opt-out handling can live in the pipeline.

Only form-encoded SMS webhooks are parsed; media URLs from inbound MMS are ignored.`).
		Example(
			"Publishing Twilio events to NATS",
			"Twilio is configured to POST both the message status callback and the inbound message webhook to the same URL. The processor emits JSON events which are then published to NATS.",
			`
input:
  http_server:
    path: /twilio/webhook

pipeline:
  processors:
    - twilio_webhook:
        url: https://example.com/twilio/webhook

output:
  nats:
    urls: [ nats://localhost:4222 ]
    subject: twilio.events
`,
		).
		Footnotes(`
## Signature validation

Twilio signs requests with the auth token of the account that owns the webhook. Set `+"`auth_token`"+` to that token, or to the SharedKey secret when the request carries an `+"`X-Twilio-Webhooks-Signature-Settings`"+` header. The signature covers the exact public URL configured in Twilio, so `+"`url`"+` must match it, including any query string.

Twilio signatures do not include a timestamp or nonce, so the processor cannot detect a replayed request. Ensure the endpoint is only reachable over TLS and consider replay protection at the network layer.

## Delivery status and opt-outs

Point the output's `+"`status_callback`"+` at the same `+"`http_server`"+` URL to receive delivery failures (`+"`undelivered`"+`, `+"`failed`"+`) with their Twilio error codes. Configure the Twilio phone number or Messaging Service inbound webhook to receive `+"`STOP`"+`/`+"`START`"+`/`+"`HELP`"+` messages; when Advanced Opt-Out is enabled Twilio also sets `+"`OptOutType`"+`.

This processor does not store opt-out state. Route the events to whatever store your pipeline uses (cache, Redis, SQL, etc.) and filter opted-out recipients before the `+"`twilio_sms`"+` output.`).
		Fields(
			service.NewStringField(fieldURL).
				Description("The exact public URL that Twilio sends the webhook to, including its scheme, host, path and query string. The signature is validated against this value.").
				Example("https://example.com/twilio/webhook"),
			service.NewStringField(fieldAuthToken).
				Description("The Twilio auth token (or SharedKey secret) used to verify the webhook signature. When left empty the environment variable `TWILIO_AUTH_TOKEN` is used.").
				Default("").
				Secret(),
			service.NewStringEnumField(fieldKind, kindAuto, kindStatus, kindInbound).
				Description("The expected webhook type. With `auto` the type is detected from the payload: messages with a body or `OptOutType` are inbound, message status callbacks carry a delivery status such as `delivered` or `undelivered`.").
				Default(kindAuto).
				Advanced(),
		)
}

func init() {
	err := service.RegisterProcessor("twilio_webhook", webhookProcessorSpec(),
		func(conf *service.ParsedConfig, mgr *service.Resources) (service.Processor, error) {
			return newWebhookProcessor(conf, mgr)
		})
	if err != nil {
		panic(err)
	}
}

type webhookProcessor struct {
	validator *twilioclient.RequestValidator
	url       string
	kind      string
	log       *service.Logger
}

func newWebhookProcessor(conf *service.ParsedConfig, mgr *service.Resources) (*webhookProcessor, error) {
	webhookURL, err := conf.FieldString(fieldURL)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(webhookURL)
	if err != nil {
		return nil, fmt.Errorf("parsing '%s': %w", fieldURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("'%s' must be an absolute URL including scheme and host", fieldURL)
	}

	authToken, err := conf.FieldString(fieldAuthToken)
	if err != nil {
		return nil, err
	}
	if authToken == "" {
		authToken = os.Getenv(envAuthToken)
	}
	if authToken == "" {
		return nil, fmt.Errorf("a Twilio auth token is required: set '%s' or the environment variable '%s'", fieldAuthToken, envAuthToken)
	}

	kind, err := conf.FieldString(fieldKind)
	if err != nil {
		return nil, err
	}

	validator := twilioclient.NewRequestValidator(authToken)
	return &webhookProcessor{
		validator: &validator,
		url:       webhookURL,
		kind:      kind,
		log:       mgr.Logger(),
	}, nil
}

func (p *webhookProcessor) Process(ctx context.Context, msg *service.Message) (service.MessageBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	signature := metaString(msg, metaSignature)
	if signature == "" {
		p.log.Warn("Dropping Twilio webhook without a signature header")
		return nil, nil
	}

	body, err := msg.AsBytes()
	if err != nil {
		p.log.Warnf("Dropping Twilio webhook with an unreadable body: %v", err)
		return nil, nil
	}

	algorithm := twilioclient.GetSignatureAlgorithm(metaString(msg, metaSignatureSettings))
	if !p.validator.ValidateBodyWithAlgorithm(p.url, body, signature, algorithm) {
		p.log.Warn("Dropping Twilio webhook with an invalid signature")
		return nil, nil
	}

	params, err := url.ParseQuery(string(body))
	if err != nil {
		p.log.Warnf("Dropping Twilio webhook with an invalid form body: %v", err)
		return nil, nil
	}

	kind := p.kind
	if kind == kindAuto {
		kind = classify(params)
	}
	if kind == "" {
		p.log.Debug("Dropping unrecognised Twilio webhook payload")
		return nil, nil
	}

	var event map[string]any
	switch kind {
	case kindStatus:
		event = statusEvent(params)
	case kindInbound:
		event = inboundEvent(params)
	default:
		p.log.Debugf("Dropping Twilio webhook with unsupported kind '%s'", kind)
		return nil, nil
	}

	msg.SetStructured(event)
	return service.MessageBatch{msg}, nil
}

func (p *webhookProcessor) Close(ctx context.Context) error {
	return nil
}

func metaString(msg *service.Message, key string) string {
	v, ok := msg.MetaGetMut(key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// classify detects the webhook type from its form parameters. Inbound messages
// carry a body (or an OptOutType) and use the `received` status, whereas
// status callbacks report the outbound delivery state.
func classify(params url.Values) string {
	status := webhookStatus(params)
	if params.Get("Body") != "" || params.Get("OptOutType") != "" || status == "received" {
		return kindInbound
	}
	if status != "" {
		return kindStatus
	}
	return ""
}

func webhookStatus(params url.Values) string {
	return firstNonEmpty(params.Get("MessageStatus"), params.Get("SmsStatus"))
}

func webhookMessageSID(params url.Values) string {
	return firstNonEmpty(params.Get("MessageSid"), params.Get("SmsSid"))
}

func baseEvent(params url.Values, eventType string) map[string]any {
	event := map[string]any{
		"type": eventType,
	}
	setNonEmpty(event, "message_sid", webhookMessageSID(params))
	setNonEmpty(event, "account_sid", params.Get("AccountSid"))
	setNonEmpty(event, "from", params.Get("From"))
	setNonEmpty(event, "to", params.Get("To"))
	setNonEmpty(event, "status", webhookStatus(params))
	setNonEmpty(event, "api_version", params.Get("ApiVersion"))
	return event
}

func statusEvent(params url.Values) map[string]any {
	event := baseEvent(params, kindStatus)
	if code := params.Get("ErrorCode"); code != "" && code != "0" {
		if n, err := strconv.Atoi(code); err == nil {
			event["error_code"] = n
		} else {
			event["error_code"] = code
		}
	}
	setNonEmpty(event, "error_message", params.Get("ErrorMessage"))
	return event
}

func inboundEvent(params url.Values) map[string]any {
	event := baseEvent(params, kindInbound)
	setNonEmpty(event, "body", params.Get("Body"))
	setNonEmpty(event, "opt_out_type", params.Get("OptOutType"))
	return event
}

func setNonEmpty(event map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		event[key] = value
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
