.PHONY: default
default:
	gofmt -s -w .
	go install -v ./...

.PHONY: update-gomod
update-gomod:
	go get -t -v -d -u ./...
	go mod tidy -go=1.21
