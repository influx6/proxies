package proxies

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"time"

	"github.com/influx6/flux"
	"golang.org/x/crypto/ssh"
)

type (
	//MetaFunc is callback type tapping into a auth session
	MetaFunc func(error, ssh.ConnMetadata, []byte, *SSHClient)

	//PasswordAuthCallback stands for Authentication callback
	PasswordAuthCallback func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error)

	//PasswordAuthCaller stands for return a new client from the auth function
	PasswordAuthCaller func(string, time.Duration, time.Duration, MetaFunc) PasswordAuthCallback

	//SSHStreamServer provides a means of turning a standard net.Conn into a ssh net.Conn
	SSHStreamServer struct {
		pubs                ssh.Signer
		auth                PasswordAuthCaller
		dial, sdial, scdial time.Duration
		reports             *flux.StackReport
	}

	//SSHStreamer defines a custom streaming interface for ssh streamers
	SSHStreamer interface {
		ProxyStreams
		StreamSSH(*SSHServerConn, *SSHClientConn) (*ConnInsight, error)
	}

	//SSH defines a standard proxystream strategy
	SSH struct {
		ProxyStreams
	}

	//SSHConn defines a standard ssh connection including the internal connection and request and service channels
	SSHConn struct {
		*BaseConn
		Server ssh.Conn
	}

	//SSHServerConn defines a server conn ontop of SSHConn
	SSHServerConn struct {
		*SSHConn
		Channels <-chan ssh.NewChannel
		Requests <-chan *ssh.Request
	}

	//SSHClientConn defines a client conn ontop of SSHConn
	SSHClientConn struct {
		*SSHConn
		Client *SSHClient
	}

	//SSHClient provides a wrapper over the ssh.Client
	SSHClient struct {
		*ssh.Client
		Pass []byte
		Meta ssh.ConnMetadata
	}
)

var (
	//ErrBadBundle returns when a bundle fails
	ErrBadBundle = errors.New("Bundle Setup error!")
	//ErrNilClient returns when a client == nil fails
	ErrNilClient = errors.New("Client is nil!")
	//ErrBadAuth returns when a authentication fails
	ErrBadAuth = errors.New("Auth Failed!")
)

//ClientPassAuth provides a means of returning a ssh.Client and a Authentication function for a one time authentication procedure
func ClientPassAuth(addr string, dial, server time.Duration, cb MetaFunc) PasswordAuthCallback {

	reqs := make(chan struct{})

	return func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {

		config := &ssh.ClientConfig{}
		config.User = meta.User()
		config.Auth = []ssh.AuthMethod{
			ssh.Password(string(pass)),
		}

		cl, err := DialClient(dial, server, addr, config, reqs)

		if cb != nil {
			cb(err, meta, pass, &SSHClient{
				Client: cl,
				Meta:   meta,
				Pass:   pass,
			})
		}

		if err != nil {
			go func() {
				reqs <- struct{}{}
			}()
			return nil, err
		}

		close(reqs)
		return nil, nil
	}
}

//Close closes the connection
func (p *SSHConn) Close() (err error) {
	return p.Server.Close()
}

//Src returns the request as the src for the conn
func (p *SSHConn) Src() interface{} {
	return p.Server
}

//Reader returns the reader for the conn
func (p *SSHConn) Reader() io.ReadCloser {
	return nil
}

//Writer returns the writer for the conn
func (p *SSHConn) Writer() io.WriteCloser {
	return nil
}

//NewSSHConn returns a new ssh.Conn instance
func NewSSHConn(con ssh.Conn) *SSHConn {
	return &SSHConn{BaseConn: NewBaseConn(), Server: con}
}

//NewServerConn returns a new instance of ssh.SSHServerConn
func NewServerConn(c ssh.Conn, sc <-chan ssh.NewChannel, rs <-chan *ssh.Request) *SSHServerConn {
	return &SSHServerConn{
		NewSSHConn(c),
		sc,
		rs,
	}
}

//NewClientConn returns a new instance of ssh.SSHClientConn
func NewClientConn(c *SSHClient) *SSHClientConn {
	return &SSHClientConn{
		NewSSHConn(c),
		c,
	}
}

//StreamServer returns a new SSHStreamServer
func StreamServer(key string, dc, sv, sc time.Duration, r *flux.StackReport) (*SSHStreamServer, error) {
	bu, err := ioutil.ReadFile(key)

	if err != nil {
		return nil, err
	}

	priv, err := ssh.ParsePrivateKey(bu)

	if err != nil {
		return nil, err
	}

	return &SSHStreamServer{
		pubs:    priv,
		auth:    ClientPassAuth,
		dial:    dc,
		sdial:   sv,
		scdial:  sc,
		reports: r,
	}, nil
}

//Reports returns the internal supplied flux.StackReport'er if it exists or error if nil
func (s *SSHStreamServer) Reports() (*flux.StackReport, error) {
	if s.reports != nil {
		return s.reports, nil
	}

	return nil, fmt.Errorf("No Reporter")
}

//StreamStealConn takes the idea of using the provided ip of a incoming net.Conn as the ip of the target but with a different port
func (s *SSHStreamServer) StreamStealConn(con net.Conn, port int, mx MetaFunc) (*SSHServerConn, *SSHClientConn, error) {
	ip, _, _ := net.SplitHostPort(con.RemoteAddr().String())
	addr := net.JoinHostPort(ip, string(port))
	return s.StreamConnection(con, addr, mx)
}

//StreamConnection provides a means of streaming an ssh on top of a net.Conn
func (s *SSHStreamServer) StreamConnection(con net.Conn, toAddr string, mx MetaFunc) (*SSHServerConn, *SSHClientConn, error) {

	var client *SSHClient
	conf := &ssh.ServerConfig{}
	conf.AddHostKey(s.pubs)

	auth := s.auth(toAddr, s.dial, s.sdial, func(err error, meta ssh.ConnMetadata, pass []byte, c *SSHClient) {

		client = c

		if s.reports != nil {
			var es string
			state := true

			if err != nil {
				es = err.Error()
				state = false
			}

			s.reports.Report("ssh-auth", map[string]interface{}{
				"user":           meta.User(),
				"pass":           string(pass),
				"local_ip":       meta.LocalAddr().String(),
				"remote_ip":      meta.RemoteAddr().String(),
				"client_version": string(meta.ClientVersion()),
				"server_version": string(meta.ServerVersion()),
				"session_id":     string(meta.SessionID()),
				"state":          state,
				"protocol":       "ssh",
				"error":          es,
			})
		}

		if mx != nil {
			mx(err, meta, pass, client)
		}
	})

	conf.PasswordCallback = auth

	co, cn, rq, err := DialServerConn(s.scdial, con, conf)

	if err != nil {
		return nil, nil, err
	}

	if client == nil {
		return nil, nil, ErrNilClient
	}

	return NewServerConn(co, cn, rq), NewClientConn(client), nil
}

//SSHStream returns a new SSH stream handler
func SSHStream(he ErrorHandler) *SSH {
	return &SSH{
		NewProxyStream(func(c *ConnInsight, se NotifierError) {

			src := c.Src()
			dest := c.Dest()
			kill := make(chan struct{})

			scon, ok := src.(*SSHServerConn)

			if !ok {
				flux.GoDefer("ErrSubmit", func() {
					se <- ErrBadConn
				})
				return
			}

			ccon, ok := dest.(*SSHClientConn)

			if !ok {
				flux.GoDefer("ErrSubmit", func() {
					se <- ErrBadConn
				})
				return
			}

			// c.open.Emit(true)

			c.closed.Listen(func(_ interface{}) {
				close(kill)
			})

			con, req := scon.Channels, scon.Requests
			cli := ccon.Client

			flux.GoDefer("ErrSubmit", func() {
				ssh.DiscardRequests(req)
			})

			flux.GoDefer("ProxySubmit", func() {
			ploop:
				for {
					select {
					case ch, ok := <-con:
						if !ok {
							break ploop
						}

						coc, ceq, err := ch.Accept()

						checkError(err, fmt.Sprintf("Accepting Channel %s", ch.ChannelType()))

						if err != nil {
							return
						}

						err = proxyChannel(c, coc, ceq, ch, cli, kill)

						checkError(err, fmt.Sprintf("Creating Proxy Strategy for %s", ch.ChannelType()))

						if err != nil {
							return
						}

					case <-kill:
						break ploop
					}
				}
			})

		}, he),
	}
}

//StreamSSH streams the ssh server and client required
func (s *SSH) StreamSSH(sv *SSHServerConn, cl *SSHClientConn) (*ConnInsight, error) {
	return s.Stream(sv, cl)
}

//Reply handles the operation between a req and channel
func Reply(req *ssh.Request, dest ssh.Channel, c *ConnInsight) {

	dx, err := dest.SendRequest(req.Type, req.WantReply, req.Payload)

	checkError(err, fmt.Sprintf("Request %s processed", req.Type))

	if req.WantReply {
		req.Reply(dx, nil)
	}

	meta := map[string]interface{}{
		"type":    "reply",
		"name":    req.Type,
		"payload": req.Payload,
	}

	c.Aux().Emit(meta)
}

func proxyChannel(c *ConnInsight, mcha ssh.Channel, mreq <-chan *ssh.Request, master ssh.NewChannel, client *SSHClient, killer <-chan struct{}) error {

	do := new(sync.Once)
	cochan, coreq, err := client.OpenChannel(master.ChannelType(), master.ExtraData())

	checkError(err, fmt.Sprintf("Creating Client Channel for %s", client.RemoteAddr().String()))

	if err != nil {
		return err
	}

	stop := make(chan struct{})
	endClose := func() { close(stop) }

	flux.GoDefer("proxyChannelCopy", func() {
		defer cochan.Close()
		defer mcha.Close()

		func() {
		ploop:
			for {
				select {
				case <-stop:
					break ploop
				case <-killer:
					break ploop
				case slx, ok := <-coreq:
					if !ok {
						return
					}

					Reply(slx, mcha, c)

					switch slx.Type {
					case "exit-status":
						break ploop
					}

				case mlx, ok := <-mreq:
					if !ok {
						return
					}

					Reply(mlx, cochan, c)

					switch mlx.Type {
					case "exit-status":
						break ploop
					}
				}
			}
		}()
	})

	mastercloser := io.ReadCloser(mcha)
	slavecloser := io.ReadCloser(cochan)

	wrapmaster := io.MultiWriter(mcha, c.Out())
	wrapsl := io.MultiWriter(cochan, c.In())

	flux.GoDefer("CopyToSlave", func() {
		defer do.Do(endClose)
		io.Copy(wrapsl, mastercloser)
	})

	flux.GoDefer("CopyToMaster", func() {
		defer do.Do(endClose)
		io.Copy(wrapmaster, slavecloser)
	})

	flux.GoDefer("CopyCloser", func() {
		defer c.Close()

		<-stop

		mx := mastercloser.Close()
		checkError(mx, "Master Writer Closer")

		sx := slavecloser.Close()
		checkError(sx, "Slave Writer Closer")

		ex := client.Close()
		checkError(ex, "Client Writer Closer")
	})

	return nil
}
