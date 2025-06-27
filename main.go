package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	configmetrics "github.com/krateoplatformops/finops-prometheus-resource-exporter-azure/internal/config"
	"github.com/krateoplatformops/finops-prometheus-resource-exporter-azure/internal/helpers/kube/endpoints"
	"github.com/krateoplatformops/finops-prometheus-resource-exporter-azure/internal/helpers/kube/httpcall"
	"github.com/krateoplatformops/finops-prometheus-resource-exporter-azure/internal/utils"
	"k8s.io/client-go/rest"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	finopsdatatypes "github.com/krateoplatformops/finops-data-types/api/v1"
)

type recordGaugeCombo struct {
	record []string
	gauge  prometheus.Gauge
}

func ParseConfigFile(file string) (finopsdatatypes.ExporterScraperConfig, *httpcall.Endpoint, error) {
	fileReader, err := os.OpenFile(file, os.O_RDONLY, 0600)
	if err != nil {
		return finopsdatatypes.ExporterScraperConfig{}, &httpcall.Endpoint{}, err
	}
	defer fileReader.Close()
	data, err := io.ReadAll(fileReader)

	if err != nil {
		return finopsdatatypes.ExporterScraperConfig{}, &httpcall.Endpoint{}, err
	}

	parse := finopsdatatypes.ExporterScraperConfig{}

	err = yaml.Unmarshal(data, &parse)
	if err != nil {
		return finopsdatatypes.ExporterScraperConfig{}, &httpcall.Endpoint{}, err
	}

	rc, _ := rest.InClusterConfig()

	endpoint, err := endpoints.Resolve(context.Background(), endpoints.ResolveOptions{
		RESTConfig: rc,
		API:        &parse.Spec.ExporterConfig.API,
	})
	if err != nil {
		return finopsdatatypes.ExporterScraperConfig{}, &httpcall.Endpoint{}, err
	}

	// Replace variables in server URL
	endpoint.ServerURL = utils.ReplaceVariables(endpoint.ServerURL, parse.Spec.ExporterConfig.AdditionalVariables)

	// Replace variables in API path
	parse.Spec.ExporterConfig.API.Path = utils.ReplaceVariables(parse.Spec.ExporterConfig.API.Path, parse.Spec.ExporterConfig.AdditionalVariables)

	return parse, endpoint, nil
}

func makeAPIRequest(config finopsdatatypes.ExporterScraperConfig, endpoint *httpcall.Endpoint) []byte {
	res := &http.Response{StatusCode: 500}
	err_call := fmt.Errorf("")

	for ok := true; ok; ok = (err_call != nil || res.StatusCode != 200) {
		httpClient, err := httpcall.HTTPClientForEndpoint(endpoint)
		if err != nil {
			log.Logger.Warn().Err(err).Msg("error while creating HTTP client")
		}

		res, err_call = httpcall.Do(context.TODO(), httpClient, httpcall.Options{
			API:      &config.Spec.ExporterConfig.API,
			Endpoint: endpoint,
		})

		if err_call == nil && res.StatusCode != 200 {
			log.Warn().Msgf("Received status code %d", res.StatusCode)
			bodyData, _ := io.ReadAll(res.Body)
			log.Warn().Msgf("Body %s", string(bodyData))
		} else {
			log.Logger.Warn().Err(err_call).Msg("error occurred while making API call")
		}
		log.Logger.Warn().Msgf("Retrying connection in 5s...")
		time.Sleep(5 * time.Second)

		log.Logger.Info().Msgf("Parsing Endpoint again...")
		rc, _ := rest.InClusterConfig()
		endpoint, err = endpoints.Resolve(context.Background(), endpoints.ResolveOptions{
			RESTConfig: rc,
			API:        &config.Spec.ExporterConfig.API,
		})
		if err != nil {
			continue
		}
		endpoint.ServerURL = utils.ReplaceVariables(endpoint.ServerURL, config.Spec.ExporterConfig.AdditionalVariables)
	}

	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		log.Logger.Warn().Err(err).Msg("an error has occured while reading response body")
	}

	if res.StatusCode != 200 {
		log.Warn().Msgf("Received status code %d", res.StatusCode)
		log.Debug().Msgf("body: %s", data)
	}

	return utils.TrapBOM(data)
}

func getRecordsFromFile(byteData []byte, config finopsdatatypes.ExporterScraperConfig) [][]string {
	data := configmetrics.Metrics{}
	err := json.Unmarshal(byteData, &data)
	if err != nil {
		log.Logger.Error().Err(err).Msg("error decoding response")
		if e, ok := err.(*json.SyntaxError); ok {
			log.Logger.Error().Msgf("syntax error at byte offset %d", e.Offset)
		}
		log.Logger.Info().Msgf("response: %q", byteData)
		log.Logger.Error().Err(err).Msg("error while reading file")
		return nil
	}

	stringCSV := "ResourceId,metricName,timestamp,average,unit\n"
	for _, value := range data.Value {
		for _, timeseries := range value.Timeseries {
			for _, metric := range timeseries.Data {
				stringCSV += config.Spec.ExporterConfig.AdditionalVariables["ResourceId"] + "," + value.Name.Value + "," + metric.Timestamp.Format(time.RFC3339) + "," + metric.Average.AsDec().String() + "," + value.Unit + "\n"
			}
		}
	}

	stringCSV = strings.TrimSuffix(stringCSV, "\n")

	reader := csv.NewReader(strings.NewReader(stringCSV))

	records, err := reader.ReadAll()
	if err != nil {
		log.Logger.Error().Err(err).Msg("error while reading file")
		return nil
	}

	return records
}

func updatedMetrics(registry *prometheus.Registry, prometheusMetrics map[string]recordGaugeCombo) {
	for {
		config, endpoint, err := ParseConfigFile("/config/config.yaml")
		if err != nil {
			log.Logger.Error().Err(err).Msg("error while parsing configuration, trying again in 5s...")
			time.Sleep(5 * time.Second)
			continue
		}
		data := makeAPIRequest(config, endpoint)
		records := getRecordsFromFile(data, config)

		notFound := true
		log.Info().Msgf("Analyzing %d records...", len(records))
		for i, record := range records {
			// Skip header line
			if i == 0 {
				continue
			}

			notFound = true
			if _, ok := prometheusMetrics[strings.Join(record, " ")]; ok {
				metricValue, err := strconv.ParseFloat(record[3], 64)
				if err != nil {
					log.Logger.Warn().Err(err).Msgf("skipping this record for this iteration, error while parsing metric value: %s", record[3])
					continue
				}
				prometheusMetrics[strings.Join(record, " ")].gauge.Set(metricValue)
				notFound = false
			}

			if notFound {
				labels := prometheus.Labels{}
				for j, value := range record {
					labels[records[0][j]] = value
				}
				newMetricsRow := promauto.NewGauge(prometheus.GaugeOpts{
					Name:        strings.ReplaceAll(strings.ToLower(labels[records[0][1]]), " ", "_"),
					ConstLabels: labels,
				})
				metricValue, err := strconv.ParseFloat(records[i][3], 64)
				if err != nil {
					log.Logger.Warn().Err(err).Msgf("skipping this record for this iteration, error while parsing metric value: %s", records[i][3])
					continue
				}

				newMetricsRow.Set(metricValue)
				prometheusMetrics[strings.Join(record, " ")] = recordGaugeCombo{record: record, gauge: newMetricsRow}
				registry.MustRegister(newMetricsRow)
			}
		}
		time.Sleep(config.Spec.ExporterConfig.PollingInterval.Duration)
	}
}

func main() {
	registry := prometheus.NewRegistry()
	go updatedMetrics(registry, map[string]recordGaugeCombo{})

	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	http.Handle("/metrics", handler)
	http.ListenAndServe(":2112", nil)
}
