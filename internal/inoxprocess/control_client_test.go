package inoxprocess

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/inoxlang/inox/internal/core"
	"github.com/inoxlang/inox/internal/globals/fs_ns"
	"github.com/inoxlang/inox/internal/globals/http_ns"
	"github.com/inoxlang/inox/internal/globals/net_ns"
	nettypes "github.com/inoxlang/inox/internal/net_types"
	"github.com/inoxlang/inox/internal/permkind"
	"github.com/inoxlang/inox/internal/utils"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
)

func TestControlClient(t *testing.T) {
	RegisterTypesInGob()

	var lock sync.Mutex
	var receivedMessagePayloads [][]byte
	var receivedMessageTypes []net_ns.WebsocketMessageType
	var errors []error

	//messages read by the server and sent to the controlled process
	var controlChan chan Message

	controlServerURL := utils.Must(url.Parse("wss://localhost:8310"))

	setup := func() (*core.Context, ControlledProcessToken, *http.Server) {
		lock.Lock()
		receivedMessagePayloads = nil
		receivedMessageTypes = nil
		errors = nil
		controlChan = make(chan Message, 10)
		lock.Unlock()

		permissiveSocketCountLimit := core.MustMakeNotDecrementingLimit(net_ns.WS_SIMUL_CONN_TOTAL_LIMIT_NAME, 10_000)

		ctx := core.NewContexWithEmptyState(core.ContextConfig{
			Permissions: []core.Permission{
				core.WebsocketPermission{Kind_: permkind.Provide},
				core.WebsocketPermission{Kind_: permkind.Read, Endpoint: core.Host("wss://localhost:8310")},
				core.WebsocketPermission{Kind_: permkind.Write, Endpoint: core.Host("wss://localhost:8310")},
			},
			Filesystem: fs_ns.NewMemFilesystem(10_000),
			Limits:     []core.Limit{permissiveSocketCountLimit},
		}, os.Stdout)

		server, err := net_ns.NewWebsocketServer(ctx)

		if !assert.NoError(t, err) {
			return nil, "", nil
		}

		cert, key, err := http_ns.GenerateSelfSignedCertAndKey()

		if !assert.NoError(t, err) {
			return nil, "", nil
		}

		token := MakeControlledProcessToken()

		httpServer, err := http_ns.NewGolangHttpServer(ctx, http_ns.GolangHttpServerConfig{
			Addr:           "localhost:8310",
			PemEncodedCert: string(pem.EncodeToMemory(cert)),
			PemEncodedKey:  string(pem.EncodeToMemory(key)),
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := server.UpgradeGoValues(w, r, func(remoteAddrPort nettypes.RemoteAddrWithPort, remoteAddr nettypes.RemoteIpAddr, currentConns []*net_ns.WebsocketConnection) error {
					return nil
				})

				if err != nil {
					lock.Lock()
					errors = append(errors, err)
					lock.Unlock()

					return
				}

				conn.SetPingHandler(ctx, func(data string) error {
					lock.Lock()
					t.Log("receive ping")
					receivedMessageTypes = append(receivedMessageTypes, net_ns.WebsocketPingMessage)
					receivedMessagePayloads = append(receivedMessagePayloads, []byte(data))
					lock.Unlock()
					return nil
				})

				//read messages from controlChan and send them to the controlled process
				go func() {
					for !conn.IsClosedOrClosing() {
						lock.Lock()
						channel := controlChan
						lock.Unlock()

						select {
						case msg := <-channel:
							payload := MustEncodeMessage(msg)
							err := conn.WriteMessage(ctx, net_ns.WebsocketBinaryMessage, payload)
							if err != nil {
								t.Log(err)
							}
						default:
							time.Sleep(10 * time.Millisecond)
						}
					}
				}()

				for !conn.IsClosedOrClosing() {
					msgtype, p, err := conn.ReadMessage(ctx)
					if err != nil {
						conn.Close()
						lock.Lock()
						errors = append(errors, err)
						lock.Unlock()
						return
					}
					lock.Lock()
					receivedMessageTypes = append(receivedMessageTypes, msgtype)
					receivedMessagePayloads = append(receivedMessagePayloads, p)
					lock.Unlock()
				}
			}),
		})

		if !assert.NoError(t, err) {
			return nil, "", nil
		}
		return ctx, token, httpServer
	}

	t.Run("base case", func(t *testing.T) {
		ctx, token, httpServer := setup()
		if httpServer == nil {
			return
		}
		defer ctx.CancelGracefully()
		defer httpServer.Close()

		start := time.Now()
		go httpServer.ListenAndServeTLS("", "")

		client, err := ConnectToProcessControlServer(ctx, controlServerURL, token)
		if !assert.NoError(t, err) {
			return
		}
		defer client.Conn().Close()

		go client.StartControl()

		time.Sleep(time.Second)

		lock.Lock()
		defer lock.Unlock()

		t.Log(errors)

		if !assert.NotEmpty(t, receivedMessageTypes) {
			return
		}

		firstMsgType := receivedMessageTypes[0]
		if !assert.Equal(t, net_ns.WebsocketPingMessage, firstMsgType) {
			return
		}

		//check heartbeats
		for i, msgType := range receivedMessageTypes {
			if msgType != net_ns.WebsocketPingMessage {
				continue
			}
			msgPayload := receivedMessagePayloads[i]

			var hearbeat heartbeat
			err = json.Unmarshal(msgPayload, &hearbeat)
			if !assert.NoError(t, err) {
				return
			}

			assert.WithinDuration(t, start.Add(time.Duration(i+1)*HEARTBEAT_INTERVAL), hearbeat.Time, 10*time.Millisecond)
		}
	})

	t.Run("sending a stop", func(t *testing.T) {
		ctx, token, httpServer := setup()
		if httpServer == nil {
			return
		}
		defer ctx.CancelGracefully()
		defer httpServer.Close()

		go httpServer.ListenAndServeTLS("", "")

		client, err := ConnectToProcessControlServer(ctx, controlServerURL, token)
		if !assert.NoError(t, err) {
			return
		}
		defer client.Conn().Close()

		go func() {
			lock.Lock()
			channel := controlChan
			lock.Unlock()

			channel <- Message{
				ULID:  ulid.Make(),
				Inner: StopAllRequest{},
			}

			//stop the control loop to prevent the test to hang
			time.Sleep(2 * time.Second)
			ctx.CancelGracefully()
		}()

		err = client.StartControl()
		assert.ErrorIs(t, err, ErrControlLoopEnd)
	})
}
