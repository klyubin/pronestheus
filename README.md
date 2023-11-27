# ProNestheus

This is a fork of [grdl/pronestheus](https://github.com/grdl/pronestheus) where the maintainers haven't been responding to pull requests since Dec 2022. Regardless, please appreciate the maintainers of `grdl/pronestheus` for what they have built for us.

![build](https://github.com/klyubin/pronestheus/workflows/build/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/klyubin/pronestheus)](https://goreportcard.com/report/github.com/klyubin/pronestheus)

A Prometheus exporter for the [Nest Learning Thermostat](https://nest.com/). Exposes metrics about your thermostats, temperature sensors, and the weather in your current location.

Works with the new [Google Smart Device Management API](https://developers.google.com/nest/device-access)!

![dashboard](docs/dashboard.png)

## Installation

### Build from source

All you need is the Golang toolchain:
```
cd cmd/pronestheus
go build .
```

You can also cross-compile for a different platform this way. For example, here's how to build for
an ARM64 Linux target, such as a Raspberry Pi:
```
cd cmd/pronestheus
GOOS=linux GOARCH=arm64 go build .
```

### Binary download

Grab the Linux, macOS or Windows executable from the [latest release](https://github.com/klyubin/pronestheus/releases/latest).

### Docker image

```
docker run -p 9777:9777 -e "PRONESTHEUS_NEST_TOKEN=xxx" klyubin/pronestheus
```

### Helm chart

Helm chart is available in `deployments/helm`.

### "One-click" installation with Docker Compose

Update necessary variables in `deployments/docker-compose/.env` file. Then run:
```
cd deployments/docker-compose
docker-compose up
```

This will start docker containers with Prometheus, Grafana and ProNestheus exporter automatically configured. Visit http://localhost:3000 to open Grafana dashboard with your thermostat metrics.


### Usage and configuration

All configuration flags can be passed as environment variables with `PRONESTHEUS_` prefix. Eg, `PRONESTHEUS_NEST_AUTH`.

```
usage: pronestheus [<flags>]

Flags:
  -h, --help                     Show context-sensitive help (also try --help-long and --help-man).
      --listen-addr=":9777"      Address on which to expose metrics and web interface.
      --metrics-path="/metrics"  Path under which to expose metrics.
      --scrape-timeout=5000      Time to wait for remote APIs to response, in milliseconds.
      --nest-url="https://smartdevicemanagement.googleapis.com/v1/"  
                                 Nest API URL.
      --nest-client-id=NEST-CLIENT-ID  
                                 OAuth2 Client ID
      --nest-client-secret=NEST-CLIENT-SECRET  
                                 OAuth2 Client Secret.
      --nest-project-id=NEST-PROJECT-ID  
                                 Device Access Project ID.
      --nest-refresh-token=NEST-REFRESH-TOKEN  
                                 Refresh token
      --nest-google-auth-url=NEST-GOOGLE-AUTH-URL
                                 Google auth URL for access to the Nest app. Optional: only needed for scraping Nest Temperature Sensors.
                                 Optional: only needed for scraping Nest Temperature Sensors and the outside temperatures reported by the Nest App.
      --nest-google-auth-cookies=NEST-GOOGLE-AUTH-COOKIES
                                 Cookies for the Google auth URL for access to the Nest app.
                                 Optional: only needed for scraping Nest Temperature Sensors and the outside temperatures reported by the Nest App.
      --[no-]nest-label-spaces-to-dashes
                                 Whether to replace spaces with dashes in Nest thermostat label.
                                 Default: do not replace.
      --owm-url="http://api.openweathermap.org/data/2.5/weather"  
                                 The OpenWeatherMap API URL.
      --owm-auth=OWM-AUTH        The authorization token for OpenWeatherMap API.
      --owm-location="2759794"   The location ID for OpenWeatherMap API. Defaults to Amsterdam.
  -v, --version                  Show application version.

```


### Authentication

#### Nest API

To be able to call the Nest API you need to register for Device Access with Google (there's a one-time $5 fee) and follow [the Get Started guide](https://developers.google.com/nest/device-access/get-started) to create a Device Access project and OAuth2 client.

Then, follow the [Authorize the account guide](https://developers.google.com/nest/device-access/authorize) to get the necessary values for:
* OAuth2 Client ID
* OAuth2 Client Secret
* Device Access Project ID
* OAuth2 Refresh Token

Because ProNestheus is meant to run continuously, it doesn't require OAuth2 Access Token, only the Refresh Token. It will automatically get the valid access token and refresh it when needed.


#### Nest App API

Google's Device Access API does not expose information about Nest Temperature Sensors. If you want
to get information from your Nest Temperature Sensors and the outside temperature readings reported
by the Nest app, this scraper can try to gather this information by accessing the API used by the
Nest app. Beware that this hacky approach is not guaranteed to continue working and requires Google
Account cookies which cannot be scoped so that only the Nest-related information can be accessed
using those cookies.

The approach for obtaining access to the API used by the Nest app and the detailed instructions for
obtaining the credentials was borrowed from
[chrisjshull/homebridge-nest](https://github.com/chrisjshull/homebridge-nest). Thank you very much!

In short, you will need to sign in into https://home.nest.com in Chrome and grab the
**user-specific** authentication URL and the cookies used with that URL and provide them to
ProNestheus via two command-line arguments or their environment variable counterparts.
These values look something like this:

URL:
```
https://accounts.google.com/o/oauth2/iframerpc?action=issueToken&response_type=token%20id_token&login_hint=REDACTED&client_id=REDACTED.apps.googleusercontent.com&origin=https%3A%2F%2Fhome.nest.com&scope=openid%20profile%20email%20https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fnest-account&ss_domain=https%3A%2F%2Fhome.nest.com&include_granted_scopes=true&auto=0
```

Cookies:
```
NID=REDACTED; __Secure-3PSID=REDACTED; __Secure-3PAPISID=REDACTED; __Host-3PLSID=REDACTED; __Secure-3PSIDCC=REDACTED
```

**BEWARE: These cookies, once stolen, provide possibly full access to your Google Account, and not
just the information from your Nest app. If you're worried about this, create a separate Google
Account and give it access to your Nest(s) -- by sharing the respective "Home" in the Nest app or
the Google Home app with this new account. Then use that account's auth URL and cookies in the
instructions below. You should also consider passing in the URL and the cookies not as commmand-line
parameters but as the respective environment variables (see `--help`) to reduce the opportunities
for their leakage.**

1. Open a Chrome browser tab in Incognito Mode (or clear your cache).
2. Open Developer Tools (Ctrl+Shift+C or, on macOS, Cmd+Shift+C).
3. Click on 'Network' tab. Make sure 'Preserve Log' is checked.
4. In the 'Filter' box, enter `issueToken`
5. Go to `home.nest.com`, and click 'Sign in with Google'. Sign in into your account.
6. One network call (beginning with `iframerpc`) will appear in the Dev Tools window. Click on it.
7. In the Headers tab, under General, copy the entire `Request URL` (beginning with `https://accounts.google.com`). This is your `--nest-google-auth-url` command-line parameter (don't forget to quote it because of all the special characters).
8. In the 'Filter' box, enter `oauth2/iframe`
9. Several network calls will appear in the Dev Tools window. Click on the last `iframe` call.
10. In the Headers tab, under Request Headers, copy the entire `cookie` (**include the whole string which is several lines long and has many field/value pairs** - do not include the `cookie:` name). This is your `--nest-google-auth-cookies` command-line parameter (don't forget to quote it because of all the special characters).
11. Do not log out of `home.nest.com`, as this will invalidate your credentials. Just close the browser tab.
12. These credentials appear to be valid for about a year. Just repeat this procedure a year later.


#### OpenWeatherMap API

OpenWeatherMap API key is required to call the weather API. [Look here](https://openweathermap.org/appid) for instructions on how to get it.


## Exported metrics

```
# HELP nest_ambient_temperature_celsius Inside temperature.
# TYPE nest_ambient_temperature_celsius gauge
nest_ambient_temperature_celsius{id="abcd1234",label="Living Room",room="Living Room"} 23.5
# HELP nest_heating Is thermostat heating.
# TYPE nest_heating gauge
nest_heating{id="abcd1234",label="Living Room",room="Living Room"} 0
# HELP nest_cooling Is thermostat cooling.
# TYPE nest_cooling gauge
nest_cooling{id="abcd1234",label="Living Room",room="Living Room"} 1
# HELP nest_humidity_percent Inside humidity.
# TYPE nest_humidity_percent gauge
nest_humidity_percent{id="abcd1234",label="Living Room",room="Living Room"} 55
# HELP nest_setpoint_temperature_celsius Heating setpoint temperature.
# TYPE nest_setpoint_temperature_celsius gauge
nest_setpoint_temperature_celsius{id="abcd1234",label="Living Room",room="Living Room"} 18
# HELP nest_heat_setpoint_temperature_celsius Heating setpoint temperature.
# TYPE nest_heat_setpoint_temperature_celsius gauge
nest_heat_setpoint_temperature_celsius{id="abcd1234",label="Living Room",room="Living Room"} 18
# HELP nest_cool_setpoint_temperature_celsius Cooling setpoint temperature.
# TYPE nest_cool_setpoint_temperature_celsius gauge
nest_cool_setpoint_temperature_celsius{id="abcd1234",label="Living Room",room="Living Room"} 24
# HELP nest_online Is the thermostat online.
# TYPE nest_online gauge
nest_online{id="abcd1234",label="Living Room",room="Living Room"} 1
# HELP nest_up Was talking to Nest API successful.
# TYPE nest_up gauge
nest_up 1
# HELP nest_temp_sensor_temperature_celsius Temperature Sensor temperature
# TYPE nest_temp_sensor_temperature_celsius gauge
nest_temp_sensor_temperature_celsius{serial="22AA01AC123456AB",structure="Home",where="Living Room"} 22
# HELP nest_temp_sensor_battery Temperature Sensor battery level (0-100)
# TYPE nest_temp_sensor_battery gauge
nest_temp_sensor_battery{serial="22AA01AC123456AB",structure="Home",where="Living Room"} 79
# HELP nest_outside_temperature_celsius Outside temperature
# TYPE nest_outside_temperature_celsius gauge
nest_outside_temperature_celsius{id="40e8bbfc-f0b4-4d08-861b-2424f5de7d19",name="Home"} 27.8
# HELP nest_weather_humidity_percent Outside humidity.
# TYPE nest_weather_humidity_percent gauge
nest_weather_humidity_percent 82
# HELP nest_weather_pressure_hectopascal Outside pressure.
# TYPE nest_weather_pressure_hectopascal gauge
nest_weather_pressure_hectopascal 1016
# HELP nest_weather_temperature_celsius Outside temperature.
# TYPE nest_weather_temperature_celsius gauge
nest_weather_temperature_celsius 17.57
# HELP nest_weather_up Was talking to OpenWeatherMap API successful.
# TYPE nest_weather_up gauge
nest_weather_up 1
```
