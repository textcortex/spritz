module spritz.sh/integrations/slack-gateway

go 1.25.0

require (
	github.com/gorilla/websocket v1.5.3
	spritz.sh/acptext v0.0.0-00010101000000-000000000000
)

replace spritz.sh/acptext => ../acptext
