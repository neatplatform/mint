server_app ?= server
client_app ?= client


.PHONY: server
server:
	@ cd $(server_app); \
	  go build -o $(server_app); \
	  trap 'rm -f $(server_app)' EXIT INT TERM; \
	  ./$(server_app) | jq

.PHONY: client
client:
	@ cd $(client_app); \
	  go build -o $(client_app); \
	  trap 'rm -f $(client_app)' EXIT INT TERM; \
	  ./$(client_app) | jq
