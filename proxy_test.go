package proxies

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/influx6/flux"
)

func checkTestError(t *testing.T, e error, msg string) {
	if e != nil {
		t.Fatalf("Error Occcured: (%+v) For: (%s)", e, msg)
		return
	}
	t.Logf("Message: %s NoError", msg)
}

func TestDirector(t *testing.T) {

	err := LunchHTTP("127.0.0.1:3030", http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.Write([]byte(fmt.Sprintf("Welcome %s", req.URL.Path)))
	}))

	if err != nil {
		log.Fatalf("Creating HTTPServer@127.0.0.1:3030 Failed! %s", err)
	}

	ws := new(sync.WaitGroup)

	max := 1 * time.Minute
	check := 30 * time.Second

	dir := Direct(max, check, func(err error) {
		checkError(err, "Director:Error")
	})

	//setup the goroutines for work
	dir.Init()

	ws.Add(2)

	err = dir.ServeTCP(func(ci *ConnInsight) {
		flux.LogHeader(ci.In(), "Connection In() %s")
		flux.LogHeader(ci.Out(), "Connection Out() %s")
		ws.Done()
	}, "127.0.0.1:4040", "127.0.0.1:3030", nil)

	checkTestError(t, err, "Creating proxy from :4040 to :3030")

	err = dir.ServeHTTPConn(func(ci *ConnInsight) {
		flux.LogHeader(ci.In(), "Connection In() %s")
		flux.LogHeader(ci.Out(), "Connection Out() %s")
		ws.Done()
	}, "127.0.0.1:5040", "127.0.0.1:3030", false)

	checkTestError(t, err, "Creating proxy from :5040 to :3030")

	go dir.Pause()
	go makeRequest("http://127.0.0.1:5040/checkerbox", t)
	go makeRequest("http://127.0.0.1:4040/", t)

	go func() {
		<-time.After(5 * time.Second)
		dir.Resume()
	}()

	ws.Wait()
}

func makeRequest(addr string, t *testing.T) {
	log.Info("Sending Request: %s", addr)
	res, err := http.Get(addr)

	if err != nil {
		t.Fatalf("Error occured: %+s", err)
	}

	body, err := ioutil.ReadAll(res.Body)

	if err != nil {
		t.Fatalf("Error occured while reading body: %+s", err)
	}

	t.Logf("Body response: %s", body)
}
