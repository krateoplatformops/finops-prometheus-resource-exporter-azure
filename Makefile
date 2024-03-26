ARCH?=amd64
REPO?=#your repository here 
VERSION?=0.1

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -o ./bin/prometheus-resource-exporter-azure main.go

container:
	docker build -t $(REPO)prometheus-resource-exporter-azure:$(VERSION) .
	docker push $(REPO)prometheus-resource-exporter-azure:$(VERSION)
