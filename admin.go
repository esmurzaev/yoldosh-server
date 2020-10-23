package main

import (
	"encoding/binary"
	"net"
	"sync/atomic"
	"time"
)

var (
	clientsNum                auint32 // Total connected clients
	driversNum                auint32 // Total connected drivers
	statsClientsSuccessConns  auint64 // Counter for total client successful authentications
	statsClientsFailedConns   auint64 // Counter for total client failed authentications
	statsClientsFailedPackets auint64 // Counter for total client failed packets
	statsDriversSuccessConns  auint64 // Counter for total driver successful authentications
	statsDriversFailedConns   auint64 // Counter for total driver failed authentications
	statsDriversFailedPackets auint64 // Counter for total driver failed packets
	statsDriversCancelMatches auint64 // Counter for total driver cancelled matches
	statsClientsFailedMatches auint64 // Counter for total driver failed matches
	statsFoundedMatches       auint64 // Counter for total founded matches
	statsSuccessMatches       auint64 // Counter for total successful matches
)

type auint64 struct {
	value uint64
}

func (a *auint64) add() {
	atomic.AddUint64(&a.value, uint64(1))
}

func (a *auint64) delete() {
	atomic.AddUint64(&a.value, ^uint64(0))
}

func (a *auint64) get() uint64 {
	return atomic.LoadUint64(&a.value)
}

type auint32 struct {
	value uint32
}

func (a *auint32) add() {
	atomic.AddUint32(&a.value, uint32(1))
}

func (a *auint32) delete() {
	atomic.AddUint32(&a.value, ^uint32(0))
}

func (a *auint32) get() uint32 {
	return atomic.LoadUint32(&a.value)
}

func listenAndServeAdmin(addr string) error {
	lAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return err
	}
	ln, err := net.ListenTCP("tcp", lAddr)
	if err != nil {
		return err
	}
	defer func() {
		ln.Close()
	}()
	var tempDelay time.Duration

	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		go handleAdmin(conn)
	}
}

func handleAdmin(conn *net.TCPConn) {
	if !authAdmin(conn) {
		conn.Close()
		return
	}

	defer func() {
		conn.Close()
	}()

	buf := make([]byte, 255)

	for {
		length, err := conn.Read(buf[:3])
		if err != nil || buf[0] != 0x47 || length != 3 {
			return
		}
		switch buf[1] {

		case 0x01: // Number of connected users and stats get
			n := binary.PutUvarint(buf[3:], uint64(clientsNum.get()))
			n += binary.PutUvarint(buf[n:], uint64(driversNum.get()))
			n += binary.PutUvarint(buf[n:], statsClientsSuccessConns.get())
			n += binary.PutUvarint(buf[n:], statsClientsFailedConns.get())
			n += binary.PutUvarint(buf[n:], statsClientsFailedPackets.get())
			n += binary.PutUvarint(buf[n:], statsDriversSuccessConns.get())
			n += binary.PutUvarint(buf[n:], statsDriversFailedConns.get())
			n += binary.PutUvarint(buf[n:], statsDriversFailedPackets.get())
			n += binary.PutUvarint(buf[n:], statsClientsFailedMatches.get())
			n += binary.PutUvarint(buf[n:], statsFoundedMatches.get())
			n += binary.PutUvarint(buf[n:], statsSuccessMatches.get())
			n += 3
			buf[0] = 0x20
			buf[2] = byte(n)
			if _, err = conn.Write(buf[:n]); err != nil {
				return
			}

		default:
			return
		}
	}
}
