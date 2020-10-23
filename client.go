package main

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"
)

//-------------------------------------------------------------------------------------------------------
// Данные клиентов

type client struct {
	conn         *net.TCPConn
	id           uint32
	matchTime    uint32  // For statics
	match        bool    // Route match status
	tariff       byte    //
	seat         byte    //
	posit        [8]byte // Client current position
	destPlaceIdx uint16  // Client destination place index
}

var (
	clientID            uint32
	clientsMutex        sync.RWMutex
	pointedClientsFlag  [(1 << 16) / 8]byte
	routedClientsFlag   [(1 << 32) / 8]byte
	pointedClientsCount = make(map[uint16]byte, clientsMaxNum)
	routedClients       = make(map[uint32][]*client, clientsMaxNum)
)

//-------------------------------------------------------------------------------------------------------

func listenAndServeClient(addr string) error {
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
		go handleClient(conn)
	}
}

func handleClient(conn *net.TCPConn) {
	if clientsNum.get() >= clientsMaxNum {
		buf := make([]byte, 2)
		buf[0] = 0x50
		buf[1] = 0x01
		conn.Write(buf) // Server busy error
		conn.Close()
		return
	}
	if !auth(conn) {
		conn.Close()
		statsClientsFailedConns.add()
		return
	}

	var (
		connCloseFlag bool
		route         uint32
		nodeA         uint16
		nodeBs        []uint16
		buf           = make([]byte, 2)
		cl            = &client{
			conn: conn,
		}
	)

	statsClientsSuccessConns.add()
	clientsNum.add()

	defer func() {
		if !connCloseFlag {
			conn.Close()
			statsClientsFailedPackets.add()
		}
		if nodeA > 0 {
			route = uint32(nodeA) << 16
			length := len(nodeBs)
			for i := 0; i < length; i++ {
				route = route&0xFFFF0000 | uint32(nodeBs[i])
				clientRouteDelete(route, cl.id)
			}
		}
		clientsNum.delete()
	}()

	for {
		length, err := conn.Read(buf)
		if err != nil || length != 2 {
			if err == io.EOF {
				connCloseFlag = true
			}
			return
		}

		switch buf[0] {

		case 0x11: // Route add
			nodeBsNum := int(buf[1])
			nLen := (nodeBsNum * 2) + 13
			tmpBuf := make([]byte, nLen)
			length, err = conn.Read(tmpBuf)
			if err != nil || length != nLen {
				return
			}
			cl.seat = tmpBuf[0] & 0x0F
			cl.tariff = tmpBuf[0] >> 4
			copy(cl.posit[:], tmpBuf[1:9])
			cl.destPlaceIdx = binary.BigEndian.Uint16(tmpBuf[9:11])
			if nodeA > 0 {
				route = uint32(nodeA) << 16
				length = len(nodeBs)
				for i := 0; i < length; i++ {
					route = route&0xFFFF0000 | uint32(nodeBs[i])
					clientRouteDelete(route, cl.id)
				}
				cl.match = false
			}
			nodeA = binary.BigEndian.Uint16(tmpBuf[11:13])
			route = uint32(nodeA) << 16
			nodeBs = make([]uint16, nodeBsNum)
			for i, j := 0, 13; i < nodeBsNum; i++ {
				nodeBs[i] = binary.BigEndian.Uint16(tmpBuf[j:])
				route = route&0xFFFF0000 | uint32(nodeBs[i])
				clientRouteAdd(route, cl)
				j += 2
			}
			tmpBuf = nil
			buf[0] = 0x21
			buf[1] = 0x01
			conn.Write(buf[:2])

		case 0x12: // Description update
			cl.seat = buf[1] & 0x0F
			cl.tariff = buf[1] >> 4

		case 0x13: // Route match status
			switch buf[1] {
			case 0x01: // Route match successful
				t := uint32(time.Now().Unix())
				if cl.matchTime > t-60 {
					statsSuccessMatches.add()
				} else {
					cl.matchTime = t
				}
				connCloseFlag = true
				return
			case 0x02: // Route match failed
				cl.match = false
				statsClientsFailedMatches.add()
			default:
				return
			}
		default:
			return
		}
	}
}

//-------------------------------------------------------------------------------------------------------

func pointedClientsFlagGetAndSet(offset uint16) bool {
	index, pos := offset/8, offset%8
	if ((pointedClientsFlag[index] >> pos) & 1) == 0 {
		pointedClientsFlag[index] |= 1 << pos
		return false
	}
	return true
}

func pointedClientsFlagGet(offset uint16) bool {
	index, pos := offset/8, offset%8
	return ((pointedClientsFlag[index] >> pos) & 1) != 0
}

func pointedClientsFlagClear(offset uint16) {
	index, pos := offset/8, offset%8
	pointedClientsFlag[index] &^= 1 << pos
}

//-------------------------------------------------------------------------------------------------------

func routedClientsFlagGetAndSet(offset uint32) bool {
	index, pos := offset/8, offset%8
	if ((routedClientsFlag[index] >> pos) & 1) == 0 {
		routedClientsFlag[index] |= 1 << pos
		return false
	}
	return true
}

func routedClientsFlagGet(offset uint32) bool {
	index, pos := offset/8, offset%8
	return ((routedClientsFlag[index] >> pos) & 1) != 0
}

func routedClientsFlagClear(offset uint32) {
	index, pos := offset/8, offset%8
	routedClientsFlag[index] &^= 1 << pos
}

//-------------------------------------------------------------------------------------------------------
// Устанавливает идентификатор клиента.
// Устанавливает флаг маршрута,
// и добавляеть клиента в карты маршрутов.
// Устанавливает флаг точки нахождения,
// если флаг уже установлен, то инкрементирует счетчик точки нахождения.

func clientRouteAdd(route uint32, cl *client) {
	clientsMutex.Lock()
	if cl.id == 0 {
		clientID++
		cl.id = clientID
	}
	nodeA := uint16(route >> 16)
	if pointedClientsFlagGetAndSet(nodeA) {
		pointedClientsCount[nodeA]++
	}
	if routedClientsFlagGetAndSet(route) {
		clients := routedClients[route]
		clients = append(clients, cl)
		routedClients[route] = clients
	} else {
		routedClients[route] = []*client{cl}
	}
	clientsMutex.Unlock()
}

// Если счетчик точки равно ноль, то удаляеть флаг точки нахождения,
// флаг маршрута, клиента из карты маршрутов, счетчика точки,
// иначе декрементирует счетчик точки, и удаляеть клиента из карты маршрутов,
// если клиентов в карты маршрутов не больше 1, то удаляеть флаг маршрута.

func clientRouteDelete(route, id uint32) {
	nodeA := uint16(route >> 16)
	clientsMutex.Lock()
	num, ok := pointedClientsCount[nodeA]
	if num == 0 {
		pointedClientsFlagClear(nodeA)
		routedClientsFlagClear(route)
		delete(routedClients, route)
		if ok {
			delete(pointedClientsCount, nodeA)
		}
	} else {
		pointedClientsCount[nodeA]--
		clients := routedClients[route]
		if len(clients) <= 1 {
			routedClientsFlagClear(route)
			delete(routedClients, route)
		} else {
			for i, cl := range clients {
				if cl.id == id {
					if i > 0 {
						copy(clients[1:], clients[:i])
					}
					clients = clients[1:]
					routedClients[route] = clients
					break
				}
			}
		}
	}
	clientsMutex.Unlock()
}
