package proxies

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/honeycast/honeycast.io/xnet"
	"github.com/influx6/flux"
	"golang.org/x/crypto/ssh"
)

type (

	//TCPKeepAliveListener provides the same internal wrapping as http keep alive functionalities
	TCPKeepAliveListener struct {
		*net.TCPListener
	}
)

var (
	//ErrTimeout provides a timeout error
	ErrTimeout = errors.New("Timeout on Connection")
	//threshold returns a chan of time
	threshold = func(ds time.Duration) <-chan time.Time {
		return time.After(ds)
	}
	//ErrorNotFind stands for errors when value not find
	ErrorNotFind = errors.New("NotFound!")
	//ErrorBadRequestType stands for errors when the interface{} recieved can not
	//be type asserted as a *http.Request object
	ErrorBadRequestType = errors.New("type is not a *http.Request")
	//ErrorBadHTTPPacketType stands for errors when the interface{} received is not a
	//bad request type
	ErrorBadHTTPPacketType = errors.New("type is not a HTTPPacket")
	//ErrorNoConnection describe when a link connection does not exists"
	ErrorNoConnection = errors.New("NoConnection")
)

//LoadTLS loads a tls.Config from a key and cert file path
func LoadTLS(cert, key string) (*tls.Config, error) {
	var config *tls.Config
	config.Certificates = make([]tls.Certificate, 1)

	c, err := tls.LoadX509KeyPair(cert, key)

	if err != nil {
		return nil, err
	}

	config.Certificates[0] = c
	return config, nil
}

//Accept sets the keep alive features of the tcp listener
func (t *TCPKeepAliveListener) Accept() (net.Conn, error) {
	tc, err := t.AcceptTCP()
	if err != nil {
		return nil, err
	}

	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(3 * time.Minute)
	return tc, nil
}

//KeepAliveListener returns a new TCPKeepAliveListener
func KeepAliveListener(t *net.TCPListener) *TCPKeepAliveListener {
	return &TCPKeepAliveListener{t}
}

//MakeListener returns a new net.Listener for http.Request
func MakeListener(addr, ts string, conf *tls.Config) (net.Listener, error) {

	var l net.Listener
	var err error

	if conf == nil {
		l, err = tls.Listen(ts, addr, conf)
	} else {
		l, err = net.Listen(ts, addr)
	}

	if err != nil {
		return nil, err
	}

	return l, nil
}

//MakeBaseListener returns a new net.Listener(*TCPKeepAliveListener) for http.Request
func MakeBaseListener(addr string, conf *tls.Config) (net.Listener, error) {

	var l net.Listener
	var err error

	log.Info("New base Listener for %s", addr)
	if conf != nil {
		l, err = tls.Listen("tcp", addr, conf)
	} else {
		l, err = net.Listen("tcp", addr)
	}

	if err != nil {
		return nil, err
	}

	tl, ok := l.(*net.TCPListener)

	if !ok {
		return nil, ErrBadConn
	}

	return KeepAliveListener(tl), nil
}

//MakeBaseServer returns a new http.Server using the provided listener
func MakeBaseServer(l net.Listener, handle http.Handler, c *tls.Config) (*http.Server, net.Listener, error) {

	tl, ok := l.(*net.TCPListener)

	if !ok {
		return nil, nil, fmt.Errorf("Listener is not type *net.TCPListener")
	}

	s := &http.Server{
		Addr:           tl.Addr().String(),
		Handler:        handle,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
		TLSConfig:      c,
	}

	s.SetKeepAlivesEnabled(true)
	go s.Serve(KeepAliveListener(tl))

	return s, tl, nil
}

//CreateHTTP returns a http server using the giving address
func CreateHTTP(addr string, handle http.Handler) (*http.Server, net.Listener, error) {
	l, err := net.Listen("tcp", addr)

	if err != nil {
		return nil, nil, err
	}

	return MakeBaseServer(l, handle, nil)
}

//CreateTLS returns a http server using the giving address
func CreateTLS(addr string, conf *tls.Config, handle http.Handler) (*http.Server, net.Listener, error) {
	l, err := tls.Listen("tcp", addr, conf)

	if err != nil {
		return nil, nil, err
	}

	return MakeBaseServer(l, handle, conf)
}

//LunchHTTP returns a http server using the giving address
func LunchHTTP(addr string, handle http.Handler) error {
	_, _, err := CreateHTTP(addr, handle)
	return err
}

//LunchTLS returns a http server using the giving address
func LunchTLS(addr string, conf *tls.Config, handle http.Handler) error {
	_, _, err := CreateTLS(addr, conf, handle)
	return err
}

//DialServerConn serves in place of ssh.NewServerConn
func DialServerConn(ds time.Duration, con net.Conn, conf *ssh.ServerConfig) (sc *ssh.ServerConn, cs <-chan ssh.NewChannel, rs <-chan *ssh.Request, ex error) {

	done := make(chan struct{})
	reset := make(chan struct{})

	authlog := conf.AuthLogCallback
	logger := func(conn ssh.ConnMetadata, method string, err error) {
		flux.GoDefer("AuthLogCallback", func() {
			flux.GoDefer("AuthLog", func() {
				if authlog != nil {
					authlog(conn, method, err)
				}
			})
			reset <- struct{}{}
		})
	}

	conf.AuthLogCallback = logger

	flux.GoDefer("NewServerConn", func() {
		defer close(done)
		sc, cs, rs, ex = ssh.NewServerConn(con, conf)
		return
	})

	expiration := threshold(ds)

	func() {

	nsloop:
		for {
			select {
			case <-done:
				expiration = nil
				break nsloop
			case <-reset:
				expiration = threshold(ds)
			case <-expiration:
				if sc != nil {
					sc.Close()
				}
				sc = nil
				cs = nil
				rs = nil
				ex = fmt.Errorf("Expired NewServerConn call for ip:%+s ", con.RemoteAddr())
				break nsloop
			}
		}

	}()
	return
}

//DialClient returns two channels where one returns a ssh.Client and the other and error
func DialClient(dial, expire time.Duration, ip string, conf *ssh.ClientConfig, retry <-chan struct{}) (*ssh.Client, error) {

	flux.Report(nil, fmt.Sprintf("MakeDial for %s for dailing at %+s and expiring in %+s", conf.User, dial, expire))

	cons := make(chan *ssh.Client)
	errs := make(chan error)

	var con net.Conn
	var sc ssh.Conn
	var chans <-chan ssh.NewChannel
	var req <-chan *ssh.Request
	var err error

	flux.GoDefer("MakeDial", func() {
		con, err = net.DialTimeout("tcp", ip, dial)

		if err != nil {
			flux.Report(err, fmt.Sprintf("MakeDial:Before for %s net.DailTimeout", ip))
			errs <- err
			return
		}

		sc, chans, req, err = ssh.NewClientConn(con, ip, conf)

		if err != nil {
			flux.Report(err, fmt.Sprintf("MakeDial:After for %s ssh.NewClientConn", ip))
			errs <- err
			return
		}

		flux.Report(nil, fmt.Sprintf("MakeDial initiating NewClient for %s", ip))
		cons <- ssh.NewClient(sc, chans, req)
		return
	})

	expiration := threshold(expire)

	go func() {
		for _ = range retry {
			expiration = threshold(expire)
		}
	}()

	select {
	case err := <-errs:
		flux.Report(err, fmt.Sprintf("NewClient Ending!"))
		return nil, err
	case som := <-cons:
		flux.Report(nil, fmt.Sprintf("NewClient Created!"))
		expiration = nil
		return som, nil
	case <-expiration:
		flux.Report(nil, fmt.Sprintf("MakeDial Expired for %s!", ip))
		defer con.Close()
		if sc != nil {
			sc.Close()
		}
		return nil, ErrTimeout
	}
}

//AddHTTPBundles hands a default bundle to the supplied director
func AddHTTPBundles(x ProxyDirector, http HTTPStreamer, check bool) {

	x.Register("http", func(req *ProxyRequest, d *Director, a Action) error {
		to := net.JoinHostPort(req.Addr, string(req.Port))
		return HTTPProvider(x, http, a, req.From, to, false, check)
	})

	x.Register("httpcon-reqres", func(req *ProxyRequest, d *Director, a Action) error {
		to := net.JoinHostPort(req.Addr, string(req.Port))
		return HTTPProvider(x, http, a, req.From, to, true, check)
	})

	x.Register("httptls", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		to := net.JoinHostPort(req.Addr, fmt.Sprintf("%d", req.Port))
		// to := net.JoinHostPort(req.Addr, string(req.Port))
		return TLSHPProvider(x, http, a, req.From, to, cert, check)
	})
}

//AddTCPBundles hands a default bundle to the supplied director
func AddTCPBundles(x ProxyDirector, stream TCPStreamer, check bool) {

	x.Register("tcp", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		to := net.JoinHostPort(req.Addr, fmt.Sprintf("%d", req.Port))
		return TCPProvider(x, stream, a, req.From, to, cert, check)
	})

	x.Register("xtcp", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		log.Info("XTcp Will set up custom Addr to %s", req.From)
		return x.ServeCustomTCP(req.From, cert, func(con net.Conn, dir Directors) error {

			var c *ConnInsight
			var err error

			ip, _, _ := net.SplitHostPort(con.RemoteAddr().String())

			log.Info("XTCP using IP:%s to Port: %d", ip, req.Port)
			addr := net.JoinHostPort(ip, fmt.Sprintf("%d", req.Port))
			log.Info("XTCP using new endpoint Addr: %d", addr)

			req.Addr = ip
			log.Info("XTCP Done setting IP Addr: %d", addr)

			// flux.GoDefer("StreamWithConn.XTcp", func() {
			log.Info("XTCP Begin streaming using tcp.StreamWithConn IP Addr: %d", addr)
			c, err = stream.StreamWithConn(con, addr, cert)
			log.Info("XTCP Done streaming using tcp.StreamWithConn IP Addr: %d", addr)

			if err != nil {
				flux.Report(err, fmt.Sprintf("XTcp XConn Closing Connection for %s", ip))
				flux.Report(con.Close(), fmt.Sprintf("XTcp XConn Closing Connection for %s", ip))
				return err
			}
			// })

			a(c)
			return nil
		}, check)
	})
}

//AddSSHBundles hands a default bundle to the supplied director
func AddSSHBundles(x ProxyDirector, sv SSHStreamer, serv *SSHStreamServer, check bool) error {

	if serv == nil {
		return ErrBadBundle
	}

	if sv == nil {
		return ErrBadBundle
	}

	x.Register("ssh", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		return x.ServeCustomTCP(req.From, cert, func(con net.Conn, d Directors) error {

			addr := net.JoinHostPort(req.Addr, fmt.Sprintf("%d", req.Port))
			// addr := net.JoinHostPort(req.Addr, string(req.Port))

			sl, cl, err := serv.StreamConnection(con, addr, nil)

			flux.Report(err, fmt.Sprintf("Initiated ssh authentication Scheme for %s and %s", con.RemoteAddr().String(), addr))

			if err != nil {
				return err
			}

			cn, err := sv.Stream(sl, cl)

			flux.Report(err, fmt.Sprintf("Initiated ssh proxy Scheme for %s -> %s", con.RemoteAddr().String(), addr))

			if err != nil {
				return err
			}

			a(cn)
			return nil
		}, check)
	})

	return nil
}

//AddSSHSpecialBundles hands a default bundle to the supplied director
func AddSSHSpecialBundles(x ProxyDirector, sv SSHStreamer, serv *SSHStreamServer, controls *xnet.Control, check bool, each xnet.EachControl) error {

	if serv == nil {
		return ErrBadBundle
	}

	if sv == nil {
		return ErrBadBundle
	}

	if controls == nil {
		return ErrBadBundle
	}

	x.Register("cssh", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		rq, _ := serv.Reports()
		ls, err := xnet.ListenTCP("tcp", req.From, cert, controls, each, rq)

		if err != nil {
			return err
		}

		return x.ServeCustom(ls, func(con net.Conn, d Directors) error {

			xcon, ok := con.(*xnet.XConn)

			flux.Report(nil, fmt.Sprintf("Retrieving XConn from net.Conn for IP  %s -> %t", con.RemoteAddr().String(), ok))

			if !ok {
				return xnet.ErrBadXConn
			}

			ip, err := xcon.Container().IP()

			flux.Report(err, fmt.Sprintf("Retrieving Container IP from XConn for %s -> %s", con.RemoteAddr().String(), ip))

			if err != nil {
				return err
			}

			req.Addr = ip
			addr := net.JoinHostPort(ip, fmt.Sprintf("%d", req.Port))
			// addr := net.JoinHostPort(ip, string(req.Port))

			sl, cl, err := serv.StreamConnection(xcon, addr, nil)

			flux.Report(err, fmt.Sprintf("Initiated ssh authentication Scheme for %s and %s", con.RemoteAddr().String(), addr))

			if err != nil {
				return err
			}

			cn, err := sv.Stream(sl, cl)

			flux.Report(err, fmt.Sprintf("Initiated ssh proxy Scheme for %s -> %s", con.RemoteAddr().String(), addr))

			if err != nil {
				return err
			}

			rip, _, _ := net.SplitHostPort(cl.Client.Meta.RemoteAddr().String())

			cn.Meta["cip"] = ip
			cn.Meta["rip"] = rip
			cn.Meta["name"] = xcon.Container().Name()
			cn.Meta["port"] = fmt.Sprintf("%d", req.Port)
			cn.Meta["pass"] = string(cl.Client.Pass)
			cn.Meta["user"] = cl.Client.Meta.User()

			a(cn)
			cn.open.Emit(true)
			return nil
		}, check)
	})

	return nil
}

//AddTCPSpecialBundles hands a default bundle to the supplied director
func AddTCPSpecialBundles(x ProxyDirector, stream TCPStreamer, controls *xnet.Control, check bool, each xnet.EachControl, report *flux.StackReport) error {

	if stream == nil {
		return ErrBadBundle
	}

	if controls == nil {
		return ErrBadBundle
	}

	x.Register("ctcp", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		log.Info("CTcp Xnet creating xnet.Listener for tcp for %s", req.From)
		ls, err := xnet.ListenTCP("tcp", req.From, cert, controls, each, report)

		if err != nil {
			return err
		}

		return x.ServeCustom(ls, func(con net.Conn, d Directors) error {

			xcon, ok := con.(*xnet.XConn)

			flux.Report(nil, fmt.Sprintf("Retrieving CTcp XConn from net.Conn for IP  %s -> %t", con.RemoteAddr().String(), ok))

			if !ok {
				return xnet.ErrBadXConn
			}

			ip, err := xcon.Container().IP()

			flux.Report(err, fmt.Sprintf("Retrieving Container IP from CTcp XConn for %s -> %s", con.RemoteAddr().String(), ip))

			if err != nil {
				return err
			}

			req.Addr = ip
			addr := net.JoinHostPort(ip, fmt.Sprintf("%d", req.Port))
			// addr := net.JoinHostPort(ip, string(req.Port))

			cn, err := stream.StreamWithConn(xcon, addr, cert)

			flux.Report(err, fmt.Sprintf("Initiated ctcp container proxy Scheme for %s -> %s", con.RemoteAddr().String(), addr))

			if err != nil {
				// flux.Report(xcon.Close(), fmt.Sprintf("Closing tcp container proxy Scheme for %s -> %s", con.RemoteAddr().String(), addr))
				return err
			}

			rip, _, _ := net.SplitHostPort(con.RemoteAddr().String())

			cn.Meta["container_ip"] = ip
			cn.Meta["remote_ip"] = rip
			cn.Meta["local_ip"] = con.LocalAddr().String()
			cn.Meta["name"] = xcon.Container().Name()
			cn.Meta["port"] = fmt.Sprintf("%d", req.Port)

			a(cn)
			cn.Opened().Emit(true)
			return nil
		}, check)
	})

	return nil
}

//AddHTTPSpecialBundles hands a default bundle to the supplied director
func AddHTTPSpecialBundles(x ProxyDirector, stream HTTPStreamer, controls *xnet.Control, check bool, each xnet.EachControl, report *flux.StackReport) error {

	if stream == nil {
		return ErrBadBundle
	}

	if controls == nil {
		return ErrBadBundle
	}

	x.Register("chttp", func(req *ProxyRequest, d *Director, a Action) error {

		var err error
		var cert *tls.Config

		if req.CertFile != "" && req.KeyFile != "" {
			cert, err = LoadTLS(req.CertFile, req.KeyFile)

			if err != nil {
				return err
			}
		}

		ls, err := xnet.ListenTCP("tcp", req.From, cert, controls, each, report)

		if err != nil {
			return err
		}

		return x.ServeCustom(ls, func(con net.Conn, d Directors) error {

			xcon, ok := con.(*xnet.XConn)

			flux.Report(nil, fmt.Sprintf("Retrieving HTTP XConn from net.Conn for IP  %s -> %t", con.RemoteAddr().String(), ok))

			if !ok {
				return xnet.ErrBadXConn
			}

			ip, err := xcon.Container().IP()

			flux.Report(err, fmt.Sprintf("Retrieving Container IP from HTTP XConn for %s -> %s", con.RemoteAddr().String(), ip))

			if err != nil {
				return err
			}

			req.Addr = ip
			addr := net.JoinHostPort(ip, fmt.Sprintf("%d", req.Port))
			// addr := net.JoinHostPort(ip, string(req.Port))

			cn, err := stream.StreamWithConn(xcon, addr, cert)

			flux.Report(err, fmt.Sprintf("Initiated HTTP tcp container proxy Scheme for %s -> %s", con.RemoteAddr().String(), addr))

			if err != nil {
				return err
			}

			rip, _, _ := net.SplitHostPort(ip)
			cn.Meta["container_ip"] = ip
			cn.Meta["remote_ip"] = rip
			cn.Meta["local_ip"] = con.LocalAddr().String()
			cn.Meta["name"] = xcon.Container().Name()
			cn.Meta["port"] = fmt.Sprintf("%d", req.Port)

			a(cn)
			return nil
		}, check)
	})

	return nil
}
