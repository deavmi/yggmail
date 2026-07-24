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
	idle "github.com/emersion/go-imap-idle"
	move "github.com/emersion/go-imap-move"
	"github.com/emersion/go-imap/server"
	"github.com/emersion/go-sasl"
)

type IMAPServer struct {
	server  *server.Server
	backend *Backend
	notify  *IMAPNotify
}

func NewIMAPServer(backend *Backend, addr string, insecure bool) (*IMAPServer, *IMAPNotify, error) {
	s := &IMAPServer{
		server:  server.New(backend),
		backend: backend,
	}
	s.notify = NewIMAPNotify(s.server, backend.Log)
	s.server.Addr = addr
	s.server.AllowInsecureAuth = insecure
	//s.server.Debug = os.Stdout
	s.server.Enable(idle.NewExtension())
	s.server.Enable(move.NewExtension())
	s.server.EnableAuth(sasl.Login, func(conn server.Conn) sasl.Server {
		return sasl.NewLoginServer(func(username, password string) error {
			return authenticateLogin(
				s.backend, conn.Info(), conn.Context(), username, password,
			)
		})
	})
	go func() {
		if err := s.server.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()
	return s, s.notify, nil
}

func authenticateLogin(
	backend *Backend,
	info *imap.ConnInfo,
	context *server.Context,
	username, password string,
) error {
	user, err := backend.Login(info, username, password)
	if err != nil {
		return err
	}
	context.State = imap.AuthenticatedState
	context.User = user
	return nil
}
