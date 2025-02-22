package argo

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestWebsocketCaller(t *testing.T) {
	time.Sleep(time.Second)
	c, err := newWebsocketCaller(context.Background(), "ws://localhost:6800/jsonrpc", time.Second, &DefaultNotifier{})
	if err != nil {
		t.Fatal(err.Error())
	}
	defer c.Close()

	var info VersionInfo
	if err := c.Call(aria2GetVersion, []interface{}{}, &info); err != nil {
		t.Error(err.Error())
	} else {
		fmt.Println(info.Version)
	}
}
