package nestapp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/go-kit/kit/log"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	errNon200Response      = errors.New("nest app API responded with non-200 code")
	errFailedUnmarshalling = errors.New("failed unmarshalling Nest app API response body")
	errFailedRequest       = errors.New("failed Nest app API request")
	errFailedReadingBody   = errors.New("failed reading Nest app API response body")
)

// Config provides the configuration necessary to create the Collector.
type Config struct {
	Logger      log.Logger
	Timeout     int
	AuthURL     string
	AuthCookies string
}

// Collector implements the Collector interface, collecting thermostats data from Nest app API.
type Collector struct {
	config                Config
	client                *http.Client
	accessToken           string
	accessTokenValidUntil time.Time
	userId                string
	logger                log.Logger
	metrics               *Metrics
}

// Metrics contains the metrics collected by the Collector.
type Metrics struct {
	up           *prometheus.Desc
	temp         *prometheus.Desc
	batteryLevel *prometheus.Desc
	outsideTemp  *prometheus.Desc
}

// New creates a Collector using the given Config.
func New(cfg Config) (*Collector, error) {
	client := &http.Client{}
	client.Timeout = time.Duration(cfg.Timeout) * time.Millisecond

	collector := &Collector{
		config:  cfg,
		client:  client,
		logger:  cfg.Logger,
		metrics: buildMetrics(),
	}

	ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Timeout)*time.Millisecond)
	defer cancel()
	err := collector.reauth(ctxTimeout)
	if err != nil {
		return nil, fmt.Errorf("Failed to authenticate to Nest API: %w", err)
	}

	return collector, nil
}

func (c *Collector) reauth(ctx context.Context) error {
	googleAccessToken, err := c.getGoogleAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("Failed to get Google Account access token: %w", err)
	}
	jwt, userId, jwtExpirationInstant, err := c.getNestJwt(ctx, googleAccessToken)
	if err != nil {
		return fmt.Errorf("Failed to get Nest access token: %w", err)
	}

	c.accessToken = jwt
	c.userId = userId
	c.accessTokenValidUntil = jwtExpirationInstant
	c.logger.Log("level", "debug", "message", fmt.Sprintf("Obtained new access token for API used by the Nest app. Valid until %s", jwtExpirationInstant.String()))
	return nil
}

func (c *Collector) getGoogleAccessToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.config.AuthURL, nil)
	if err != nil {
		return "", fmt.Errorf("Failed to create GET request: %w", err)
	}
	req.Header.Set("Cookie", c.config.AuthCookies)
	req.Header.Set("X-Requested-With", "XmlHttpRequest")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Failed to read response body: %w", err)
	}

	bodyString := string(body)
	if errorId := gjson.Get(bodyString, "error"); errorId.Exists() {
		return "", fmt.Errorf("%s: %s", errorId.String(), gjson.Get(bodyString, "error_description").String())
	}

	accessToken := gjson.Get(bodyString, "access_token").String()
	if accessToken == "" {
		return "", fmt.Errorf("No access token in the response")
	}

	return accessToken, nil
}

func (c *Collector) getNestJwt(ctx context.Context, googleAccessToken string) (string, string, time.Time, error) {
	requestBody := fmt.Sprintf(`{"embed_google_oauth_access_token": true,
"expire_after": "3600s",
"google_oauth_access_token": "%s",
"policy_id": "authproxy-oauth-policy"
}`, googleAccessToken)
	req, err := http.NewRequestWithContext(ctx,
		"POST",
		"https://nestauthproxyservice-pa.googleapis.com/v1/issue_jwt",
		bytes.NewReader([]byte(requestBody)))
	if err != nil {
		return "", "", time.Now(), fmt.Errorf("Failed to create POST request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", googleAccessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XmlHttpRequest")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", time.Now(), fmt.Errorf("Request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", time.Now(), fmt.Errorf("Failed to read response body: %w", err)
	}

	bodyString := string(body)
	if errorId := gjson.Get(bodyString, "error"); errorId.Exists() {
		return "", "", time.Now(), fmt.Errorf("%s: %s", errorId.String(), gjson.Get(bodyString, "error_description").String())
	}

	jwt := gjson.Get(bodyString, "jwt").String()
	if jwt == "" {
		return "", "", time.Now(), fmt.Errorf("No JWT in the response: %s", bodyString)
	}

	userId := gjson.Get(bodyString, "claims").Get("subject").Get("nestId").Get("id").String()
	if userId == "" {
		return "", "", time.Now(), fmt.Errorf("No user ID (claims.subject.nestId.id) in the response: %s", bodyString)
	}

	expirationStr := gjson.Get(bodyString, "claims").Get("expirationTime").String()
	if expirationStr == "" {
		return "", "", time.Now(), fmt.Errorf("No access token expiration time (claims.expirationTime) in the response: %s", bodyString)
	}

	expirationInstant, err := time.Parse(time.RFC3339, expirationStr)
	if err != nil {
		return "", "", time.Now(), fmt.Errorf("Failed to parse access token expiration time (%s): %w", expirationStr, err)
	}

	return jwt, userId, expirationInstant, nil
}

func buildMetrics() *Metrics {
	var sensorLabels = []string{"serial", "structure", "where"}
	var structureLabels = []string{"id", "name"}
	return &Metrics{
		up:           prometheus.NewDesc("nest_app_up", "Was talking to Nest app API successful.", nil, nil),
		temp:         prometheus.NewDesc("nest_temp_sensor_temperature_celsius", "Temperature Sensor temperature", sensorLabels, nil),
		batteryLevel: prometheus.NewDesc("nest_temp_sensor_battery", "Temperature Sensor battery level (0-100)", sensorLabels, nil),
		outsideTemp:  prometheus.NewDesc("nest_outside_temperature_celsius", "Outside temperature", structureLabels, nil),
	}
}

// Describe implements the prometheus.Describe interface.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.metrics.up
	ch <- c.metrics.temp
	ch <- c.metrics.batteryLevel
	ch <- c.metrics.outsideTemp
}

// Collect implements the prometheus.Collector interface.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	readings, err := c.getReadings()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.metrics.up, prometheus.GaugeValue, 0)
		c.logger.Log("level", "error", "message", "Failed collecting Nest app data", "stack", errors.WithStack(err))
		return
	}

	c.logger.Log("level", "debug", "message", "Successfully collected Nest app data")

	ch <- prometheus.MustNewConstMetric(c.metrics.up, prometheus.GaugeValue, 1)

	for _, sensor := range readings.sensors {
		labels := []string{sensor.SerialNumber, sensor.StructureName, sensor.WhereName}

		ch <- prometheus.MustNewConstMetric(c.metrics.temp, prometheus.GaugeValue, sensor.Temperature, labels...)
		ch <- prometheus.MustNewConstMetric(c.metrics.batteryLevel, prometheus.GaugeValue, float64(sensor.BatteryLevel), labels...)
	}

	for _, structure := range readings.structures {
		labels := []string{structure.Id, structure.Name}
		if !math.IsNaN(structure.OutsideTemperature) {
			ch <- prometheus.MustNewConstMetric(c.metrics.outsideTemp, prometheus.GaugeValue, structure.OutsideTemperature, labels...)
		}
	}
}

type NestTemperatureSensor struct {
	SerialNumber  string
	StructureName string
	WhereName     string
	LastUpdatedAt time.Time
	Temperature   float64
	BatteryLevel  int64
}

type Structure struct {
	Id                 string
	Name               string
	WhereNames         map[string]string
	OutsideTemperature float64
}

type Readings struct {
	structures []Structure
	sensors    []NestTemperatureSensor
}

func (c *Collector) getReadings() (readings *Readings, err error) {
	// Try to re-authenticate and obtain a new access token if the current one is about to expire
	// or has expired.
	if !time.Now().Before(c.accessTokenValidUntil.Add(-2 * time.Minute)) {
		// Access token about to expire or already expired
		ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Duration(c.config.Timeout)*time.Millisecond)
		defer cancel()
		err := c.reauth(ctxTimeout)
		if err != nil {
			// Error out only if the current token expired.
			if !time.Now().Before(c.accessTokenValidUntil) {
				return nil, fmt.Errorf("Failed to re-authenticate to Nest API: %w", err)
			}
		}
	}
	// We probably have a valid accecss token -- use it

	// Ask the Nest App API for the information on structures, locations, and the Temperature
	// Sensors ("kryptonite").
	reqBody := "{\"known_bucket_types\":[\"structure\",\"where\",\"kryptonite\"],\"known_bucket_versions\":[]}"
	req, err := http.NewRequest("POST",
		fmt.Sprintf("https://home.nest.com/api/0.1/user/%s/app_launch", c.userId),
		bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", c.accessToken))
	req.Header.Set("Cookie", fmt.Sprintf("G_ENABLED_IDPS=google; eu_cookie_accepted=1; viewer-volume=0.5; cztoken=%s; user_token=%s", c.accessToken, c.accessToken))
	req.Header.Set("X-nl-user-id", c.userId)
	req.Header.Set("X-nl-protocol-version", "1")

	res, err := c.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(errFailedRequest, err.Error())
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return nil, errors.Wrap(errNon200Response, fmt.Sprintf("code: %d", res.StatusCode))
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, errors.Wrap(errFailedReadingBody, err.Error())
	}

	// Populate our "structures" map from the returned "structure" and "where" objects.
	structures := make(map[string]Structure)
	gjson.Get(string(body), "updated_buckets").ForEach(func(_, obj gjson.Result) bool {
		objKey := obj.Get("object_key").String()
		if strings.HasPrefix(objKey, "structure.") {
			if v := obj.Get("value"); v.Exists() {
				id := strings.TrimPrefix(objKey, "structure.")
				structures[id] = Structure{
					Id:                 id,
					Name:               v.Get("name").String(),
					WhereNames:         make(map[string]string),
					OutsideTemperature: math.NaN(),
				}
			}
		}
		return true
	})
	gjson.Get(string(body), "updated_buckets").ForEach(func(_, obj gjson.Result) bool {
		objKey := obj.Get("object_key").String()
		if strings.HasPrefix(objKey, "where.") {
			if v := obj.Get("value"); v.Exists() {
				id := strings.TrimPrefix(objKey, "where.")
				structure, found := structures[id]
				if found {
					v.Get("wheres").ForEach(func(_, obj gjson.Result) bool {
						if whereId := obj.Get("where_id"); whereId.Exists() {
							structure.WhereNames[whereId.String()] = obj.Get("name").String()
						}
						return true
					})
				}
			}
		}
		return true
	})

	// Populate our "sensors" list from the returned "kryptonite" objects.
	sensors := make([]NestTemperatureSensor, 0)
	gjson.Get(string(body), "updated_buckets").ForEach(func(_, obj gjson.Result) bool {
		objKey := obj.Get("object_key").String()
		if strings.HasPrefix(objKey, "kryptonite.") {
			if v := obj.Get("value"); v.Exists() {
				structure := structures[v.Get("structure_id").String()]
				sensors = append(sensors, NestTemperatureSensor{
					SerialNumber:  v.Get("serial_number").String(),
					LastUpdatedAt: time.Unix(v.Get("last_updated_at").Int(), 0),
					Temperature:   v.Get("current_temperature").Float(),
					BatteryLevel:  v.Get("battery_level").Int(),
					StructureName: structure.Name,
					WhereName:     structure.WhereNames[v.Get("where_id").String()],
				})
			}
		}
		return true
	})

	// Populate the outside temperature for each structure from the returned weather info.
	if weatherForStructures := gjson.Get(string(body), "weather_for_structures"); weatherForStructures.Exists() {
		weatherForStructures.ForEach(func(key, value gjson.Result) bool {
			if strings.HasPrefix(key.String(), "structure.") {
				structureId := strings.TrimPrefix(key.String(), "structure.")
				structure, found := structures[structureId]
				if found {
					if current := value.Get("current"); current.Exists() {
						if tempC := current.Get("temp_c"); tempC.Exists() {
							structure.OutsideTemperature = tempC.Float()
							structures[structureId] = structure
						}
					}
				}
			}
			return true
		})
	}

	structuresList := make([]Structure, 0)
	for _, structure := range structures {
		structuresList = append(structuresList, structure)
	}
	return &Readings{
		structures: structuresList,
		sensors:    sensors,
	}, nil
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
