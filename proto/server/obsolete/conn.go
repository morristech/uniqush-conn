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

package server

import (
	"fmt"
	"github.com/uniqush/uniqush-conn/msgcache"
	"github.com/uniqush/uniqush-conn/proto"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type SubscribeRequest struct {
	Subscribe bool // false: unsubscribe; true: subscribe
	Service   string
	Username  string
	Params    map[string]string
}

type ForwardRequest struct {
	Receiver        string         `json:"receiver"`
	ReceiverService string         `json:"service"`
	TTL             time.Duration  `json:"ttl"`
	Message         *proto.Message `json:"msg"`
}

type Conn interface {
	// Send the message to client.
	// If the message is larger than the digest threshold,
	// then send a digest to the client.
	SendMessage(msg *proto.Message, extra map[string]string, ttl time.Duration, id string) error
	SetMessageCache(cache msgcache.Cache)
	SetForwardRequestChannel(fwdChan chan<- *ForwardRequest)
	SetSubscribeRequestChan(subChan chan<- *SubscribeRequest)
	Visible() bool
	proto.Conn
}

type serverConn struct {
	proto.Conn
	cmdio             *proto.CommandIO
	digestThreshold   int32
	compressThreshold int32
	visible           int32
	digestFielsLock   sync.Mutex
	digestFields      []string
	mcache            msgcache.Cache
	fwdChan           chan<- *ForwardRequest
	subChan           chan<- *SubscribeRequest
}

func (self *serverConn) Visible() bool {
	v := atomic.LoadInt32(&self.visible)
	return v > 0
}

func (self *serverConn) SetForwardRequestChannel(fwdChan chan<- *ForwardRequest) {
	self.fwdChan = fwdChan
}

func (self *serverConn) SetSubscribeRequestChan(subChan chan<- *SubscribeRequest) {
	self.subChan = subChan
}

func (self *serverConn) shouldDigest(msg *proto.Message) (sz int, sendDigest bool) {
	sz = msg.Size()
	d := atomic.LoadInt32(&self.digestThreshold)
	if d >= 0 && d < int32(sz) {
		sendDigest = true
	}
	return
}

func (self *serverConn) writeAutoCompress(msg *proto.Message, sz int) error {
	compress := false
	c := atomic.LoadInt32(&self.compressThreshold)
	if c > 0 && c < int32(sz) {
		compress = true
	}
	return self.WriteMessage(msg, compress)
}

func (self *serverConn) sendAllCachedMessage(excludes ...string) error {
	msgs, err := self.mcache.GetCachedMessages(self.Service(), self.Username(), excludes...)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		sz, sendDigest := self.shouldDigest(msg)
		if sendDigest {
			err = self.writeDigest(msg, nil, sz, msg.Id)
			if err != nil {
				return err
			}
		} else {
			err = self.writeAutoCompress(msg, sz)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (self *serverConn) SendMessage(msg *proto.Message, extra map[string]string, ttl time.Duration, id string) error {
	sz, sendDigest := self.shouldDigest(msg)
	if sendDigest {
		err := self.writeDigest(msg, extra, sz, id)
		if err != nil {
			return err
		}
		return nil
	}

	// Otherwise, send the message directly
	msg.Id = id
	err := self.writeAutoCompress(msg, sz)
	return err
}

func (self *serverConn) fromServer(msg *proto.Message) bool {
	if len(msg.Sender) == 0 {
		return true
	}
	if msg.Sender == self.Username() {
		if len(msg.SenderService) == 0 {
			return true
		}
		if msg.SenderService == self.Service() {
			return true
		}
	}
	return false
}

func (self *serverConn) writeDigest(msg *proto.Message, extra map[string]string, sz int, id string) (err error) {
	digest := new(proto.Command)
	digest.Type = proto.CMD_DIGEST
	digest.Params = make([]string, 2, 4)
	digest.Params[0] = fmt.Sprintf("%v", sz)
	digest.Params[1] = id

	if !self.fromServer(msg) {
		digest.Params = append(digest.Params, msg.Sender)
		digest.Params = append(digest.Params, msg.SenderService)
	}

	dmsg := new(proto.Message)

	header := make(map[string]string, len(extra))

	self.digestFielsLock.Lock()
	defer self.digestFielsLock.Unlock()
	for _, f := range self.digestFields {
		if len(msg.Header) > 0 {
			if v, ok := msg.Header[f]; ok {
				header[f] = v
			}
		}
		if len(extra) > 0 {
			if v, ok := extra[f]; ok {
				header[f] = v
			}
		}
	}
	if len(header) > 0 {
		dmsg.Header = header
		digest.Message = dmsg
	}

	compress := false
	c := atomic.LoadInt32(&self.compressThreshold)
	if c > 0 && c < int32(sz) {
		compress = true
	}

	err = self.cmdio.WriteCommand(digest, compress)
	if err != nil {
		return
	}
	return
}

func (self *serverConn) ProcessCommand(cmd *proto.Command) (msg *proto.Message, err error) {
	if cmd == nil {
		return
	}
	switch cmd.Type {
	case proto.CMD_SUBSCRIPTION:
		if self.subChan == nil {
			return
		}
		if len(cmd.Params) < 1 {
			err = proto.ErrBadPeerImpl
			return
		}
		if cmd.Message == nil {
			err = proto.ErrBadPeerImpl
			return
		}
		if len(cmd.Message.Header) == 0 {
			err = proto.ErrBadPeerImpl
			return
		}
		sub := true
		if cmd.Params[0] == "0" {
			sub = false
		} else if cmd.Params[0] == "1" {
			sub = true
		} else {
			return
		}
		req := new(SubscribeRequest)
		req.Params = cmd.Message.Header
		req.Service = self.Service()
		req.Username = self.Username()
		req.Subscribe = sub
		self.subChan <- req

	case proto.CMD_SET_VISIBILITY:
		if len(cmd.Params) < 1 {
			err = proto.ErrBadPeerImpl
			return
		}
		var v int32
		v = -1
		if cmd.Params[0] == "0" {
			v = 0
		} else if cmd.Params[0] == "1" {
			v = 1
		}
		if v >= 0 {
			atomic.StoreInt32(&self.visible, v)
		}
	case proto.CMD_FWD_REQ:
		if len(cmd.Params) < 2 {
			err = proto.ErrBadPeerImpl
			return
		}
		if self.fwdChan == nil {
			return
		}
		fwdreq := new(ForwardRequest)
		if cmd.Message == nil {
			cmd.Message = new(proto.Message)
		}
		cmd.Message.Sender = self.Username()
		cmd.Message.SenderService = self.Service()
		fwdreq.TTL, _ = time.ParseDuration(cmd.Params[0])
		fwdreq.Receiver = cmd.Params[1]
		if len(cmd.Params) > 2 {
			fwdreq.ReceiverService = cmd.Params[2]
		} else {
			fwdreq.ReceiverService = self.Service()
		}
		cmd.Message.Id = ""
		fwdreq.Message = cmd.Message
		self.fwdChan <- fwdreq
	case proto.CMD_SETTING:
		if len(cmd.Params) < 2 {
			err = proto.ErrBadPeerImpl
			return
		}
		if len(cmd.Params[0]) > 0 {
			var d int
			d, err = strconv.Atoi(cmd.Params[0])
			if err != nil {
				err = proto.ErrBadPeerImpl
				return
			}
			atomic.StoreInt32(&self.digestThreshold, int32(d))

		}
		if len(cmd.Params[1]) > 0 {
			var c int
			c, err = strconv.Atoi(cmd.Params[1])
			if err != nil {
				err = proto.ErrBadPeerImpl
				return
			}
			atomic.StoreInt32(&self.compressThreshold, int32(c))
		}
		nrPreDigestFields := 2
		if len(cmd.Params) > nrPreDigestFields {
			self.digestFielsLock.Lock()
			defer self.digestFielsLock.Unlock()
			self.digestFields = make([]string, len(cmd.Params)-nrPreDigestFields)
			for i, f := range cmd.Params[nrPreDigestFields:] {
				self.digestFields[i] = f
			}
		}
	case proto.CMD_MSG_RETRIEVE:
		if len(cmd.Params) < 1 {
			err = proto.ErrBadPeerImpl
			return
		}
		id := cmd.Params[0]

		// If there is no cache, then send an empty message
		if self.mcache == nil {
			m := new(proto.Message)
			m.Id = id
			err = self.writeAutoCompress(m, m.Size())
			return
		}

		var rmsg *proto.Message

		rmsg, err = self.mcache.Get(self.Service(), self.Username(), id)
		if err != nil {
			return
		}

		if rmsg == nil {
			rmsg = new(proto.Message)
		}
		rmsg.Id = id
		err = self.writeAutoCompress(rmsg, rmsg.Size())
	case proto.CMD_REQ_ALL_CACHED:
		if self.mcache == nil {
			return
		}
		excludes := make([]string, 0, 10)
		if cmd.Message != nil {
			msg := cmd.Message
			if len(msg.Body) > 0 {
				data := msg.Body
				for len(data) > 0 {
					var id []byte
					var err error
					id, data, err = cutString(data)
					if err != nil {
						break
					}
					excludes = append(excludes, string(id))
				}
			}
		}
		self.sendAllCachedMessage(excludes...)
	}
	return
}

func cutString(data []byte) (str, rest []byte, err error) {
	var idx int
	var d byte
	idx = -1
	for idx, d = range data {
		if d == 0 {
			break
		}
	}
	if idx < 0 {
		err = proto.ErrMalformedCommand
		return
	}
	str = data[:idx]
	rest = data[idx+1:]
	return
}

func (self *serverConn) SetMessageCache(cache msgcache.Cache) {
	self.mcache = cache
}

func NewConn(cmdio *proto.CommandIO, service, username string, conn net.Conn) Conn {
	sc := new(serverConn)
	sc.cmdio = cmdio
	c := proto.NewConn(cmdio, service, username, conn, sc)
	sc.Conn = c
	sc.digestThreshold = -1
	sc.compressThreshold = 512
	sc.digestFields = make([]string, 0, 10)
	sc.visible = 1
	return sc
}
