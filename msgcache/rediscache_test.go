/*
 * Copyright 2013 Nan Deng
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

package msgcache

import (
	"crypto/rand"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/uniqush/uniqush-conn/proto"
	"io"
	"testing"
	"time"
)

func randomMessage() *proto.Message {
	msg := new(proto.Message)
	msg.Body = make([]byte, 10)
	io.ReadFull(rand.Reader, msg.Body)
	msg.Header = make(map[string]string, 2)
	msg.Header["aaa"] = "hello"
	msg.Header["aa"] = "hell"
	return msg
}

func multiRandomMessage(N int) []*proto.Message {
	msgs := make([]*proto.Message, N)
	for i := 0; i < N; i++ {
		msgs[i] = randomMessage()
	}
	return msgs
}

func getCache() Cache {
	db := 1
	c, _ := redis.Dial("tcp", "localhost:6379")
	c.Do("SELECT", db)
	c.Do("FLUSHDB")
	c.Close()
	return NewRedisMessageCache("", "", db)
}

func TestGetSetMessage(t *testing.T) {
	N := 10
	msgs := multiRandomMessage(N)
	cache := getCache()
	srv := "srv"
	usr := "usr"

	ids := make([]string, N)

	for i, msg := range msgs {
		id, err := cache.CacheMessage(srv, usr, msg, 0*time.Second)
		if err != nil {
			t.Errorf("Set error: %v", err)
			return
		}
		ids[i] = id
	}
	for i, msg := range msgs {
		m, err := cache.GetThenDel(srv, usr, ids[i])
		if err != nil {
			t.Errorf("Del error: %v", err)
			return
		}
		if !m.Eq(msg) {
			t.Errorf("%vth message does not same", i)
		}
	}
	for i, id := range ids {
		m, err := cache.GetThenDel(srv, usr, id)
		if err != nil {
			t.Errorf("Get error: %v", err)
			return
		}
		if m != nil {
			t.Errorf("%vth message should be deleted", i)
		}
	}

}

func TestGetSetMessageTTL(t *testing.T) {
	N := 10
	msgs := multiRandomMessage(N)
	cache := getCache()
	srv := "srv"
	usr := "usr"

	ids := make([]string, N)

	for i, msg := range msgs {
		id, err := cache.CacheMessage(srv, usr, msg, 1*time.Second)
		if err != nil {
			t.Errorf("Set error: %v", err)
			return
		}
		ids[i] = id
	}
	time.Sleep(2 * time.Second)
	for i, id := range ids {
		m, err := cache.GetThenDel(srv, usr, id)
		if err != nil {
			t.Errorf("Get error: %v", err)
			return
		}
		if m != nil {
			t.Errorf("%vth message should be deleted", i)
		}
	}
}

func strSetEq(a, b []string) bool {
	if len(a) != len(b) {
		fmt.Printf("Different size\n")
		return false
	}
	for _, s := range a {
		found := false
		for _, t := range b {
			if s == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestCacheThenRetrieveIds(t *testing.T) {
	N := 10
	msgs := multiRandomMessage(N)
	cache := getCache()
	srv := "srv"
	usr := "usr"

	ids := make([]string, N)

	for i, msg := range msgs {
		id, err := cache.CacheMessage(srv, usr, msg, 0*time.Second)
		if err != nil {
			t.Errorf("Set error: %v", err)
			return
		}
		ids[i] = id
	}

	idShadows, err := cache.GetAllIds(srv, usr)
	if err != nil {
		t.Errorf("Set error: %v", err)
		return
	}
	if !strSetEq(idShadows, ids) {
		t.Errorf("retrieved different ids: %v != %v", idShadows, ids)
		return
	}
}

func TestGetNonExistMsg(t *testing.T) {
	cache := getCache()
	srv := "srv"
	usr := "usr"

	msg, err := cache.GetThenDel(srv, usr, "wont-be-a-good-message-id")
	if err != nil {
		t.Errorf("%v", err)
		return
	}
	if msg != nil {
		t.Errorf("should be nil message")
		return
	}
}
