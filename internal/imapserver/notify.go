/*
 *  Copyright (c) 2021 Neil Alexander
 *
 *  This Source Code Form is subject to the terms of the Mozilla Public
 *  License, v. 2.0. If a copy of the MPL was not distributed with this
 *  file, You can obtain one at http://mozilla.org/MPL/2.0/.
 */

package imapserver

import (
	"log"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/server"
)

type IMAPNotify struct {
	server *server.Server
	log    *log.Logger
}

func (ext *IMAPNotify) NotifyNew(count int) error {
	var firstErr error
	ext.server.ForEachConn(func(c server.Conn) {
		context := c.Context()
		if context.State != imap.SelectedState ||
			context.Mailbox == nil ||
			context.Mailbox.Name() != "INBOX" {
			return
		}
		response := imap.NewUntaggedResp([]interface{}{
			uint32(count),
			imap.RawString("EXISTS"),
		})
		if err := c.WriteResp(response); err != nil && firstErr == nil {
			firstErr = err
		}
	})
	return firstErr
}

func NewIMAPNotify(s *server.Server, log *log.Logger) *IMAPNotify {
	return &IMAPNotify{
		server: s,
		log:    log,
	}
}
