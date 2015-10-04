package proxies

import (
	"crypto/tls"
	"net"
	"net/http"
)

//TCPProvider wraps the creation of a ConnServe and binds the director for easy management and serves a (optional tls or normal) bare net.Conn tcp connection (i.e using tls with a base net.Conn and not a http.Server)
func TCPProvider(d ProxyDirector, stream TCPStreamer, a Action, from, to string, conf *tls.Config, checkers bool) error {
	return d.ServeCustomTCP(from, conf, func(con net.Conn, dir Directors) error {

		var c *ConnInsight
		var err error

		c, err = stream.StreamWithConn(con, to, conf)

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, checkers)
}

//TLSCPProvider provides a special case when the connection must no doubt be as tcp net.Conn using a tsl.Config else turn to extracting the tls from the src net.Conn
func TLSCPProvider(d ProxyDirector, stream TCPStreamer, a Action, from, to string, conf *tls.Config, check bool) error {
	return d.ServeCustomTCP(from, conf, func(con net.Conn, dir Directors) error {

		var c *ConnInsight
		var err error

		if conf != nil {
			c, err = stream.StreamWithConn(con, to, conf)
		} else {
			c, err = stream.StreamTLSWith(con, to)
		}

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, check)
}

//TCPTYPEProvider wraps the creation of a ConnServe and binds the director for easy management for handling generic to be provided net.Conn with specific type
func TCPTYPEProvider(d ProxyDirector, stream TCPStreamer, a Action, from, to, ts string, conf *tls.Config, check bool) error {
	return d.ServeCustomConn(from, ts, func(con net.Conn, dir Directors) error {

		c, err := stream.StreamTypeConn(con, to, ts, conf)

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, conf, check)
}

//HTTPProvider provides a convenient provider for when dealing with raw http net.Conns alone without mixing in of http.Request,using the provided http proxy net.Conn centric functions. The last argument toReq (boolean) tells the server to if true treat the connection as going from a net.Conn to http.Request(that is use a http.Request to proxy) and if false instead use a http net.Conn for both request
func HTTPProvider(d ProxyDirector, stream HTTPStreamer, a Action, from, to string, toReq, check bool) error {
	return d.ServeCustomHTTPConns(from, nil, func(con net.Conn, dir Directors) error {
		var c *ConnInsight
		var err error

		if !toReq {
			c, err = stream.StreamWithConn(con, to, nil)
		} else {
			c, err = stream.StreamReq(con, to)
		}

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, check)
}

//TLSHPProvider provides a convenient provider for when dealing with raw http net.Conns alone without mixing in of http.Request,using the provided http proxy net.Conn centric functions,if a *tls.Config is supplied it uses that for all connection,else it takes the connection it gets and treat it as a tls.Conn then tries to extract the certificates to create a new tls.Conn to the destination
func TLSHPProvider(d ProxyDirector, stream HTTPStreamer, a Action, from, to string, conf *tls.Config, check bool) error {
	return d.ServeCustomHTTPConns(from, conf, func(con net.Conn, dir Directors) error {
		var c *ConnInsight
		var err error

		if conf != nil {
			c, err = stream.StreamWithConn(con, to, conf)
		} else {
			c, err = stream.StreamTLSWith(con, to)
		}

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, check)
}

//HTTPCustomProvider wraps the creation of a httpServe and binds the director for easy management. It provides dual use for two types of mechanism: 																	1. handling streaming from one http.Request to another(the destination)																							2. handling streaming from a http.Request to a net.Conn http connection																							This type are determined by the toCon bool switch,when true it turns into option 2 operation and when false turns to operation 1
func HTTPCustomProvider(d ProxyDirector, stream HTTPStreamer, a Action, from, to string, toCon bool, cf *tls.Config, check bool) error {
	return d.ServeCustomHTTP(from, cf, func(res http.ResponseWriter, req *http.Request, dir Directors) error {

		var c *ConnInsight
		var err error

		if toCon {
			c, err = stream.StreamUnitAddr(req, res, to, cf)
		} else {
			c, err = stream.StreamUnit(req, res, to)
		}

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, check)
}

//ReqResProvider is a special case for proxying between two individual http.Request and their response, using the ReqRes to ReqRes strategy, this is just created for convenience and for those who prefer such a high level proxy approach to the low-level http net.Conn to http net.Conn provided by Director.ServeHTTPConn
func ReqResProvider(d ProxyDirector, stream HTTPStreamer, a Action, from, to string, check bool) error {
	return d.ServeCustomHTTP(from, nil, func(res http.ResponseWriter, req *http.Request, dir Directors) error {

		var c *ConnInsight
		var err error

		c, err = stream.StreamReqRes(req, res, to)

		if err != nil {
			return err
		}

		a(c)
		return nil
	}, check)
}
