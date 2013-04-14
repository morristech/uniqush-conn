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
	"encoding/json"
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/uniqush/uniqush-conn/proto"
	"time"
)

type redisMessageCache struct {
	pool *redis.Pool
}

func NewRedisMessageCache(addr, password string, db int) Cache {
	if len(addr) == 0 {
		addr = "localhost:6379"
	}
	if db < 0 {
		db = 0
	}

	dial := func() (redis.Conn, error) {
		c, err := redis.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		if len(password) > 0 {
			if _, err := c.Do("AUTH", password); err != nil {
				c.Close()
				return nil, err
			}
		}
		if _, err := c.Do("SELECT", db); err != nil {
			c.Close()
			return nil, err
		}
		return c, err
	}
	testOnBorrow := func(c redis.Conn, t time.Time) error {
		_, err := c.Do("PING")
		return err
	}

	pool := &redis.Pool{
		MaxIdle:      3,
		IdleTimeout:  240 * time.Second,
		Dial:         dial,
		TestOnBorrow: testOnBorrow,
	}

	ret := new(redisMessageCache)
	ret.pool = pool
	return ret
}

func msgKey(service, username, id string) string {
	return fmt.Sprintf("mcache:%v:%v:%v", service, username, id)
}

func msgMarshal(msg *proto.Message) (data []byte, err error) {
	data, err = json.Marshal(msg)
	return
}

func msgUnmarshal(data []byte) (msg *proto.Message, err error) {
	msg = new(proto.Message)
	err = json.Unmarshal(data, msg)
	if err != nil {
		msg = nil
		return
	}
	return
}

func (self *redisMessageCache) Set(service, username, id string, msg *proto.Message, ttl time.Duration) error {
	key := msgKey(service, username, id)
	conn := self.pool.Get()
	defer conn.Close()

	data, err := msgMarshal(msg)
	if err != nil {
		return err
	}

	if ttl.Seconds() <= 0.0 {
		_, err = conn.Do("SET", key, data)
	} else {
		_, err = conn.Do("SETEX", key, int64(ttl.Seconds()), data)
	}
	if err != nil {
		return err
	}
	return nil
}

func (self *redisMessageCache) Get(service, username, id string) (msg *proto.Message, err error) {
	key := msgKey(service, username, id)
	conn := self.pool.Get()
	defer conn.Close()

	reply, err := conn.Do("GET", key)
	if err != nil {
		return
	}
	data, err := redis.Bytes(reply, err)
	if err != nil {
		return
	}
	msg, err = msgUnmarshal(data)
	return
}

func (self *redisMessageCache) Del(service, username, id string) (msg *proto.Message, err error) {
	key := msgKey(service, username, id)
	conn := self.pool.Get()
	defer conn.Close()

	err = conn.Send("MULTI")
	if err != nil {
		return
	}
	err = conn.Send("GET", key)
	if err != nil {
		conn.Do("DISCARD")
		return
	}
	err = conn.Send("DEL", key)
	if err != nil {
		conn.Do("DISCARD")
		return
	}
	reply,  err := conn.Do("EXEC")
	if err != nil {
		return
	}

	bulkReply, err := redis.Values(reply, err)
	if err != nil {
		return
	}
	if len(bulkReply) != 2 {
		return
	}
	data, err := redis.Bytes(bulkReply[0], err)
	if err != nil {
		return
	}
	if len(data) == 0 {
		return
	}
	msg, err = msgUnmarshal(data)
	return
}

