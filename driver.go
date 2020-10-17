package main

import (
	"encoding/binary"
	"io"
	"net"
	"time"
)

func serveDriver(addr string) error {
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
		go handleDriver(conn)
	}
}

func handleDriver(conn *net.TCPConn) {
	if driversNum.get() >= driversMaxNum {
		buf := make([]byte, 2)
		buf[0] = 0x50
		buf[1] = 0x01
		conn.Write(buf) // Server busy error
		conn.Close()
		return
	}
	if !auth(conn) {
		conn.Close()
		statsDriversFailedConns.add()
		return
	}

	var (
		connCloseFlag   bool
		seat            byte
		tariff          byte
		matchClientsNum byte
		nodesNum        byte
		lastNodeSeq     byte = 255
		carInfo         [2]byte
		nodes           []uint16
		buf             = make([]byte, 43)
		matchClients    []*client
		matchClientsOld []*client
	)

	statsDriversSuccessConns.add()
	driversNum.add()

	defer func() {
		if connCloseFlag {
			conn.Close()
		} else {
			statsDriversFailedPackets.add()
		}
		driversNum.delete()
	}()

	for {
		length, err := conn.Read(buf[:2])
		if err != nil || length != 2 {
			if err == io.EOF {
				connCloseFlag = true
			}
			return
		}

		switch buf[0] {

		case 0x14: // Route match search
			// Если в точке нахождения есть клиенты, поиск клиента по маршруту,
			// при совпадения маршрута, оповещения перевозчика и клиента
			nodesSeq := buf[1]
			if lastNodeSeq == nodesSeq {
				continue
			}
			lastNodeSeq = nodesSeq
			node := nodes[nodesSeq]
			if !pointedClientsFlagGet(node) {
				continue
			}
			route := uint32(node) << 16
			for i := nodesNum; i > nodesSeq; i-- {
				route = route&0xFFFF0000 | uint32(nodes[i])
				if !routedClientsFlagGet(route) {
					continue
				}
				clientsMutex.RLock()
				clients, ok := routedClients[route]
				clientsMutex.RUnlock()
				if !ok {
					continue
				}
				for _, cl := range clients {
					if !cl.match && seat >= cl.seat && tariff <= cl.tariff {
						cl.match = true
						matchClients = append(matchClients, cl)
						matchClientsNum++
						if seat-cl.seat == 0 || matchClientsNum == 4 {
							goto match
						}
					}
				}
			}
			if matchClientsNum == 0 {
				continue
			}
		match:
			buf[0] = 0x22
			buf[1] = 0x01
			buf[2] = matchClientsNum
			i := 3
			for _, cl := range matchClients {
				copy(buf[i:i+8], cl.posit[:])
				i += 8
				binary.BigEndian.PutUint16(buf[i:i+2], cl.destPlaceIdx)
				i += 2
			}
			if _, err := conn.Write(buf[:i]); err != nil { // Route match successful, to driver
				for _, cl := range matchClients {
					cl.match = false
				}
				continue
			}
			buf[2] = tariff
			copy(buf[3:], carInfo[:])
			for i, cl := range matchClients {
				go func(clConn *net.TCPConn, i int, buf []byte) {
					_, err := clConn.Write(buf) // Route match successful, to client
					if err != nil {
						buf[1] = 0x02
						buf[2] = byte(i)    // Failed client index
						conn.Write(buf[:3]) // Route match failed, to driver
						clConn.SetReadDeadline(time.Time{})
					}
				}(cl.conn, i, buf[:5])
				statsFoundedMatches.add()
			}
			matchClientsOld = make([]*client, matchClientsNum)
			copy(matchClientsOld, matchClients)
			matchClients = nil
			matchClientsNum = 0
			continue

		case 0x11: // Route nodes set
			nodesNum = buf[1]
			wpLen := (int(nodesNum) * 2) + 3
			tmpBuf := make([]byte, wpLen)
			length, err := conn.Read(tmpBuf)
			if err != nil || length != wpLen {
				return
			}
			if seat == 0 {
				seat = tmpBuf[0] & 0x0F
				tariff = tmpBuf[0] >> 4
				copy(carInfo[:], tmpBuf[1:3])
			}
			nodes = make([]uint16, nodesNum)
			for i, j := 0, 3; i < int(nodesNum); i++ {
				nodes[i] = binary.BigEndian.Uint16(tmpBuf[j:])
				j += 2
			}
			tmpBuf = nil
			nodesNum-- // For last index of nodes
			buf[0] = 0x21
			buf[1] = 0x01
			conn.Write(buf[:2])

		case 0x12: // Description update
			seat = buf[1] & 0x03
			tariff = buf[1] << 4

		case 0x13: // Route match status
			switch buf[1] {
			case 0x01: // Route match confirmed
				t := uint32(time.Now().Unix())
				for _, cl := range matchClientsOld {
					if cl.matchTime > t-60 {
						statsSuccessMatches.add()
					} else {
						cl.matchTime = t
					}
				}
			case 0x02: // Route match cancelled
				buf[0] = 0x22
				buf[1] = 0x02
				for _, cl := range matchClientsOld {
					cl.match = false
					go func(clConn *net.TCPConn, buf []byte) {
						_, err := clConn.Write(buf)
						if err != nil { // Route match aborted, to client
							clConn.SetReadDeadline(time.Time{})
						}
					}(cl.conn, buf[:2])
				}
				statsDriversCancelMatches.add()
			default:
				return
			}
			matchClientsOld = nil

		default:
			return
		}
	}
}
