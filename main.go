package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	regex, _ := regexp.Compile("<.*?>")
	rc, _ := rest.InClusterConfig()

	endpoint, err := endpoints.Resolve(context.Background(), endpoints.ResolveOptions{
		RESTConfig: rc,
		API:        &parse.Spec.ExporterConfig.API,
	})
	if err != nil {
		return finopsdatatypes.ExporterScraperConfig{}, &httpcall.Endpoint{}, err
	}

	newURL := endpoint.ServerURL
	toReplaceRange := regex.FindStringIndex(newURL)
	for toReplaceRange != nil {
		// Use the indexes of the match of the regex to replace the URL with the value of the additional variable from the config file
		// The replacement has +1/-1 on the indexes to remove the < and > from the string to use as key in the config map
		// If the replacement contains ONLY uppercase letters, it is taken from environment variables
		varToReplace := parse.Spec.ExporterConfig.AdditionalVariables[newURL[toReplaceRange[0]+1:toReplaceRange[1]-1]]
		if varToReplace == strings.ToUpper(varToReplace) {
			varToReplace = os.Getenv(varToReplace)
		}
		newURL = strings.Replace(newURL, newURL[toReplaceRange[0]:toReplaceRange[1]], varToReplace, -1)
		toReplaceRange = regex.FindStringIndex(newURL)
	}
	endpoint.ServerURL = newURL

	newURL = parse.Spec.ExporterConfig.API.Path
	toReplaceRange = regex.FindStringIndex(newURL)
	for toReplaceRange != nil {
		// Use the indexes of the match of the regex to replace the URL with the value of the additional variable from the config file
		// The replacement has +1/-1 on the indexes to remove the < and > from the string to use as key in the config map
		// If the replacement contains ONLY uppercase letters, it is taken from environment variables
		varToReplace := parse.Spec.ExporterConfig.AdditionalVariables[newURL[toReplaceRange[0]+1:toReplaceRange[1]-1]]
		if varToReplace == strings.ToUpper(varToReplace) {
			varToReplace = os.Getenv(varToReplace)
		}
		newURL = strings.Replace(newURL, newURL[toReplaceRange[0]:toReplaceRange[1]], varToReplace, -1)
		toReplaceRange = regex.FindStringIndex(newURL)
	}
	parse.Spec.ExporterConfig.API.Path = newURL

	return parse, endpoint, nil
}

func makeAPIRequest(config finopsdatatypes.ExporterScraperConfig, endpoint *httpcall.Endpoint, fileName string) {
	log.Logger.Info().Msgf("Request URL: %s", endpoint.ServerURL)

	httpClient, err := httpcall.HTTPClientForEndpoint(endpoint)
	if err != nil {
		fatal(err)
	}

	res, err := httpcall.Do(context.TODO(), httpClient, httpcall.Options{
		API:      &config.Spec.ExporterConfig.API,
		Endpoint: endpoint,
	})
	for err != nil {
		fatal(err)
		log.Logger.Warn().Msgf("Retrying connection in 5s...")
		time.Sleep(5 * time.Second)
		res, err = httpcall.Do(context.TODO(), httpClient, httpcall.Options{
			API:      &config.Spec.ExporterConfig.API,
			Endpoint: endpoint,
		})
	}

	defer res.Body.Close()

	if res.StatusCode == 202 {
		res.Body.Close()
		secondsToSleep, _ := strconv.ParseInt(res.Header.Get("Retry-after"), 10, 64)
		time.Sleep(time.Duration(secondsToSleep) * time.Second)
		res, err = http.Get(res.Header.Get("Location"))
		fatal(err)

		var data map[string]string
		err = json.NewDecoder(res.Body).Decode(&data)
		fatal(err)
		res.Body.Close()
		res, err = http.Get(data["downloadUrl"])
		fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	fatal(err)

	if res.StatusCode != 200 {
		log.Warn().Msgf("Received status code %d", res.StatusCode)
		log.Debug().Msgf("body: %s", data)
	}

	err = os.WriteFile(fmt.Sprintf("/temp/%s.dat", fileName), utils.TrapBOM(data), 0644)
	fatal(err)

}

func getRecordsFromFile(fileName string, config finopsdatatypes.ExporterScraperConfig) [][]string {

	byteData, err := os.ReadFile(fmt.Sprintf("/temp/%s.dat", fileName))
	fatal(err)

	data := configmetrics.Metrics{}
	err = json.Unmarshal(byteData, &data)
	if err != nil {
		log.Logger.Error().Err(err).Msg("error decoding response")
		if e, ok := err.(*json.SyntaxError); ok {
			log.Logger.Error().Msgf("syntax error at byte offset %d", e.Offset)
		}
		log.Logger.Info().Msgf("response: %q", byteData)
		fatal(err)
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
	fatal(err)

	return records
}

func updatedMetrics(config finopsdatatypes.ExporterScraperConfig, endpoint *httpcall.Endpoint, useConfig bool, registry *prometheus.Registry, prometheusMetrics map[string]recordGaugeCombo) {
	for {
		fileName := ""
		if config.Spec.ExporterConfig.Provider.Name != "" {
			fileName = config.Spec.ExporterConfig.Provider.Name
		} else {
			fileName = "download"
		}
		if useConfig {
			makeAPIRequest(config, endpoint, fileName)
		}
		records := getRecordsFromFile(fileName, config)

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
				fatal(err)
				prometheusMetrics[strings.Join(record, " ")].gauge.Set(metricValue)
				notFound = false
			}

			if notFound {
				labels := prometheus.Labels{}
				for j, value := range record {
					labels[records[0][j]] = value
				}
				newMetricsRow := promauto.NewGauge(prometheus.GaugeOpts{
					Name:        fmt.Sprintf("usage_%s_%d", strings.ReplaceAll(config.Spec.ExporterConfig.Provider.Name, "-", "_"), i),
					ConstLabels: labels,
				})
				metricValue, err := strconv.ParseFloat(records[i][3], 64)
				fatal(err)

				newMetricsRow.Set(metricValue)
				prometheusMetrics[strings.Join(record, " ")] = recordGaugeCombo{record: record, gauge: newMetricsRow}
				registry.MustRegister(newMetricsRow)
			}
		}
		time.Sleep(config.Spec.ExporterConfig.PollingInterval.Duration)
	}
}

func main() {
	var err error
	config := finopsdatatypes.ExporterScraperConfig{}
	endpoint := &httpcall.Endpoint{}
	useConfig := true
	if len(os.Args) <= 1 {
		config, endpoint, err = ParseConfigFile("/config/config.yaml")
		fatal(err)
	} else {
		useConfig = false
		config.Spec.ExporterConfig.Provider.Name = os.Args[1]
		config.Spec.ExporterConfig.PollingInterval = metav1.Duration{Duration: 1 * time.Hour}
	}

	registry := prometheus.NewRegistry()
	go updatedMetrics(config, endpoint, useConfig, registry, map[string]recordGaugeCombo{})

	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	http.Handle("/metrics", handler)
	http.ListenAndServe(":2112", nil)
}

func fatal(err error) {
	if err != nil {
		log.Logger.Warn().Err(err).Msg("an error has occured, continuing...")
	}
}
