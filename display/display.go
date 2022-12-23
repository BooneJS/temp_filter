package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
)

type Temperatures struct {
	high    float64
	current float64
	low     float64
}

func ToF(c float64) float64 {
	return (c * 1.8) + 32
}

type TemperatureScale int32

func (ts *TemperatureScale) Set(s string) error {
	switch strings.ToLower(s) {
	case "celsius":
		*ts = Celsius
	case "fahrenheit":
		*ts = Fahrenheit
	default:
		return fmt.Errorf("%s is not celsius or fahrenheit", s)
	}
	return nil
}

const (
	Celsius TemperatureScale = iota
	Fahrenheit
)

func (t Temperatures) String(scale TemperatureScale) string {
	if scale == Celsius {
		return fmt.Sprintf("currently %.2f°C, with high of %.2f°C and low of %.2f°C",
			t.current, t.high, t.low)
	}
	return fmt.Sprintf("currently %.2f°F, with high of %.2f°F and low of %.2f°F",
		ToF(t.current), ToF(t.high), ToF(t.low))
}

type resp struct {
	garage  Temperatures
	porch   Temperatures
	outside Temperatures
}

type Location int32

const (
	Garage Location = iota
	Porch
	Outside
)

func (l Location) String() string {
	switch l {
	case Garage:
		return "Garage"
	case Porch:
		return "Porch"
	case Outside:
		return "Outside"
	}
	panic("Switch fall-through")
	return "NOTFOUND"
}

func fetchTemperatures(location Location) Temperatures {
	dbc := influxdb2.NewClient("http://localhost:8086", "temperature:temperature")
	query := dbc.QueryAPI("raspberry")
	// influx -database 'Temperatures' -execute "SELECT max("Temperature") FROM \"sample\" WHERE \"location\" = 'Garage'" -format json -precision rfc3339
	// {"results":[{"series":[{"name":"sample","columns":["time","max"],"values":[["2021-12-31T08:00:14.550095777Z",20.5]]}]}]}
	//|> range(start: -2h, stop: now())
	temps := make(map[string]float64)
	allFuncs := []string{"min", "last", "max"}
	for i, f := range allFuncs {
		temps[f] = -69.0 + float64(i)
	}

	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		panic(err)
	}
	year, month, day := time.Now().Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).In(loc)
	//midnight = midnight.Add(time.Duration(-6) * time.Hour) // sorta Central Time
	fmt.Printf("Midnight Chicago is %v\n", midnight.Format(time.RFC3339))
	//fmt.Printf("Midnight Chicago is %v\n", midnight.Format(time.RFC3339))
	for _, f := range allFuncs {
		fluxQuery := fmt.Sprintf(`
		from(bucket: "Temperatures/a_year")
		|> range(start: %s, stop: now())
		|> %s(column: "_value")
		|> filter(fn: (r) => r.location == "%s" and
							r._field == "Temperature")`,
			midnight.Format(time.RFC3339), f, location.String())
		fmt.Printf("query: %s\n", fluxQuery)
		result, err := query.Query(context.Background(), fluxQuery)
		if err != nil {
			panic(err)
		}
		// Iterate over query response
		assigned := false
		for result.Next() {
			if assigned {
				panic(fmt.Sprintf("Multiple rows returned for %s\n", f))
			}
			//fmt.Printf("record: %v\n", result.Record())
			num, ok := result.Record().Value().(float64)
			if !ok {
				panic("not ok")
			}
			fmt.Printf("@ %v, temps[\"%s\"] = %f\n", result.Record().Time(), f, num)
			temps[f] = num
			assigned = true
		}
		// check for an error
		if result.Err() != nil {
			fmt.Printf("query parsing error: %s\n", result.Err().Error())
		}
	}
	dbc.Close()
	return Temperatures{
		current: temps["last"],
		low:     temps["min"],
		high:    temps["max"],
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/temperatures" {
		fmt.Fprintf(w, "The Garage is %s\n", fetchTemperatures(Garage).String(flagScale))
		fmt.Fprintf(w, "The Porch is %s\n", fetchTemperatures(Porch).String(flagScale))
		fmt.Fprintf(w, "The Outside is %s\n", fetchTemperatures(Outside).String(flagScale))
	}
}

var flagScale TemperatureScale

func init() {
	f := flag.String("scale", "celsius", "celsius or fahrenheit")
	flag.Parse()
	flagScale.Set(*f)
}

func main() {
	fmt.Printf("The Garage is %s\n", fetchTemperatures(Garage).String(flagScale))
	fmt.Printf("The Porch is %s\n", fetchTemperatures(Porch).String(flagScale))
	fmt.Printf("The Outside is %s\n", fetchTemperatures(Outside).String(flagScale))
	os.Exit(1)
	http.HandleFunc("/", handler)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
