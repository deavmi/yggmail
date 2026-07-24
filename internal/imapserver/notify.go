/*
 *  Copyright (c) 2021 Neil Alexander
 *
 *  This Source Code Form is subject to the terms of the Mozilla Public
 *  License, v. 2.0. If a copy of the MPL was not distributed with this
 *  file, You can obtain one at http://mozilla.org/MPL/2.0/.
 */

package imapserver

import (
	"fmt"
)

type IMAPNotify struct {
	backend *Backend
}

func (ext *IMAPNotify) NotifyNew(count int) error {
	if count < 0 {
		return fmt.Errorf("invalid INBOX message count %d", count)
	}
	ext.backend.sendMailboxCount("INBOX", count)
	return nil
}

func NewIMAPNotify(backend *Backend) *IMAPNotify {
	return &IMAPNotify{backend: backend}
}
