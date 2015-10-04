package proxies

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/influx6/flux"
)

//ServeBase returns a new ConnServe using an addr and an optional tls.Config
func ServeBase(t TargetOp, d Directors, addr string, conf *tls.Config, check bool) (*ConnServe, error) {
	l, err := MakeBaseListener(addr, conf)

	if err != nil {
		return nil, err
	}

	return NewConServe(l, d, t, check), nil
}

//ServeType returns a new ConnServe using an addr
func ServeType(t TargetOp, d Directors, addr, ft string, cf *tls.Config, check bool) (*ConnServe, error) {
	l, err := MakeListener(addr, ft, cf)

	if err != nil {
		return nil, err
	}

	return NewConServe(l, d, t, check), nil
}

//Serve returns a new ConnServe using addr and port
func Serve(t TargetOp, d Directors, addr string, check bool) (*ConnServe, error) {
	return ServeBase(t, d, addr, nil, check)
}

//ServeTLSFrom returns a new ConnServe using addr
func ServeTLSFrom(t TargetOp, d Directors, ft, addr string, conf *tls.Config, check bool) (*ConnServe, error) {
	l, err := tls.Listen(ft, addr, conf)
	if err != nil {
		return nil, err
	}
	return NewConServe(l, d, t, check), nil
}

//ServeTLS returns a new ConnServe using addr
func ServeTLS(t TargetOp, d Directors, addr string, conf *tls.Config, check bool) (*ConnServe, error) {
	return ServeTLSFrom(t, d, "tcp", addr, conf, check)
}

//ServeHTTPWith provides a different approach,instead of using the base net.Conn ,itself uses the http.Request and http.Response as means of proxying using the director
func ServeHTTPWith(t TargetReqResOp, addr string, d Directors, conf *tls.Config, check bool) (*HTTPServe, error) {
	var hs *http.Server
	var ls net.Listener
	var err error

	handler := http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		defer flux.Report(err, fmt.Sprintf("Request Processor Completed!"))
		flux.GoDefer("HttpServer", func() {
			d.Requests().Emit(true)
			flux.Report(err, fmt.Sprintf("Request Process Begin!"))
			// d.Wait()
			err := t(res, req, d)
			flux.Report(err, fmt.Sprintf("Request Processed Finished!"))
			if err != nil {
				res.WriteHeader(404)
				res.Write([]byte(err.Error()))
				flux.GoDefer("ReportError", func() {
					eos, ok := d.Errors()
					if ok {
						eos <- err
					}
				})
			}
		})
	})

	if conf != nil {
		hs, ls, err = CreateTLS(addr, conf, handler)
	} else {
		hs, ls, err = CreateHTTP(addr, handler)
	}

	flux.Report(err, fmt.Sprintf("HttpServe Listener Processor!"))
	if err != nil {
		return nil, err
	}

	hps := &HTTPServe{
		listener: ls,
		server:   hs,
		director: d,
		idle:     time.Now(),
		closer:   make(Notifier),
	}

	go func() {
		defer ls.Close()
		flux.Report(nil, fmt.Sprintf("HttpServer HealthCheck Status %t", check))
	nloop:
		for {
			select {
			case <-d.HealthNotify():
				flux.Report(nil, fmt.Sprintf("HttpServer checking HealthCheck status %t", check))
				if check {
					age := hps.director.MaxAge()
					idle := time.Duration(hps.idle.Unix())
					if idle > age {
						break nloop
					}
				}
			case <-hps.closer:
				break nloop
			}
		}
	}()

	return hps, nil
}

//ServeHTTP returns a HTTPServe for http ReqRes proxying
func ServeHTTP(t TargetReqResOp, d Directors, addr string, check bool) (*HTTPServe, error) {
	return ServeHTTPWith(t, addr, d, nil, check)
}
