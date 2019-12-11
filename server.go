package koushin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

const cookieName = "koushin_session"

const messagesPerPage = 50

type Server struct {
	sessions *SessionManager

	imap struct {
		host     string
		tls      bool
		insecure bool
	}

	smtp struct {
		host     string
		tls      bool
		insecure bool
	}

	plugins []Plugin
}

func (s *Server) parseIMAPURL(imapURL string) error {
	u, err := url.Parse(imapURL)
	if err != nil {
		return fmt.Errorf("failed to parse IMAP server URL: %v", err)
	}

	s.imap.host = u.Host
	switch u.Scheme {
	case "imap":
		// This space is intentionally left blank
	case "imaps":
		s.imap.tls = true
	case "imap+insecure":
		s.imap.insecure = true
	default:
		return fmt.Errorf("unrecognized IMAP URL scheme: %s", u.Scheme)
	}

	return nil
}

func (s *Server) parseSMTPURL(smtpURL string) error {
	u, err := url.Parse(smtpURL)
	if err != nil {
		return fmt.Errorf("failed to parse SMTP server URL: %v", err)
	}

	s.smtp.host = u.Host
	switch u.Scheme {
	case "smtp":
		// This space is intentionally left blank
	case "smtps":
		s.smtp.tls = true
	case "smtp+insecure":
		s.smtp.insecure = true
	default:
		return fmt.Errorf("unrecognized SMTP URL scheme: %s", u.Scheme)
	}

	return nil
}

func newServer(imapURL, smtpURL string) (*Server, error) {
	s := &Server{}
	s.sessions = NewSessionManager(s.connectIMAP)

	if err := s.parseIMAPURL(imapURL); err != nil {
		return nil, err
	}

	if smtpURL != "" {
		if err := s.parseSMTPURL(smtpURL); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// Context is the context used by HTTP handlers.
//
// Use a type assertion to get it from a echo.Context:
//
//     ctx := ectx.(*koushin.Context)
type Context struct {
	echo.Context
	Server  *Server
	Session *Session
}

var aLongTimeAgo = time.Unix(233431200, 0)

func (c *Context) setToken(token string) {
	cookie := http.Cookie{
		Name:     cookieName,
		Value:    token,
		HttpOnly: true,
		// TODO: domain, secure
	}
	if token == "" {
		cookie.Expires = aLongTimeAgo // unset the cookie
	}
	c.SetCookie(&cookie)
}

func isPublic(path string) bool {
	return path == "/login" || strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/themes/")
}

type Options struct {
	IMAPURL, SMTPURL string
	Theme            string
}

func New(e *echo.Echo, options *Options) error {
	s, err := newServer(options.IMAPURL, options.SMTPURL)
	if err != nil {
		return err
	}

	s.plugins, err = loadAllLuaPlugins(e.Logger)
	if err != nil {
		return fmt.Errorf("failed to load plugins: %v", err)
	}

	e.Renderer, err = loadTemplates(e.Logger, options.Theme, s.plugins)
	if err != nil {
		return fmt.Errorf("failed to load templates: %v", err)
	}

	e.HTTPErrorHandler = func(err error, c echo.Context) {
		code := http.StatusInternalServerError
		if he, ok := err.(*echo.HTTPError); ok {
			code = he.Code
		} else {
			c.Logger().Error(err)
		}
		// TODO: hide internal errors
		c.String(code, err.Error())
	}

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(ectx echo.Context) error {
			ctx := &Context{Context: ectx, Server: s}
			ctx.Set("context", ctx)

			cookie, err := ctx.Cookie(cookieName)
			if err == http.ErrNoCookie {
				// Require auth for all pages except /login
				if isPublic(ctx.Path()) {
					return next(ctx)
				} else {
					return ctx.Redirect(http.StatusFound, "/login")
				}
			} else if err != nil {
				return err
			}

			ctx.Session, err = ctx.Server.sessions.Get(cookie.Value)
			if err == ErrSessionExpired {
				ctx.setToken("")
				return ctx.Redirect(http.StatusFound, "/login")
			} else if err != nil {
				return err
			}
			ctx.Session.Ping()

			return next(ctx)
		}
	})

	e.GET("/mailbox/:mbox", handleGetMailbox)

	e.GET("/message/:mbox/:uid", func(ectx echo.Context) error {
		ctx := ectx.(*Context)
		return handleGetPart(ctx, false)
	})
	e.GET("/message/:mbox/:uid/raw", func(ectx echo.Context) error {
		ctx := ectx.(*Context)
		return handleGetPart(ctx, true)
	})

	e.GET("/login", handleLogin)
	e.POST("/login", handleLogin)

	e.GET("/logout", handleLogout)

	e.GET("/compose", handleCompose)
	e.POST("/compose", handleCompose)

	e.GET("/message/:mbox/:uid/reply", handleCompose)
	e.POST("/message/:mbox/:uid/reply", handleCompose)

	e.Static("/assets", "public/assets")
	e.Static("/themes", "public/themes")

	for _, p := range s.plugins {
		p.SetRoutes(e.Group(""))
	}

	return nil
}
