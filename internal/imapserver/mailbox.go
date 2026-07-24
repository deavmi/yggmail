/*
 *  Copyright (c) 2021 Neil Alexander
 *
 *  This Source Code Form is subject to the terms of the Mozilla Public
 *  License, v. 2.0. If a copy of the MPL was not distributed with this
 *  file, You can obtain one at http://mozilla.org/MPL/2.0/.
 */

package imapserver

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend"
	"github.com/emersion/go-imap/backend/backendutil"
	"github.com/emersion/go-message/textproto"
	"github.com/neilalexander/yggmail/internal/storage/types"
)

type Mailbox struct {
	backend *Backend
	name    string
	user    *User
}

func (mbox *Mailbox) getIDsFromSeqSet(uid bool, seqSet *imap.SeqSet) ([]int, error) {
	mails, err := mbox.backend.Storage.MailList(mbox.name, nil)
	if err != nil {
		return nil, fmt.Errorf("mbox.backend.Storage.MailList: %w", err)
	}

	var maxUID uint32
	if len(mails) > 0 {
		maxUID = uint32(mails[len(mails)-1].ID)
	}
	maxSeq := uint32(len(mails))

	ids := make([]int, 0, len(mails))
	for i, mail := range mails {
		number, maximum := uint32(i+1), maxSeq
		if uid {
			number, maximum = uint32(mail.ID), maxUID
		}
		if seqSetContains(seqSet, number, maximum) {
			ids = append(ids, mail.ID)
		}
	}
	return ids, nil
}

func (mbox *Mailbox) Name() string {
	return mbox.name
}

func (mbox *Mailbox) Info() (*imap.MailboxInfo, error) {
	info := &imap.MailboxInfo{
		Attributes: []string{},
		Delimiter:  "/",
		Name:       mbox.name,
	}
	return info, nil
}

func (mbox *Mailbox) Status(items []imap.StatusItem) (*imap.MailboxStatus, error) {
	status := imap.NewMailboxStatus(mbox.name, items)
	status.PermanentFlags = []string{
		"\\Seen", "\\Answered", "\\Flagged", "\\Deleted",
	}
	status.Flags = status.PermanentFlags

	for _, name := range items {
		switch name {
		case imap.StatusMessages:
			count, err := mbox.backend.Storage.MailCount(mbox.name)
			if err != nil {
				return nil, fmt.Errorf("mbox.backend.Storage.MailCount: %w", err)
			}
			status.Messages = uint32(count)

		case imap.StatusUidNext:
			id, err := mbox.backend.Storage.MailNextID(mbox.name)
			if err != nil {
				return nil, fmt.Errorf("mbox.backend.Storage.MailNextID: %w", err)
			}
			status.UidNext = uint32(id)

		case imap.StatusUidValidity:
			validity, err := mbox.backend.Storage.MailUIDValidity(mbox.name)
			if err != nil {
				return nil, fmt.Errorf("mbox.backend.Storage.MailUIDValidity: %w", err)
			}
			status.UidValidity = validity

		case imap.StatusRecent:
			status.Recent = 0 // TODO

		case imap.StatusUnseen:
			unseen, err := mbox.backend.Storage.MailUnseen(mbox.name)
			if err != nil {
				return nil, fmt.Errorf("mbox.backend.Storage.MailUnseen: %w", err)
			}
			status.Unseen = uint32(unseen)
		}
	}

	return status, nil
}

func (mbox *Mailbox) SetSubscribed(subscribed bool) error {
	return mbox.backend.Storage.MailboxSubscribe(mbox.name, subscribed)
}

func (mbox *Mailbox) Check() error {
	return nil
}

func (mbox *Mailbox) ListMessages(uid bool, seqSet *imap.SeqSet, items []imap.FetchItem, ch chan<- *imap.Message) error {
	defer close(ch)

	mails, err := mbox.backend.Storage.MailList(mbox.name, nil)
	if err != nil {
		return fmt.Errorf("mbox.backend.Storage.MailList: %w", err)
	}

	maxSeq := uint32(len(mails))
	var maxUID uint32
	if len(mails) > 0 {
		maxUID = uint32(mails[len(mails)-1].ID)
	}
	for i, mail := range mails {
		mseq := uint32(i + 1)
		id := uint32(mail.ID)
		number, maximum := mseq, maxSeq
		if uid {
			number, maximum = id, maxUID
		}
		if !seqSetContains(seqSet, number, maximum) {
			continue
		}

		fetched := imap.NewMessage(mseq, items)
		fetched.SeqNum = mseq
		fetched.Uid = id

		get := func() (io.Reader, textproto.Header, error) {
			bodyreader := bufio.NewReader(bytes.NewReader(mail.Mail))
			hdr, err := textproto.ReadHeader(bodyreader)
			if err != nil {
				return nil, textproto.Header{}, fmt.Errorf("textproto.ReadHeader: %w", err)
			}
			return bodyreader, hdr, err
		}

		for _, item := range items {
			switch item {
			case imap.FetchEnvelope:
				_, hdr, err := get()
				if err != nil {
					continue
				}
				if fetched.Envelope, err = backendutil.FetchEnvelope(hdr); err != nil {
					continue
				}

			case imap.FetchBody, imap.FetchBodyStructure:
				bodyreader, hdr, err := get()
				if err != nil {
					continue
				}
				if fetched.BodyStructure, err = backendutil.FetchBodyStructure(hdr, bodyreader, item == imap.FetchBodyStructure); err != nil {
					continue
				}

			case imap.FetchFlags:
				fetched.Flags = []string{}
				if mail.Seen {
					fetched.Flags = append(fetched.Flags, "\\Seen")
				}
				if mail.Answered {
					fetched.Flags = append(fetched.Flags, "\\Answered")
				}
				if mail.Flagged {
					fetched.Flags = append(fetched.Flags, "\\Flagged")
				}
				if mail.Deleted {
					fetched.Flags = append(fetched.Flags, "\\Deleted")
				}

			case imap.FetchInternalDate:
				fetched.InternalDate = mail.Date

			case imap.FetchRFC822Size:
				fetched.Size = uint32(len(mail.Mail))

			case imap.FetchUid:
				fetched.Uid = id

			default:
				section, err := imap.ParseBodySectionName(item)
				if err != nil {
					continue
				}
				bodyreader, hdr, err := get()
				if err != nil {
					continue
				}
				l, err := backendutil.FetchBodySection(hdr, bodyreader, section)
				if err != nil {
					continue
				}
				fetched.Body[section] = l
			}
		}

		ch <- fetched
	}

	return nil
}

func (mbox *Mailbox) SearchMessages(uid bool, criteria *imap.SearchCriteria) ([]uint32, error) {
	seen, possible := seenFilter(criteria)
	if !possible {
		return []uint32{}, nil
	}
	// Filtering in SQL uses the seen index for the common UID SEARCH UNSEEN
	// sync path. Sequence-number searches need the complete mailbox so that
	// their positions remain correct.
	if !uid || criteria.SeqNum != nil {
		seen = nil
	}
	mails, err := mbox.backend.Storage.MailList(mbox.name, seen)
	if err != nil {
		return nil, fmt.Errorf("mbox.backend.Storage.MailList: %w", err)
	}

	maxSeq := uint32(len(mails))
	var maxUID uint32
	if len(mails) > 0 {
		maxUID = uint32(mails[len(mails)-1].ID)
	}
	var ids []uint32
	for i, mail := range mails {
		seqNum := uint32(i + 1)
		mailUID := uint32(mail.ID)
		if criteria.SeqNum != nil && !seqSetContains(criteria.SeqNum, seqNum, maxSeq) {
			continue
		}
		if criteria.Uid != nil && !seqSetContains(criteria.Uid, mailUID, maxUID) {
			continue
		}
		if !matchesSeenCriteria(mail.Seen, criteria) {
			continue
		}
		if uid {
			ids = append(ids, mailUID)
		} else {
			ids = append(ids, seqNum)
		}
	}
	return ids, nil
}

func seqSetContains(seqSet *imap.SeqSet, number, maximum uint32) bool {
	if seqSet.Contains(number) {
		return true
	}
	if number != maximum {
		return false
	}
	for _, seq := range seqSet.Set {
		if seq.Contains(0) {
			return true
		}
	}
	return false
}

func matchesSeenCriteria(seen bool, criteria *imap.SearchCriteria) bool {
	for _, flag := range criteria.WithFlags {
		if flag == imap.SeenFlag && !seen {
			return false
		}
	}
	for _, flag := range criteria.WithoutFlags {
		if flag == imap.SeenFlag && seen {
			return false
		}
	}
	return true
}

func seenFilter(criteria *imap.SearchCriteria) (*bool, bool) {
	var (
		filter    bool
		hasSeen   bool
		hasUnseen bool
	)
	for _, flag := range criteria.WithFlags {
		hasSeen = hasSeen || flag == imap.SeenFlag
	}
	for _, flag := range criteria.WithoutFlags {
		hasUnseen = hasUnseen || flag == imap.SeenFlag
	}
	if hasSeen && hasUnseen {
		return nil, false
	}
	if !hasSeen && !hasUnseen {
		return nil, true
	}
	filter = hasSeen
	return &filter, true
}

func (mbox *Mailbox) CreateMessage(flags []string, date time.Time, body imap.Literal) error {
	if mbox.name == "Outbox" {
		return fmt.Errorf("can't append into Outbox as it is a protected folder")
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("b.ReadFrom: %w", err)
	}
	id, err := mbox.backend.Storage.MailCreate(mbox.name, b)
	if err != nil {
		return fmt.Errorf("mbox.backend.Storage.MailCreate: %w", err)
	}
	mail := &types.Mail{ID: id}
	applyMailFlags(mail, flags)
	if err := mbox.backend.Storage.MailUpdateFlags(
		mbox.name, id, mail.Seen, mail.Answered, mail.Flagged, mail.Deleted,
	); err != nil {
		return err
	}
	if err := mbox.notifyMessageCount(); err != nil {
		return err
	}
	return nil
}

func (mbox *Mailbox) UpdateMessagesFlags(uid bool, seqSet *imap.SeqSet, op imap.FlagsOp, flags []string) error {
	ids, err := mbox.getIDsFromSeqSet(uid, seqSet)
	if err != nil {
		return fmt.Errorf("mbox.getIDsFromSeqSet: %w", err)
	}

	for _, id := range ids {
		seq, mail, err := mbox.backend.Storage.MailSelect(mbox.name, id)
		if err != nil {
			return fmt.Errorf("mbox.backend.Storage.MailSelect: %w", err)
		}
		updated := backendutil.UpdateFlags(mailFlags(mail), op, flags)
		applyMailFlags(mail, updated)

		if err := mbox.backend.Storage.MailUpdateFlags(
			mbox.name, mail.ID, mail.Seen,
			mail.Answered, mail.Flagged, mail.Deleted,
		); err != nil {
			return err
		}
		mbox.backend.sendUpdate(&backend.MessageUpdate{
			Update: backend.NewUpdate("", mbox.name),
			Message: &imap.Message{
				SeqNum: uint32(seq),
				Uid:    uint32(mail.ID),
				Flags:  mailFlags(mail),
			},
		})
	}
	return nil
}

func mailFlags(mail *types.Mail) []string {
	var flags []string
	if mail.Seen {
		flags = append(flags, imap.SeenFlag)
	}
	if mail.Answered {
		flags = append(flags, imap.AnsweredFlag)
	}
	if mail.Flagged {
		flags = append(flags, imap.FlaggedFlag)
	}
	if mail.Deleted {
		flags = append(flags, imap.DeletedFlag)
	}
	return flags
}

func applyMailFlags(mail *types.Mail, flags []string) {
	mail.Seen = false
	mail.Answered = false
	mail.Flagged = false
	mail.Deleted = false
	for _, flag := range flags {
		switch flag {
		case imap.SeenFlag:
			mail.Seen = true
		case imap.AnsweredFlag:
			mail.Answered = true
		case imap.FlaggedFlag:
			mail.Flagged = true
		case imap.DeletedFlag:
			mail.Deleted = true
		}
	}
}

func (mbox *Mailbox) CopyMessages(uid bool, seqSet *imap.SeqSet, destName string) error {
	destName = canonicalMailboxName(destName)
	if destName == "Outbox" {
		return fmt.Errorf("can't copy into Outbox as it is a protected folder")
	}

	ids, err := mbox.getIDsFromSeqSet(uid, seqSet)
	if err != nil {
		return fmt.Errorf("mbox.getIDsFromSeqSet: %w", err)
	}

	for _, id := range ids {
		if err := mbox.backend.Storage.MailCopy(mbox.name, id, destName); err != nil {
			return fmt.Errorf("mbox.backend.Storage.MailCopy: %w", err)
		}
	}
	return mbox.notifyMailboxCount(destName)
}

func (mbox *Mailbox) Expunge() error {
	mails, err := mbox.backend.Storage.MailList(mbox.name, nil)
	if err != nil {
		return err
	}
	var seqNums []uint32
	for i, mail := range mails {
		if mail.Deleted {
			seqNums = append(seqNums, uint32(i+1))
		}
	}
	if err := mbox.backend.Storage.MailExpunge(mbox.name); err != nil {
		return err
	}
	for i := len(seqNums) - 1; i >= 0; i-- {
		mbox.backend.sendUpdate(&backend.ExpungeUpdate{
			Update: backend.NewUpdate("", mbox.name),
			SeqNum: seqNums[i],
		})
	}
	return nil
}

func (mbox *Mailbox) MoveMessages(uid bool, seqset *imap.SeqSet, dest string) error {
	dest = canonicalMailboxName(dest)
	if dest == "Outbox" {
		return fmt.Errorf("can't copy into Outbox as it is a protected folder")
	}

	ids, err := mbox.getIDsFromSeqSet(uid, seqset)
	if err != nil {
		return fmt.Errorf("mbox.getIDsFromSeqSet: %w", err)
	}

	type messageRef struct {
		id  int
		seq int
	}
	refs := make([]messageRef, 0, len(ids))
	for _, id := range ids {
		seq, _, err := mbox.backend.Storage.MailSelect(mbox.name, id)
		if err != nil {
			return err
		}
		refs = append(refs, messageRef{id: id, seq: seq})
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].seq > refs[j].seq
	})
	for _, ref := range refs {
		if err := mbox.backend.Storage.MailMove(mbox.name, ref.id, dest); err != nil {
			return err
		}
		mbox.backend.sendUpdate(&backend.ExpungeUpdate{
			Update: backend.NewUpdate("", mbox.name),
			SeqNum: uint32(ref.seq),
		})
	}
	return mbox.notifyMailboxCount(dest)
}

func (mbox *Mailbox) notifyMessageCount() error {
	return mbox.notifyMailboxCount(mbox.name)
}

func (mbox *Mailbox) notifyMailboxCount(name string) error {
	count, err := mbox.backend.Storage.MailCount(name)
	if err != nil {
		return fmt.Errorf("mbox.backend.Storage.MailCount: %w", err)
	}
	mbox.backend.sendMailboxCount(name, count)
	return nil
}
