package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"net"
	"time"
)

func auth(conn *net.TCPConn) bool {
	buf := make([]byte, 17)
	length, err := conn.Read(buf)
	if length != 17 || err != nil {
		return false
	}

	if buf[0] != apiVersion {
		buf[0] = 0x40
		buf[1] = 0x01
		conn.Write(buf[:2]) // API version error
		return false
	}

	buf = buf[1:]
	// Decrypt packet
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return false
	}
	iv := make([]byte, aes.BlockSize)
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(buf, buf)
	// Code check
	code := binary.BigEndian.Uint64(buf[:8])
	if code != masterCode {
		return false
	}
	// Timestamp check
	stamp := binary.BigEndian.Uint32(buf[8:12])
	t := time.Now()
	s := uint32(t.Unix())
	if stamp-180 > s || stamp+180 < s {
		buf[0] = 0x40
		buf[1] = 0x02
		conn.Write(buf[:2]) // User timestamp error
		return false
	}
	// Conn configuration
	conn.SetDeadline(t.Add(120 * time.Minute))
	return true
}

func authAdmin(conn *net.TCPConn) bool {
	buf := make([]byte, 17)
	length, err := conn.Read(buf)
	if length != 17 || err != nil {
		return false
	}

	if buf[0] != 0x0A {
		buf[0] = 0x40
		buf[1] = 0x01
		conn.Write(buf[:2]) // API version error
		return false
	}

	buf = buf[1:]
	// Decrypt packet
	block, _ := aes.NewCipher(adminMasterKey)
	block.Decrypt(buf, buf)
	// Code check
	code := binary.BigEndian.Uint64(buf[:8])
	if code != adminMasterCode {
		return false
	}
	// Timestamp check
	stamp := binary.BigEndian.Uint32(buf[8:12])
	t := time.Now()
	s := uint32(t.Unix())
	if stamp-60 > s || stamp+60 < s {
		buf[0] = 0x40
		buf[1] = 0x02
		conn.Write(buf[:2]) // Admin timestamp error
		return false
	}
	// Conn configuration
	conn.SetDeadline(t.Add(24 * time.Hour))
	return true
}
