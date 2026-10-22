package health

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

var ErrNotServing = errors.New("RPC server is not serving")

type Check struct {
	Name string
	Ping func(context.Context) error
}

type Checker struct {
	serving atomic.Bool
	timeout time.Duration
	checks  []Check
}

func NewChecker(timeout time.Duration, checks ...Check) *Checker {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Checker{timeout: timeout, checks: append([]Check(nil), checks...)}
}

func (c *Checker) SetServing(serving bool) {
	c.serving.Store(serving)
}

func (c *Checker) CheckReadiness(ctx context.Context) error {
	if !c.serving.Load() {
		return ErrNotServing
	}
	checkCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for _, check := range c.checks {
		if check.Ping == nil {
			continue
		}
		if err := check.Ping(checkCtx); err != nil {
			return fmt.Errorf("%s: %w", check.Name, err)
		}
	}
	return nil
}

func (c *Checker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		writeStatus(response, http.StatusOK, "ok")
	})
	mux.HandleFunc("/readyz", func(response http.ResponseWriter, request *http.Request) {
		if err := c.CheckReadiness(request.Context()); err != nil {
			writeStatus(response, http.StatusServiceUnavailable, "not ready")
			return
		}
		writeStatus(response, http.StatusOK, "ready")
	})
	return mux
}

func writeStatus(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write([]byte(body + "\n"))
}
