module github.com/soundmap/soundmap/tests/e2e

go 1.22

require (
	github.com/soundmap/soundmap v0.0.0
	google.golang.org/grpc v1.64.0
)

require (
	golang.org/x/net v0.24.0 // indirect
	golang.org/x/sys v0.19.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240415180920-8c6c420018be // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

replace github.com/soundmap/soundmap => ../../
