module github.com/fizcultor/backend/services/poller-svc

go 1.23.0

require (
	github.com/fizcultor/backend/gen v0.0.0-00010101000000-000000000000
	github.com/fizcultor/backend/pkg v0.0.0
	github.com/sony/gobreaker v1.0.0
	google.golang.org/grpc v1.66.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/caarlos0/env/v11 v11.2.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240604185151-ef581f913117 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/fizcultor/backend/pkg => ../../pkg

replace github.com/fizcultor/backend/gen => ../../gen
