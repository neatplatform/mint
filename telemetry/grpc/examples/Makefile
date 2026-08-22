server_app ?= server
client_app ?= client


.PHONY: tools
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

.PHONY: proto
proto:
	@ protoc \
	    --proto_path=proto \
	    --go_out=proto --go_opt=paths=source_relative \
	    --go-grpc_out=proto --go-grpc_opt=paths=source_relative \
	    proto/file/v1/file.proto

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
