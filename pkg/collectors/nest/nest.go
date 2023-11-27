package nest

import (
	"context"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/go-kit/kit/log"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	errNon200Response      = errors.New("nest API responded with non-200 code")
	errFailedParsingURL    = errors.New("failed parsing OpenWeatherMap API URL")
	errFailedUnmarshalling = errors.New("failed unmarshalling Nest API response body")
	errFailedRequest       = errors.New("failed Nest API request")
	errFailedReadingBody   = errors.New("failed reading Nest API response body")
)

// Thermostat stores thermostat data received from Nest API.
type Thermostat struct {
	ID           string
	Room         string
	Label        string
	Online       bool
	AmbientTemp  float64
	SetpointTemp float64
	Humidity     float64
	Status       string
}

// Config provides the configuration necessary to create the Collector.
type Config struct {
	Logger            log.Logger
	Timeout           int
	APIURL            string
	OAuthClientID     string
	OAuthClientSecret string
	RefreshToken      string
	ProjectID         string
	OAuthToken        *oauth2.Token
}

// Collector implements the Collector interface, collecting thermostats data from Nest API.
type Collector struct {
	client  *http.Client
	url     string
	logger  log.Logger
	metrics *Metrics
}

// Metrics contains the metrics collected by the Collector.
type Metrics struct {
	up           *prometheus.Desc
	online       *prometheus.Desc
	ambientTemp  *prometheus.Desc
	setpointTemp *prometheus.Desc
	humidity     *prometheus.Desc
	heating      *prometheus.Desc
}

// New creates a Collector using the given Config.
func New(cfg Config) (*Collector, error) {
	if _, err := url.ParseRequestURI(cfg.APIURL); err != nil {
		return nil, errors.Wrap(errFailedParsingURL, err.Error())
	}

	oauthConfig := &oauth2.Config{
		ClientID:     cfg.OAuthClientID,
		ClientSecret: cfg.OAuthClientSecret,
		Scopes:       []string{"https://www.googleapis.com/auth/sdm.service"},
		Endpoint:     endpoints.Google,
	}

	// If token is not provided we create a new one using RefreshToken. Using this token, the client will automatically
	// get, and refresh, a valid access token for the API.
	if cfg.OAuthToken == nil {
		cfg.OAuthToken = &oauth2.Token{
			TokenType:    "Bearer",
			RefreshToken: cfg.RefreshToken,
		}
	}

	client := oauthConfig.Client(context.Background(), cfg.OAuthToken)
	client.Timeout = time.Duration(cfg.Timeout) * time.Millisecond

	collector := &Collector{
		client:  client,
		url:     strings.TrimRight(cfg.APIURL, "/") + "/enterprises/" + cfg.ProjectID + "/devices/",
		logger:  cfg.Logger,
		metrics: buildMetrics(),
	}

	return collector, nil
}

func buildMetrics() *Metrics {
	var nestLabels = []string{"id", "room", "label"}
	return &Metrics{
		up:           prometheus.NewDesc(strings.Join([]string{"nest", "up"}, "_"), "Was talking to Nest API successful.", nil, nil),
		online:       prometheus.NewDesc(strings.Join([]string{"nest", "online"}, "_"), "Is the thermostat online.", nestLabels, nil),
		ambientTemp:  prometheus.NewDesc(strings.Join([]string{"nest", "ambient", "temperature", "celsius"}, "_"), "Inside temperature.", nestLabels, nil),
		setpointTemp: prometheus.NewDesc(strings.Join([]string{"nest", "setpoint", "temperature", "celsius"}, "_"), "Setpoint temperature.", nestLabels, nil),
		humidity:     prometheus.NewDesc(strings.Join([]string{"nest", "humidity", "percent"}, "_"), "Inside humidity.", nestLabels, nil),
		heating:      prometheus.NewDesc(strings.Join([]string{"nest", "heating"}, "_"), "Is thermostat heating.", nestLabels, nil),
	}
}

// Describe implements the prometheus.Describe interface.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.metrics.up
	ch <- c.metrics.online
	ch <- c.metrics.ambientTemp
	ch <- c.metrics.setpointTemp
	ch <- c.metrics.humidity
	ch <- c.metrics.heating
}

// Collect implements the prometheus.Collector interface.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	thermostats, err := c.getNestReadings()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.metrics.up, prometheus.GaugeValue, 0)
		c.logger.Log("level", "error", "message", "Failed collecting Nest data", "stack", errors.WithStack(err))
		return
	}

	c.logger.Log("level", "debug", "message", "Successfully collected Nest data")

	ch <- prometheus.MustNewConstMetric(c.metrics.up, prometheus.GaugeValue, 1)

	for _, therm := range thermostats {
		labels := []string{therm.ID, therm.Room, strings.Replace(therm.Label, " ", "-", -1)}

		ch <- prometheus.MustNewConstMetric(c.metrics.online, prometheus.GaugeValue, b2f(therm.Online), labels...)

		// Emit the rest of the metrics only if the thermostat is ONLINE.
		// When the thermostat is offline, we do not know the current values
		// of these metrics.
		if !therm.Online {
			continue
		}

		ch <- prometheus.MustNewConstMetric(c.metrics.ambientTemp, prometheus.GaugeValue, therm.AmbientTemp, labels...)
		if !math.IsNaN(therm.SetpointTemp) {
			ch <- prometheus.MustNewConstMetric(c.metrics.setpointTemp, prometheus.GaugeValue, therm.SetpointTemp, labels...)
		}
		ch <- prometheus.MustNewConstMetric(c.metrics.humidity, prometheus.GaugeValue, therm.Humidity, labels...)
		ch <- prometheus.MustNewConstMetric(c.metrics.heating, prometheus.GaugeValue, b2f(therm.Status == "HEATING"), labels...)
	}
}

func (c *Collector) getNestReadings() (thermostats []*Thermostat, err error) {
	res, err := c.client.Get(c.url)
	if err != nil {
		return nil, errors.Wrap(errFailedRequest, err.Error())
	}

	if res.StatusCode != 200 {
		return nil, errors.Wrap(errNon200Response, fmt.Sprintf("code: %d", res.StatusCode))
	}

	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, errors.Wrap(errFailedReadingBody, err.Error())
	}

	// Iterate over the array of "devices" returned from the API and unmarshall them into Thermostat objects.
	gjson.Get(string(body), "devices").ForEach(func(_, device gjson.Result) bool {
		// Skip to next device if the current one is not a thermostat.
		if device.Get("type").String() != "sdm.devices.types.THERMOSTAT" {
			return true
		}

		heatSetPoint := math.NaN()
		// The set point for heating might not be present, for example, when the
		// thermostat's mode is OFF or COOL.
		if v := device.Get("traits.sdm\\.devices\\.traits\\.ThermostatTemperatureSetpoint.heatCelsius"); v.Exists() {
			heatSetPoint = v.Float()
		}

		room := ""
		// We determine the room from the list of parent relationships of this
		// thermostat. We're explicitly looking for relationships of type
		// "room" because I didn't have a way to test how other relationship
		// types look like.
		//
		// Even though this is an array of relationships, a Nest thermostat
		// can belong only to a single room.
		for _, parent := range device.Get("parentRelations").Array() {
			if strings.Contains(parent.Get("parent").String(), "/rooms/") {
				room = parent.Get("displayName").String()
				break
			}
		}

		thermostat := Thermostat{
			ID:           device.Get("name").String(),
			Room:         room,
			Label:        device.Get("traits.sdm\\.devices\\.traits\\.Info.customName").String(),
			Online:       device.Get("traits.sdm\\.devices\\.traits\\.Connectivity.status").String() == "ONLINE",
			AmbientTemp:  device.Get("traits.sdm\\.devices\\.traits\\.Temperature.ambientTemperatureCelsius").Float(),
			SetpointTemp: heatSetPoint,
			Humidity:     device.Get("traits.sdm\\.devices\\.traits\\.Humidity.ambientHumidityPercent").Float(),
			Status:       device.Get("traits.sdm\\.devices\\.traits\\.ThermostatHvac.status").String(),
		}

		thermostats = append(thermostats, &thermostat)
		return true
	})

	if len(thermostats) == 0 {
		return nil, errors.Wrap(errFailedUnmarshalling, "no valid thermostats in devices list")
	}

	return thermostats, nil
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
