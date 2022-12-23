package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	db "github.com/influxdata/influxdb-client-go/v2"
	"github.com/kardianos/service"
)

// Allows for custom unmarshal of date/time for reuse in InfluxDB.
const formatTime = "2006-01-02 15:04:05"

type CustomTime time.Time

func (ct CustomTime) UnmarshalJSON(b []byte) (err error) {
	s := strings.Trim(string(b), `"`)
	t, err := time.Parse(formatTime, s)
	ct = CustomTime(t)
	return
}

func (ct CustomTime) MarshalJSON() ([]byte, error) {
	return []byte(ct.String()), nil
}

func (ct CustomTime) String() string {
	t := time.Time(ct)
	return fmt.Sprintf("%v", t.Format(formatTime))
}

// Maps sensor id to location.
var idAllowlist = map[int]string{
	9788:  "Garage",
	12869: "Porch",
	13875: "Outside",
}

type AcuriteMessage struct {
	Time          CustomTime
	Model         string
	Id            int
	Channel       string
	Battery_ok    int
	Temperature_C float32
	Humidity      float32
	Mic           string
}

var logger service.Logger

type program struct {
	mqttc *mqtt.Client
	dbc   *db.Client
}

func (p *program) Start(s service.Service) error {
	go p.run()
	return nil
}
func (p *program) run() {
	incoming := make(chan []byte)
	opts := mqtt.NewClientOptions().AddBroker("tcp://localhost:1883")
	opts.SetClientID("acurite_filterer")
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		incoming <- msg.Payload()
	})
	c := mqtt.NewClient(opts)
	p.mqttc = &c

	if token := (*p.mqttc).Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	if token := (*p.mqttc).Subscribe("rtl_433/#", 0, nil); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	dbc := db.NewClient("http://localhost:8086", "temperature:temperature")
	p.dbc = &dbc
	writeDb := dbc.WriteAPI("raspberry", "Temperatures/a_year")
	errorsCh := writeDb.Errors()
	go func() {
		for err := range errorsCh {
			fmt.Printf("DB write error: %s\n", err.Error())
		}
	}()

	var message AcuriteMessage
	prevPayload := []byte{}
	for {
		p := <-incoming
		if bytes.Equal(prevPayload, p) {
			continue
		}
		prevPayload = p
		if err := json.Unmarshal(p, &message); err != nil {
			panic(err)
		}
		if location, ok := idAllowlist[message.Id]; ok {
			p := db.NewPointWithMeasurement("sample").
				AddTag("location", location).
				AddTag("AcuRiteId", string(message.Id)).
				AddField("Temperature", message.Temperature_C).
				AddField("Humidity", message.Humidity).
				SetTime(time.Time(message.Time))
			go writeDb.WritePoint(p)
			//fmt.Printf("Good message to %s: %v\n", location, message)
		}

	}
	return
}

func (p *program) Stop(s service.Service) error {
	// Stop should not block. Return with a few seconds.
	if token := (*p.mqttc).Unsubscribe("rtl_433"); token.Wait() && token.Error() != nil {
		fmt.Println(token.Error())
		os.Exit(1)
	}

	(*p.mqttc).Disconnect(250)
	(*p.dbc).Close()
	return nil
}

func main() {
	svcFlag := flag.String("service", "", "Control the system service.")
	flag.Parse()

	svcConfig := &service.Config{
		Name:        "AcuRiteTemperatureFilter",
		DisplayName: "AcuRite Temperature Filter",
		Description: "Acurite Temperature Filter",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}
	logger, err = s.Logger(nil)
	if err != nil {
		log.Fatal(err)
	}

	if len(*svcFlag) != 0 {
		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	} else {
		err = s.Run()
		if err != nil {
			logger.Error(err)
		}
	}
}
