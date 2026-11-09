# Bento Twilio Plugin

A custom [Bento](https://github.com/warpstreamlabs/bento) plugin adding a
`twilio_sms` output that sends SMS and MMS messages through the
[Twilio Messages API](https://www.twilio.com/docs/messaging/api/message-resource#create-a-message-resource)
using [`github.com/twilio/twilio-go`](https://github.com/twilio/twilio-go).

## Features

* **SMS and MMS** — send a text body, up to ten media URLs, or both in a single
  message.
* **Content templates** — send a pre-approved [Content API](https://www.twilio.com/docs/content)
  template with variables instead of a plain body.
* **Messaging Services** — send through the sender pool of a Messaging Service,
  with optional link shortening and an RCS fallback sender.
* **Delivery status callbacks** — point Twilio at a status callback URL so
  delivery updates can be routed back into Bento.
* **Go templates everywhere** — `from`, `to`, `body`, `media_urls`,
  `status_callback`, `fallback_from` and `content_variables` are all rendered
  as [Go templates](https://pkg.go.dev/text/template) against the message
  payload.
* **Twilio credentials from config or environment** — `account_sid` and
  `auth_token` can be set explicitly or through `TWILIO_ACCOUNT_SID` and
  `TWILIO_AUTH_TOKEN`.

## Building and Running

Because this is a custom Bento plugin it must be compiled into a custom Bento
binary:

```bash
go build -o bento-twilio .
./bento-twilio -c workflow.yaml
```

## Configuration

```yaml
output:
  twilio_sms:
    # Credentials. When omitted the TWILIO_ACCOUNT_SID and TWILIO_AUTH_TOKEN
    # environment variables are used.
    account_sid: ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
    auth_token: your_auth_token

    # Sender: an E.164 phone number, alphanumeric sender ID, short code or
    # channel address. Required unless messaging_service_sid is set.
    from: "+15551234567"

    # Recipient (required) and bodies, all rendered as Go templates.
    to: '{{ .phone }}'
    body: 'Alert for {{ .site }}: {{ .message }}'

    # Optional MMS media (up to 10), rendered as Go templates. Twilio fetches
    # the media itself, so the URLs must be publicly reachable.
    media_urls:
      - '{{ .chart_url }}'

    # Optional Messaging Service and Content API template.
    messaging_service_sid: ""
    content_sid: ""
    content_variables: ""

    # Delivery and message options.
    status_callback: ""
    fallback_from: ""
    validity_period: 0        # seconds, 1-36000; 0 leaves Twilio's default
    smart_encoded: false
    shorten_urls: false       # requires messaging_service_sid
    send_as_mms: false
    max_price: 0              # USD; 0 leaves Twilio's default

    # Connection options.
    region: ""                # Twilio region, e.g. ie1
    edge: ""                  # Twilio edge location, e.g. dublin
    timeout: 10s              # HTTP request timeout
    max_in_flight: 64
```

At least one of `body`, `media_urls` or `content_sid` must be configured. The
output sends one message per Bento message, to a single recipient: fan out with
a Bento broker or a processor when a payload needs to reach several numbers.

### Template data context

The message payload is used as the template data. When the payload is a JSON
object its fields are available directly:

```json
{"phone": "+15550000000", "site": "example.org", "message": "CPU above 90%"}
```

```yaml
to: '{{ .phone }}'
body: 'Alert for {{ .site }}: {{ .message }}'
```

If the payload is not a JSON object it is exposed as `{{ .value }}` (and, for
non-JSON payloads, `{{ .raw }}`). Missing object keys render as empty strings.

The following helper functions are available in addition to the
[built-in template functions](https://pkg.go.dev/text/template#hdr-Functions):
`toJson`, `base64`, `upper`, `lower`, `trim`, `env`, `join`, `replace`,
`default`, `now` and `formatTime`. For example, a sender can be read from the
environment with:

```yaml
from: '{{ env "TWILIO_FROM_NUMBER" }}'
```

### Phone numbers

Twilio expects phone numbers in [E.164](https://www.twilio.com/docs/glossary/what-e164)
format, for example `+15551234567`. Alphanumeric sender IDs and short codes are
also accepted for `from` where your account and destination country support
them. Numbers are not normalised by the plugin; render them in the correct
format in your pipeline.

### MMS

`media_urls` accepts up to ten URLs. Twilio downloads the media itself, so the
URLs must be publicly reachable and served with a supported content type. The
media size limits (5 MB for images, 500 KB for other types) are enforced by
Twilio.

### Content templates

When `content_sid` is set the message is sent through the
[Content API](https://www.twilio.com/docs/content) instead of a plain body.
Variables are passed as a JSON object rendered from `content_variables`, for
example:

```yaml
content_sid: HXxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
content_variables: '{"1":"{{ .name }}","2":"{{ .code }}"}'
```

The rendered value must be valid JSON; invalid JSON causes the write to fail
before any request is made.

### Delivery status callbacks

Set `status_callback` to a URL that Twilio can reach. Delivery updates can then
be received with a Bento `http_server` input and routed wherever you need them:

```yaml
output:
  twilio_sms:
    from: "+15551234567"
    to: '{{ .phone }}'
    body: "Hello"
    status_callback: https://example.com/twilio/status
```

## Credentials

`account_sid` and `auth_token` are optional and fall back to the
`TWILIO_ACCOUNT_SID` and `TWILIO_AUTH_TOKEN` environment variables. Auth tokens
are marked as secrets in the Bento config and are sent with HTTP basic
authentication; they are never logged.

```bash
export TWILIO_ACCOUNT_SID=ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
export TWILIO_AUTH_TOKEN=your_auth_token
```

## Delivery semantics

The Twilio API responds as soon as it accepts a message for delivery. This does
not guarantee delivery to the handset; use `status_callback` for delivery
updates. Bento treats a rejected request (invalid number, insufficient funds,
queue limit reached, etc.) as a write failure, so the message is retried or
routed according to your error handling configuration.

The Messages API does not support idempotency keys. If a request fails after
Twilio accepted the message (for example a network timeout), a Bento retry can
therefore deliver a duplicate message.

Messages longer than 160 GSM-7 characters (or 70 UCS-2 characters) are split
into multiple segments and billed by Twilio accordingly. Enabling
`smart_encoded` lets Twilio replace Unicode lookalikes with GSM-7 characters to
reduce the number of segments.

## Development

This project uses [Task](https://taskfile.dev) to manage common operations:

```bash
task build      # build ./bin/bento-twilio
task test       # run unit tests
task test-e2e   # run end-to-end tests against a local Twilio API stub
task test-all   # unit + e2e
task vet        # go vet
task fmt        # gofmt
task tidy       # go mod tidy
task lint       # lint the example configs with the built binary
task run        # run the binary, e.g. task run -- -c workflow.yaml
task check      # vet + unit + e2e + config lint (pre-commit gate)
task clean      # remove build artifacts
```

The end-to-end tests are gated behind the `e2e` build tag. They exercise the
real Twilio client against an in-process `httptest` server that speaks the
Messages API, verifying basic authentication, the request path and the encoded
form values. Run them directly with:

```bash
go test -tags e2e -count=1 -run EndToEnd -v ./sms/
```

## License

MIT
