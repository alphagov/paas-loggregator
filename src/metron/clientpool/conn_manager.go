package clientpool

import (
	"errors"
	"io"
	"log"
	"plumbing"
	"sync/atomic"
	"time"
	"unsafe"
)

type ConnManager struct {
	conn      unsafe.Pointer
	maxWrites int64
	dialer    Dialer
}

type grpcConn struct {
	addr   string
	client plumbing.DopplerIngestor_PusherClient
	closer io.Closer
	writes int64
}

func NewConnManager(d Dialer, maxWrites int64) *ConnManager {
	m := &ConnManager{
		maxWrites: maxWrites,
		dialer:    d,
	}
	go m.maintainConn()
	return m
}

func (m *ConnManager) Write(data []byte) error {
	conn := atomic.LoadPointer(&m.conn)
	if conn == nil || (*grpcConn)(conn) == nil {
		return errors.New("no connection to doppler present")
	}

	gRPCConn := (*grpcConn)(conn)
	err := gRPCConn.client.Send(&plumbing.EnvelopeData{
		Payload: data,
	})

	// TODO: This block is untested because we don't know how to
	// induce an error from the stream via the test
	if err != nil {
		log.Printf("error writing to doppler %s: %s", gRPCConn.addr, err)
		atomic.StorePointer(&m.conn, nil)
		gRPCConn.closer.Close()
		return err
	}

	if atomic.AddInt64(&gRPCConn.writes, 1) >= m.maxWrites {
		log.Printf("recycling connection to doppler %s after %d writes", gRPCConn.addr, m.maxWrites)
		atomic.StorePointer(&m.conn, nil)
		gRPCConn.closer.Close()
	}

	return nil
}

func (m *ConnManager) maintainConn() {
	for range time.Tick(50 * time.Millisecond) {
		conn := atomic.LoadPointer(&m.conn)
		if conn != nil && (*grpcConn)(conn) != nil {
			continue
		}

		closer, pusherClient, err := m.dialer.Dial()
		if err != nil {
			log.Printf("error dialing doppler %s: %s", m.dialer, err)
			continue
		}

		atomic.StorePointer(&m.conn, unsafe.Pointer(&grpcConn{
			addr:   m.dialer.String(),
			client: pusherClient,
			closer: closer,
		}))
	}
}
