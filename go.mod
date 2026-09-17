module github.com/dolmen-go/openapi-preprocessor

go 1.26.0

require (
	github.com/dolmen-go/jsonptr v0.0.0-20260529085001-d6b11e72da90
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826
	go.yaml.in/yaml/v3 v3.0.5
)

require (
	github.com/ikawaha/kagome-dict v1.1.7 // indirect
	github.com/ikawaha/kagome-dict/ipa v1.2.6 // indirect
	github.com/ikawaha/kagome/v2 v2.11.0 // indirect
	github.com/lufia/godoc2man v0.1.1-0.20260901145734-bee67d4b8763 // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

tool github.com/lufia/godoc2man

replace github.com/lufia/godoc2man => github.com/dolmen-go/lufia-godoc2man.fork v0.0.0-20260913123340-79a88c315b3f
