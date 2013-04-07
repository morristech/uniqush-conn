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
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"testing"
	"time"
)

type fakeAuthorizer struct {
}

func (self *fakeAuthorizer) Authenticate(service, name, token string) (bool, error) {
	if service != "service" {
		return false, nil
	}
	if name != "username" {
		return false, nil
	}
	if token != "token" {
		return false, nil
	}
	return true, nil
}

func testMessageExchange(addr string, token string, msgs ...*Message) error {
	c2sConn, s2cConn, err := buildServerClient(addr)
	if err != nil {
		return err
	}
	defer c2sConn.Close()
	defer s2cConn.Close()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	pub := &priv.PublicKey

	ch := make(chan bool)
	var ec error
	var es error
	go func() {
		defer func() {
			ch <- true
		}()
		cliConn, err := Dial(c2sConn, pub, "service", "username", token)
		if err != nil {
			es = fmt.Errorf("Client: Auth error: %v", err)
			return
		}
		for i, msg := range msgs {
			m, err := cliConn.ReadMessage()
			if err != nil {
				ec = err
				return
			}
			if !m.Eq(msg) {
				ec = fmt.Errorf("%vth message corrupted", i)
				return
			}
		}
		ec = nil
	}()
	go func() {
		defer func() {
			ch <- true
		}()
		auth := &fakeAuthorizer{}
		servConn, err := AuthConn(s2cConn, priv, auth, 800*time.Millisecond)
		if err != nil {
			es = fmt.Errorf("Server: Auth error: %v", err)
			return
		}
		// Make sure we have cleared the deadline
		time.Sleep(1 * time.Second)
		for _, msg := range msgs {
			err := servConn.WriteMessage(msg, true, true)
			if err != nil {
				es = err
				return
			}
		}
	}()
	i := 0
	for _ = range ch {
		if es != nil {
			return es
		}
		if ec != nil {
			return ec
		}
		i++
		if i >= 2 {
			break
		}
	}
	return nil
}

func randomMessage() *Message {
	msg := new(Message)
	msg.Body = make([]byte, 10)
	io.ReadFull(rand.Reader, msg.Body)
	msg.Header = make(map[string]string, 2)
	msg.Header["aaa"] = "hello"
	msg.Header["aa"] = "hell"
	return msg
}

func TestExchangingSingleMessage(t *testing.T) {
	msg := randomMessage()
	err := testMessageExchange("127.0.0.1:8088", "token", msg)
	if err != nil {
		t.Errorf("%v", err)
	}
	return
}

func TestAuthFail(t *testing.T) {
	msg := randomMessage()
	err := testMessageExchange("127.0.0.1:8088", "wrong token", msg)
	if err == nil {
		t.Errorf("Should auth faile")
	}
	return
}