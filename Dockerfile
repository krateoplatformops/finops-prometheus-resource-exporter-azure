FROM scratch
WORKDIR /prometheus-resource-exporter-azure
COPY ./bin ./bin
WORKDIR /temp
WORKDIR /prometheus-resource-exporter-azure/bin
ENTRYPOINT ["./prometheus-resource-exporter-azure"]
