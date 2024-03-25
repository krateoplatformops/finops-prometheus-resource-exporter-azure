# finops-prometheus-resource-exporter-azure
This repository is part of a wider exporting architecture for the FinOps Cost and Usage Specification (FOCUS). This component is tasked with exporting in the Prometheus format the metrics of "Virtual Machine" resources found in a FOCUS report. The metrics are obtained through an API call to a service provider metrics server. The exporter runs on the port 2112. 

## Dependencies
--

## Configuration
To start the exporting process, see the "config-sample.yaml" file.

This container is automatically started by the operator-exporter.

## Installation
To build the executable: 
```
make build REPO=<your-registry-here>
```

To build and push the Docker images:
```
make container REPO=<your-registry-here>
```

## Tested platforms
 - Azure