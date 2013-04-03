/*
 * Copyright 2012 Nan Deng
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package proto

import (
	"testing"
	"net"
	"crypto/rsa"
	"crypto/rand"
	"time"
)

func serverGetOneClient(addr string) (conn net.Conn, err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return
	}
	defer ln.Close()
	conn, err = ln.Accept()
	if err != nil {
		return
	}
	return
}

func clientConnectServer(addr string) (conn net.Conn, err error) {
	conn, err = net.Dial("tcp", addr)
	if err != nil {
		return
	}
	return
}

func buildServerClient(addr string) (server net.Conn, client net.Conn, err error) {
	ch := make(chan error)
	go func() {
		var e error
		client, e = serverGetOneClient(addr)
		ch <- e
	}()
	// It is enough to setup a server for a test.
	time.Sleep(1 * time.Second)
	server, err = clientConnectServer(addr)
	if err != nil {
		return
	}
	err = <-ch
	if err != nil {
		return
	}
	return
}

func exchangeKeysOrReport(t *testing.T) (serverKeySet, clientKeySet *keySet) {
	addr := "127.0.0.1:8080"
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Errorf("Error: %v", err)
	}
	pub := &priv.PublicKey
	server, client, err := buildServerClient(addr)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}
	if server == nil || client == nil {
		t.Errorf("Nil pointer: server=%v; client=%v", server, client)
		return
	}
	var es error
	var ec error
	ch := make(chan bool)
	go func() {
		serverKeySet, es = serverKeyExchange(priv, client)
		ch <- true
	}()
	go func() {
		clientKeySet, ec = clientKeyExchange(pub, server)
		ch <- true
	}()
	<-ch
	<-ch
	if es != nil {
		t.Errorf("Error from server: %v", es)
	}
	if ec != nil {
		t.Errorf("Error from client: %v", ec)
	}
	if !serverKeySet.eq(clientKeySet) {
		t.Errorf("Not equal")
	}
	return
}

func TestKeyExchange(t *testing.T) {
	exchangeKeysOrReport(t)
}
