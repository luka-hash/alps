package koushin

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"

	imapclient "github.com/emersion/go-imap/client"
)

func generateToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

var ErrSessionExpired = errors.New("session expired")

// TODO: expiration timer
type ConnPool struct {
	locker sync.Mutex
	conns  map[string]*imapclient.Client
}

func NewConnPool() *ConnPool {
	return &ConnPool{
		conns: make(map[string]*imapclient.Client),
	}
}

func (pool *ConnPool) Get(token string) (*imapclient.Client, error) {
	pool.locker.Lock()
	defer pool.locker.Unlock()

	conn, ok := pool.conns[token]
	if !ok {
		return nil, ErrSessionExpired
	}
	return conn, nil
}

func (pool *ConnPool) Put(conn *imapclient.Client) (token string, err error) {
	pool.locker.Lock()
	defer pool.locker.Unlock()

	for {
		var err error
		token, err = generateToken()
		if err != nil {
			conn.Logout()
			return "", err
		}

		if _, ok := pool.conns[token]; !ok {
			break
		}
	}

	pool.conns[token] = conn

	go func() {
		<-conn.LoggedOut()

		pool.locker.Lock()
		delete(pool.conns, token)
		pool.locker.Unlock()
	}()

	return token, nil
}
