package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	configMetrics "github.com/krateoplatformops/finops-prometheus-resource-exporter-azure/pkg/config"
	"github.com/krateoplatformops/finops-prometheus-resource-exporter-azure/pkg/utils"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"

	finopsDataTypes "github.com/krateoplatformops/finops-data-types/api/v1"
)

type recordGaugeCombo struct {
	record []string
	gauge  prometheus.Gauge
}

/*
* Parse the given configuration file and unmarhsal it into the "config.Config" data type.
* The configuration struct is an array of TargetAPI structs to allow the user to define multiple end-points for exporting.
* @param file The path to the configuration file
 */
func ParseConfigFile(file string) (finopsDataTypes.ExporterScraperConfig, error) {
	fileReader, err := os.OpenFile(file, os.O_RDONLY, 0600)
	if err != nil {
		return finopsDataTypes.ExporterScraperConfig{}, err
	}
	defer fileReader.Close()
	data, err := io.ReadAll(fileReader)

	if err != nil {
		return finopsDataTypes.ExporterScraperConfig{}, err
	}

	parse := finopsDataTypes.ExporterScraperConfig{}

	err = yaml.Unmarshal(data, &parse)
	if err != nil {
		return finopsDataTypes.ExporterScraperConfig{}, err
	}

	regex, _ := regexp.Compile("<.*?>")
	newURL := parse.Spec.ExporterConfig.Url
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
	parse.Spec.ExporterConfig.Url = newURL

	return parse, nil
}

/*
* Function to remove the encoding bytes from a file.
* @param file The file to remove the encoding from.
 */
func trapBOM(file []byte) []byte {
	return bytes.Trim(file, "\xef\xbb\xbf")
}

/*
* This function makes the API request to download the FOCUS csv file according to the given configuration.
* @param targetAPI the configuration for the API request
* @return the name of the saved file
 */
func makeAPIRequest(config finopsDataTypes.ExporterScraperConfig) string {
	requestURL := fmt.Sprintf(config.Spec.ExporterConfig.Url)
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	fatal(err)

	if config.Spec.ExporterConfig.RequireAuthentication {
		switch config.Spec.ExporterConfig.AuthenticationMethod {
		case "bearer-token":
			token, err := utils.GetBearerTokenSecret(config)
			if err != nil {
				fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+token)
		case "cert-file":
			data, err := os.ReadFile(config.Spec.ExporterConfig.AdditionalVariables["certFilePath"])
			if err != nil {
				fmt.Println("There has been an error reading the cert-file")
				return ""
			}
			request.Header.Set("Authorization", "Bearer "+string(data))
		}
	}

	res, err := http.DefaultClient.Do(request)
	fatal(err)

	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	fatal(err)

	err = os.WriteFile(fmt.Sprintf("/temp/%s.dat", config.Spec.ExporterConfig.Provider.Name), trapBOM(data), 0644)
	fatal(err)

	return config.Spec.ExporterConfig.Provider.Name
}

/*
* This function reads the given csv file and returns the record list.
* @param fileName the name of the metrics file
* @return csv file as a 2D array of strings
 */
func getRecordsFromFile(fileName string, config finopsDataTypes.ExporterScraperConfig) [][]string {

	byteData, err := os.ReadFile(fmt.Sprintf("/temp/%s.dat", fileName))
	fatal(err)

	data := configMetrics.Metrics{}
	err = json.Unmarshal(byteData, &data)
	if err != nil {
		log.Printf("error decoding response: %v", err)
		if e, ok := err.(*json.SyntaxError); ok {
			log.Printf("syntax error at byte offset %d", e.Offset)
		}
		log.Printf("response: %q", byteData)
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

/*
* This function creates and maintains the prometheus gauges. Periodically, it updates the records csv file and checks if there are new rows to add to the registry.
* @param targetAPI the configuration for the API request
* @param registry the prometheus registry to add the gauges to
* @param prometheusMetrics the array of structs that contain gauges and the record the gauge was created from (to check when there are new records if it has already been created)
 */
func updatedMetrics(config finopsDataTypes.ExporterScraperConfig, useConfig bool, registry *prometheus.Registry, prometheusMetrics map[string]recordGaugeCombo) {
	for {
		fileName := config.Spec.ExporterConfig.Provider.Name
		if useConfig {
			fileName = makeAPIRequest(config)
		}
		records := getRecordsFromFile(fileName, config)
		notFound := true
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
		time.Sleep(time.Duration(config.Spec.ExporterConfig.PollingIntervalHours) * time.Hour)
	}
}

func main() {
	var err error
	config := finopsDataTypes.ExporterScraperConfig{}
	useConfig := true
	if len(os.Args) <= 1 {
		config, err = ParseConfigFile("/config/config.yaml")
		fatal(err)
	} else {
		useConfig = false
		config.Spec.ExporterConfig.Provider.Name = os.Args[1]
		config.Spec.ExporterConfig.PollingIntervalHours = 1
	}

	registry := prometheus.NewRegistry()
	go updatedMetrics(config, useConfig, registry, map[string]recordGaugeCombo{})

	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	http.Handle("/metrics", handler)
	http.ListenAndServe(":2112", nil)
}

func fatal(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}
