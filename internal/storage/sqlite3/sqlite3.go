/*
 *  Copyright (c) 2021 Neil Alexander
 *
 *  This Source Code Form is subject to the terms of the Mozilla Public
 *  License, v. 2.0. If a copy of the MPL was not distributed with this
 *  file, You can obtain one at http://mozilla.org/MPL/2.0/.
 */

package sqlite3

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/neilalexander/yggmail/internal/storage/types"
	"go.uber.org/atomic"
)

type SQLite3Storage struct {
	*TableConfig
	*TableMailboxes
	*TableMails
	*TableQueue
	db     *sql.DB
	writer *Writer
}

func NewSQLite3StorageStorage(filename string) (*SQLite3Storage, error) {
	db, err := sql.Open("sqlite3", "file:"+filename+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	s := &SQLite3Storage{
		db: db,
		writer: &Writer{
			todo: make(chan writerTask),
		},
	}
	s.TableConfig, err = NewTableConfig(db, s.writer)
	if err != nil {
		return nil, fmt.Errorf("NewTableConfig: %w", err)
	}
	s.TableMailboxes, err = NewTableMailboxes(db, s.writer)
	if err != nil {
		return nil, fmt.Errorf("NewTableMailboxes: %w", err)
	}
	s.TableMails, err = NewTableMails(db, s.writer)
	if err != nil {
		return nil, fmt.Errorf("NewTableMails: %w", err)
	}
	if err = initializeMailboxUIDs(db); err != nil {
		return nil, fmt.Errorf("initializeMailboxUIDs: %w", err)
	}
	s.TableQueue, err = NewTableQueue(db, s.writer)
	if err != nil {
		return nil, fmt.Errorf("NewTableQueue: %w", err)
	}
	return s, nil
}

func (s *SQLite3Storage) Close() error {
	return s.db.Close()
}

func (s *SQLite3Storage) QueueCreate(
	from string,
	recipients []types.QueueRecipient,
	localCopies int,
	content []byte,
) error {
	return s.writer.Do(s.db, nil, func(txn *sql.Tx) error {
		for range localCopies {
			if _, err := createMailTx(txn, "INBOX", content, time.Now()); err != nil {
				return fmt.Errorf("create local Inbox copy: %w", err)
			}
		}

		if len(recipients) == 0 {
			if _, err := createMailTx(txn, "Sent", content, time.Now()); err != nil {
				return fmt.Errorf("create Sent copy: %w", err)
			}
			return nil
		}

		outboxID, err := createMailTx(txn, "Outbox", content, time.Now())
		if err != nil {
			return fmt.Errorf("create Outbox mail: %w", err)
		}
		for _, recipient := range recipients {
			if _, err := txn.Exec(`
				INSERT INTO queue (destination, mailbox, id, mail, rcpt)
				VALUES ($1, 'Outbox', $2, $3, $4)
			`, recipient.Destination, outboxID, from, recipient.Address); err != nil {
				return fmt.Errorf("create queue destination: %w", err)
			}
		}
		return nil
	})
}

func createMailTx(txn *sql.Tx, mailbox string, content []byte, date time.Time) (int, error) {
	id, err := allocateMailboxUIDTx(txn, mailbox)
	if err != nil {
		return 0, err
	}
	if _, err := txn.Exec(`
		INSERT INTO mails (mailbox, id, mail, datetime)
		VALUES ($1, $2, $3, $4)
	`, mailbox, id, content, date.Unix()); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *SQLite3Storage) QueueMarkDelivered(destination string, id int) error {
	return s.writer.Do(s.db, nil, func(txn *sql.Tx) error {
		result, err := txn.Exec(
			"DELETE FROM queue WHERE destination = $1 AND mailbox = 'Outbox' AND id = $2",
			destination, id,
		)
		if err != nil {
			return fmt.Errorf("delete queue destination: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("deleted queue rows: %w", err)
		}
		if deleted == 0 {
			return errors.New("queue destination not found")
		}

		var remaining int
		if err := txn.QueryRow(
			"SELECT COUNT(*) FROM queue WHERE mailbox = 'Outbox' AND id = $1",
			id,
		).Scan(&remaining); err != nil {
			return fmt.Errorf("count remaining queue destinations: %w", err)
		}
		if remaining > 0 {
			return nil
		}
		return moveMailTx(txn, "Outbox", id, "Sent")
	})
}

type Writer struct {
	running atomic.Bool
	todo    chan writerTask
}

type writerTask struct {
	db   *sql.DB
	txn  *sql.Tx
	f    func(txn *sql.Tx) error
	wait chan error
}

func (w *Writer) Do(db *sql.DB, txn *sql.Tx, f func(txn *sql.Tx) error) error {
	if !w.running.Load() {
		go w.run()
	}
	task := writerTask{
		db:   db,
		txn:  txn,
		f:    f,
		wait: make(chan error, 1),
	}
	w.todo <- task
	return <-task.wait
}

func (w *Writer) run() {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	for task := range w.todo {
		if task.db != nil && task.txn != nil {
			task.wait <- task.f(task.txn)
		} else if task.db != nil && task.txn == nil {
			func() {
				txn, err := task.db.Begin()
				if err != nil {
					task.wait <- err
					return
				}
				err = task.f(txn)
				if err == nil {
					err = txn.Commit()
				} else {
					_ = txn.Rollback()
				}
				task.wait <- err
			}()
		} else {
			task.wait <- task.f(nil)
		}
		close(task.wait)
	}
}
