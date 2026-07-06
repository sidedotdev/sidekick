package iroh

import (
	"context"
	"net"
	"sync"

	"github.com/tmc/go-iroh/iroh"
)

// listener adapts an iroh endpoint to the net.Listener interface. It accepts
// incoming iroh connections and surfaces each of their bidirectional streams as
// an independent net.Conn, so a standard http.Server can serve requests over
// iroh with one HTTP request/response per stream.
type listener struct {
	ep     *iroh.Endpoint
	conns  chan net.Conn
	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	errOnce   sync.Once
	acceptErr error
}

func newListener(ep *iroh.Endpoint) *listener {
	ctx, cancel := context.WithCancel(context.Background())
	l := &listener{
		ep:     ep,
		conns:  make(chan net.Conn),
		ctx:    ctx,
		cancel: cancel,
	}
	go l.acceptConns()
	return l
}

func (l *listener) acceptConns() {
	for {
		conn, err := l.ep.Accept(l.ctx)
		if err != nil {
			l.fail(err)
			return
		}
		go l.acceptStreams(conn)
	}
}

func (l *listener) acceptStreams(conn *iroh.Conn) {
	defer conn.Close()
	for {
		streamConn, err := conn.AcceptStreamConn(l.ctx)
		if err != nil {
			return
		}
		select {
		case l.conns <- streamConn:
		case <-l.ctx.Done():
			return
		}
	}
}

func (l *listener) fail(err error) {
	l.errOnce.Do(func() {
		l.acceptErr = err
	})
	l.cancel()
}

// Accept returns the next iroh stream as a net.Conn.
func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.ctx.Done():
		if l.acceptErr != nil {
			return nil, l.acceptErr
		}
		return nil, net.ErrClosed
	}
}

// Close stops accepting new connections and streams.
func (l *listener) Close() error {
	l.closeOnce.Do(l.cancel)
	return nil
}

// Addr returns the iroh node identity this listener accepts connections for.
func (l *listener) Addr() net.Addr {
	return endpointAddr{id: l.ep.ID().String()}
}

// endpointAddr is a net.Addr describing an iroh endpoint by its node identity.
type endpointAddr struct {
	id string
}

func (a endpointAddr) Network() string { return "iroh" }
func (a endpointAddr) String() string  { return a.id }
