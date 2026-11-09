package main

import (
	"context"

	"github.com/warpstreamlabs/bento/public/service"

	// Import standard components.
	_ "github.com/warpstreamlabs/bento/public/components/all"

	// Import the custom plugins.
	_ "github.com/akhenakh/bento-twilio/sms"
	_ "github.com/akhenakh/bento-twilio/webhook"
)

func main() {
	service.RunCLI(context.Background())
}
