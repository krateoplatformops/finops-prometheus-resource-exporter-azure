ARCH?=amd64
REPO?=#your repository here 
VERSION?=0.1

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -o ./bin/prometheus-resource-exporter-azure main.go

container:
	docker build -t $(REPO)finops-prometheus-resource-exporter-azure:$(VERSION) .
	docker push $(REPO)finops-prometheus-resource-exporter-azure:$(VERSION)

container-multi:
	docker buildx build --tag $(REPO)finops-prometheus-resource-exporter-azure:$(VERSION) --push --platform linux/amd64,linux/arm64 .