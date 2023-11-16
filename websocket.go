package argo

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"sync"
	"time"
)

type websocketCaller struct {
	conn     *websocket.Conn
	sendChan chan *sendRequest
	cancel   context.CancelFunc
	wg       *sync.WaitGroup
	once     sync.Once
	timeout  time.Duration
}

func newWebsocketCaller(ctx context.Context, uri string, timeout time.Duration, notifier Notifier) (*websocketCaller, error) {
	var header = http.Header{}
	conn, _, err := websocket.DefaultDialer.Dial(uri, header)
	if err != nil {
		return nil, err
	}

	sendChan := make(chan *sendRequest, 16)
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	w := &websocketCaller{conn: conn, wg: &wg, cancel: cancel, sendChan: sendChan, timeout: timeout}
	processor := NewResponseProcessor()
	wg.Add(1)
	go func() { // routine:recv
		defer wg.Done()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			var resp websocketResponse
			if err := conn.ReadJSON(&resp); err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				log.Printf("conn.ReadJSON|err:%v", err.Error())
				return
			}
			if resp.Id == nil { // RPC notifications
				if notifier != nil {
					switch resp.Method {
					case onDownloadStart:
						notifier.OnDownloadStart(resp.Params)
					case onDownloadPause:
						notifier.OnDownloadPause(resp.Params)
					case onDownloadStop:
						notifier.OnDownloadStop(resp.Params)
					case onDownloadComplete:
						notifier.OnDownloadComplete(resp.Params)
					case onDownloadError:
						notifier.OnDownloadError(resp.Params)
					case onBtDownloadComplete:
						notifier.OnBtDownloadComplete(resp.Params)
					default:
						log.Printf("unexpected notification: %s", resp.Method)
					}
				}
				continue
			}
			_ = processor.Process(resp.clientResponse)
		}
	}()
	wg.Add(1)
	go func() { // routine:send
		defer wg.Done()
		defer cancel()
		defer func() { _ = w.conn.Close() }()

		for {
			select {
			case <-ctx.Done():
				if err = w.conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
					log.Printf("sending websocket close message: %v", err)
				}
				return
			case req := <-sendChan:
				processor.Add(req.request.Id, func(resp clientResponse) error {
					err = resp.decode(req.reply)
					req.cancel()
					return err
				})
				_ = w.conn.SetWriteDeadline(time.Now().Add(timeout))
				_ = w.conn.WriteJSON(req.request)
			}
		}
	}()

	return w, nil
}

func (w *websocketCaller) Close() {
	w.once.Do(func() {
		w.cancel()
		w.wg.Wait()
	})
}

func (w *websocketCaller) Call(method string, params, reply interface{}) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	select {
	case w.sendChan <- &sendRequest{cancel: cancel, request: newClientRequest(method, params), reply: reply}:

	default:
		return errors.New("sending channel blocking")
	}

	select {
	case <-ctx.Done():
		if err := ctx.Err(); errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
	return
}

type sendRequest struct {
	cancel  context.CancelFunc
	request *clientRequest
	reply   interface{}
}
