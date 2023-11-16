package argo

import (
	"context"
	"github.com/gorilla/websocket"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type HttpCaller struct {
	uri    string
	c      *http.Client
	cancel context.CancelFunc
	wg     *sync.WaitGroup
	once   sync.Once
}

func newHTTPCaller(ctx context.Context, u *url.URL, timeout time.Duration, notifer Notifier) *HttpCaller {
	c := &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 1,
			MaxConnsPerHost:     1,
			DialContext: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: 60 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	h := &HttpCaller{uri: u.String(), c: c, cancel: cancel, wg: &wg}
	if notifer != nil {
		_ = h.setNotifier(ctx, *u, notifer)
	}
	return h
}

func (h *HttpCaller) Close() {
	h.once.Do(func() {
		h.cancel()
		h.wg.Wait()
	})
}

func (h *HttpCaller) setNotifier(ctx context.Context, u url.URL, notifer Notifier) (err error) {
	u.Scheme = "ws"
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() { _ = conn.Close() }()
		select {
		case <-ctx.Done():
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			if err = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
				log.Printf("sending websocket close message: %v", err)
			}
			return
		}
	}()
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		var request websocketResponse
		//var err error
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err = conn.ReadJSON(&request); err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				log.Printf("conn.ReadJSON|err:%v", err.Error())
				return
			}
			switch request.Method {
			case onDownloadStart:
				notifer.OnDownloadStart(request.Params)
			case onDownloadPause:
				notifer.OnDownloadPause(request.Params)
			case onDownloadStop:
				notifer.OnDownloadStop(request.Params)
			case onDownloadComplete:
				notifer.OnDownloadComplete(request.Params)
			case onDownloadError:
				notifer.OnDownloadError(request.Params)
			case onBtDownloadComplete:
				notifer.OnBtDownloadComplete(request.Params)
			default:
				log.Printf("unexpected notification: %s", request.Method)
			}
		}
	}()
	return
}

func (h *HttpCaller) Call(method string, params, reply interface{}) (err error) {
	payload, err := EncodeClientRequest(method, params)
	if err != nil {
		return
	}
	r, err := h.c.Post(h.uri, "application/json", payload)
	if err != nil {
		return
	}
	err = DecodeClientResponse(r.Body, &reply)
	_ = r.Body.Close()
	return err
}
