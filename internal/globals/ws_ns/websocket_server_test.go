package ws_ns

import (
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"testing"
	"time"

	"github.com/inoxlang/inox/internal/core"
	"github.com/inoxlang/inox/internal/globals/fs_ns"
	"github.com/inoxlang/inox/internal/globals/http_ns"
	"github.com/inoxlang/inox/internal/permkind"
	"github.com/inoxlang/inox/internal/utils"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestWebsocketServer(t *testing.T) {
	permissiveSocketCountLimit := core.MustMakeNotDecrementingLimit(WS_SIMUL_CONN_TOTAL_LIMIT_NAME, 100)

	if !core.AreDefaultRequestHandlingLimitsSet() {
		core.SetDefaultRequestHandlingLimits([]core.Limit{})
		defer core.UnsetDefaultRequestHandlingLimits()
	}

	if !core.AreDefaultMaxRequestHandlerLimitsSet() {
		core.SetDefaultMaxRequestHandlerLimits([]core.Limit{})
		defer core.UnsetDefaultMaxRequestHandlerLimits()
	}

	t.Run("create with required permission", func(t *testing.T) {
		ctx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
		})
		server, err := NewWebsocketServer(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, server)
	})

	t.Run("create without required permission", func(t *testing.T) {
		ctx := core.NewContext(core.ContextConfig{})
		server, err := NewWebsocketServer(ctx)
		assert.ErrorIs(t, err, core.NewNotAllowedError(core.WebsocketPermission{Kind_: permkind.Provide}))
		assert.Nil(t, server)
	})

	const HOST = core.Host("https://localhost:8080")
	const ENDPOINT = core.URL("wss://localhost:8080/")

	t.Run("upgrade", func(t *testing.T) {
		HOST := core.Host("https://localhost:8080")
		ENDPOINT := core.URL("wss://localhost:8080/")

		clientCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Read, Endpoint: ENDPOINT},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
			Limits:     []core.Limit{permissiveSocketCountLimit},
		})

		serverCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
				core.HttpPermission{
					Kind_:  permkind.Provide,
					Entity: HOST,
				},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
		})

		serverState := core.NewGlobalState(serverCtx)
		serverState.Logger = zerolog.New(io.Discard)
		serverState.Out = io.Discard

		closeChan := createWebsocketServer(testWebsocketServerConfig{
			host:           HOST,
			messageTimeout: time.Second,
		}, serverCtx)
		defer func() {
			go func() {
				closeChan <- struct{}{}
			}()
		}()

		conn, err := websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
		assert.NoError(t, err)
		assert.NotNil(t, conn)
		assert.Equal(t, ENDPOINT, conn.endpoint)

		conn.Close()
	})

	t.Run("upgrade should refuse the connection if there are too many connections on the same IP", func(t *testing.T) {
		clientCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Read, Endpoint: ENDPOINT},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
			Limits:     []core.Limit{permissiveSocketCountLimit},
		})

		serverCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
				core.HttpPermission{
					Kind_:  permkind.Provide,
					Entity: HOST,
				},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
		})

		serverState := core.NewGlobalState(serverCtx)
		serverState.Logger = zerolog.New(io.Discard)
		serverState.Out = io.Discard

		closeChan := createWebsocketServer(testWebsocketServerConfig{
			host:           HOST,
			messageTimeout: time.Second,
		}, serverCtx)

		defer func() {
			go func() {
				closeChan <- struct{}{}
			}()
		}()

		//okay
		for i := 0; i < DEFAULT_MAX_IP_WS_CONNS; i++ {
			conn, err := websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
			if !assert.NoError(t, err) {
				return
			}
			assert.NotNil(t, conn)
			assert.Equal(t, ENDPOINT, conn.endpoint)
			defer conn.Close()
		}

		//refused connection.
		conn, err := websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
		if !assert.Error(t, err) {
			return
		}
		assert.Nil(t, conn)
	})

	t.Run("upgrade should not refuse the connection if there have been many connections at different times on the same IP", func(t *testing.T) {
		clientCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Read, Endpoint: ENDPOINT},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
			Limits:     []core.Limit{permissiveSocketCountLimit},
		})

		serverCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
				core.HttpPermission{
					Kind_:  permkind.Provide,
					Entity: HOST,
				},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
		})

		serverState := core.NewGlobalState(serverCtx)
		serverState.Logger = zerolog.New(io.Discard)
		serverState.Out = io.Discard

		closeChan := createWebsocketServer(testWebsocketServerConfig{
			host:           HOST,
			messageTimeout: time.Second,
		}, serverCtx)

		defer func() {
			go func() {
				closeChan <- struct{}{}
			}()
		}()

		//okay
		for i := 0; i < DEFAULT_MAX_IP_WS_CONNS; i++ {
			conn, err := websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
			if !assert.NoError(t, err) {
				return
			}
			assert.NotNil(t, conn)
			assert.Equal(t, ENDPOINT, conn.endpoint)

			if i == 0 {
				conn.Close()
				time.Sleep(time.Second / 10)
			} else {
				defer conn.Close()
			}
		}

		//should be still ok since a connection has been closed.
		conn, err := websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
		if !assert.NoError(t, err) {
			return
		}
		assert.NotNil(t, conn)
		assert.Equal(t, ENDPOINT, conn.endpoint)

		//refused connection.
		conn, err = websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
		if !assert.Error(t, err) {
			return
		}
		assert.Nil(t, conn)
	})

	t.Run("Close() should close all connections", func(t *testing.T) {
		clientCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Read, Endpoint: ENDPOINT},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
			Limits:     []core.Limit{permissiveSocketCountLimit},
		})

		serverCtx := core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
				core.HttpPermission{
					Kind_:  permkind.Provide,
					Entity: HOST,
				},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
		})

		serverState := core.NewGlobalState(serverCtx)
		serverState.Logger = zerolog.New(io.Discard)
		serverState.Out = io.Discard

		closeChan := createWebsocketServer(testWebsocketServerConfig{
			host:           HOST,
			messageTimeout: time.Second,
		}, serverCtx)

		var conns []*WebsocketConnection

		for i := 0; i < DEFAULT_MAX_IP_WS_CONNS; i++ {
			conn, _ := websocketConnect(clientCtx, ENDPOINT, core.Option{Name: "insecure", Value: core.True})
			conns = append(conns, conn)
			if !assert.NotNil(t, conn) {
				return
			}
		}

		for _, conn := range conns {
			assert.False(t, conn.closingOrClosed.Load())
		}

		go func() {
			select {
			case closeChan <- struct{}{}:
			case <-time.After(time.Second):
				return
			}
		}()

		time.Sleep(time.Second / 2)

		//check that connections are closed.
		for _, conn := range conns {
			conn.ReadMessage(clientCtx) //read to trigger close
			assert.True(t, conn.closingOrClosed.Load())
		}
	})
}

type testWebsocketServerConfig struct {
	host              core.Host
	echo              bool
	messageTimeout    time.Duration
	doNotReadMessages bool
}

// createWebsocketServer creates a websocket server that echoes the JSON messages it receives if config.echo is true.
// Writing to closeChan closes the server and cancel its context.
func createWebsocketServer(config testWebsocketServerConfig, ctx *core.Context) (closeChan chan struct{}) {
	if ctx == nil {
		ctx = core.NewContext(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
				core.HttpPermission{Kind_: permkind.Provide, Entity: config.host},
			},
			Filesystem: fs_ns.GetOsFilesystem(),
		})
		serverState := core.NewGlobalState(ctx)
		serverState.Logger = zerolog.New(io.Discard)
		serverState.Out = io.Discard
	}

	closeChan = make(chan struct{})

	//log.Println(ctx.GetFileSystem(), string(debug.Stack()))

	httpServerCreated := make(chan struct{})

	go func() {
		defer func() {
			ctx.CancelGracefully()
		}()

		defer func() {
			e := recover()
			if e != nil {
				err := utils.ConvertPanicValueToError(e)
				err = fmt.Errorf("%w: %s", err, debug.Stack())
				fmt.Println(err)
			}
		}()

		wsServer, _ := newWebsocketServer(ctx, config.messageTimeout)
		handler := core.WrapGoFunction(func(ctx *core.Context, rw *http_ns.HttpResponseWriter, req *http_ns.HttpRequest) {
			conn, err := wsServer.Upgrade(rw, req)

			if err != nil {
				fmt.Println("failed to upgrade:", err)
				return
			}

			// echo
			var v core.Value

			if config.doNotReadMessages {
				return
			}

			for ; err == nil; v, err = conn.readJSON(ctx) {
				if v != nil && config.echo {
					conn.sendJSON(ctx, v)
				}
			}
		})

		//log.Println(ctx.GetFileSystem(), string(debug.Stack()))

		server, err := http_ns.NewHttpsServer(ctx, config.host, handler)
		if err != nil {
			log.Panicln("failed to create test server", err)
		}

		httpServerCreated <- struct{}{}

		select {
		case <-closeChan:
		case <-time.After(10 * time.Second):
		}
		go server.Close(ctx) //close in goroutine to speed up closing.
		wsServer.Close(ctx)
	}()

	select {
	case <-time.After(time.Second):
		log.Panicln("timeout")
	case <-httpServerCreated:
	}

	return
}