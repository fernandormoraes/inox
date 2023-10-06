package http_ns

import (
	"testing"

	"github.com/go-git/go-billy/v5/util"
	"github.com/inoxlang/inox/internal/core"
	"github.com/inoxlang/inox/internal/globals/fs_ns"
	"github.com/inoxlang/inox/internal/permkind"
	"github.com/stretchr/testify/assert"
)

func TestGetFilesystemRoutingServerAPI(t *testing.T) {

	//create a context and a filesystem with the passed file contents.
	setup := func(files map[string]string) *core.Context {
		fls := fs_ns.NewMemFilesystem(10_000)

		ctx := core.NewContexWithEmptyState(core.ContextConfig{
			Permissions: append(core.GetDefaultGlobalVarPermissions(),
				core.FilesystemPermission{Kind_: permkind.Read, Entity: core.PathPattern("/...")},
				core.FilesystemPermission{Kind_: permkind.Write, Entity: core.PathPattern("/...")},
				core.LThreadPermission{Kind_: permkind.Create},
			),
			Filesystem: fls,
		}, nil)

		fls.MkdirAll("/routes/", 0o700)

		for file, content := range files {
			err := util.WriteFile(fls, file, []byte(content), 0o700)
			if err != nil {
				assert.FailNow(t, err.Error())
			}
		}

		return ctx
	}

	t.Run("", func(t *testing.T) {
		t.Run("root index.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/index.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			if !assert.NoError(t, err) {
				return
			}

			if !assert.Contains(t, api.endpoints, "/") {
				return
			}
		})

		t.Run("non root index.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/users/index.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			if !assert.NoError(t, err) {
				return
			}

			if !assert.Contains(t, api.endpoints, "/users") {
				return
			}
		})

		t.Run("root GET.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			if !assert.NoError(t, err) {
				return
			}

			if !assert.Contains(t, api.endpoints, "/") {
				return
			}
		})

		t.Run("non root GET.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/users/GET.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			if !assert.NoError(t, err) {
				return
			}

			if !assert.Contains(t, api.endpoints, "/users") {
				return
			}
		})

		t.Run("root GET-users.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET-users.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			if !assert.NoError(t, err) {
				return
			}

			if !assert.Contains(t, api.endpoints, "/users") {
				return
			}
		})

		t.Run("non root GET-users.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET-users.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			if !assert.NoError(t, err) {
				return
			}

			if !assert.Contains(t, api.endpoints, "/users") {
				return
			}
		})
	})

	t.Run("an error is expected if at least two modules handle the same API operation", func(t *testing.T) {

		t.Run("GET-users.ix + /users/index.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET.ix": `
					manifest {
						parameters: {

						}
					}
				`,
				"/routes/index.ix": `
					manifest {
						parameters: {

						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			assert.ErrorContains(t, err, "already implemented")
		})

		t.Run("GET-users.ix + /users/index.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET-users.ix": `
					manifest {
						parameters: {

						}
					}
				`,
				"/routes/users/index.ix": `
					manifest {
						parameters: {

						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			assert.ErrorContains(t, err, "already implemented")
		})

		t.Run("GET-users.ix + /users/GET.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET-users.ix": `
					manifest {
						parameters: {

						}
					}
				`,
				"/routes/users/GET.ix": `
					manifest {
						parameters: {

						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			assert.ErrorContains(t, err, "already implemented")
		})

		t.Run("users.ix + /users/GET.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/users.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
				"/routes/users/GET.ix": `
					manifest {
						parameters: {
	
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")
			assert.ErrorContains(t, err, "already implemented")
		})

	})

	t.Run("GET & OPTIONS handler should not have request body parameters", func(t *testing.T) {
		t.Run("GET.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET.ix": `
					manifest {
						parameters: {
							name: %str
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")

			assert.ErrorIs(t, err, ErrUnexpectedBodyParamsInGETHandler)
		})

		t.Run("GET-users.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/GET-users.ix": `
					manifest {
						parameters: {
							id: %str
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")

			assert.ErrorIs(t, err, ErrUnexpectedBodyParamsInGETHandler)
		})

		t.Run("OPTIONS.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/OPTIONS.ix": `
					manifest {
						parameters: {
							name: %str
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")

			assert.ErrorIs(t, err, ErrUnexpectedBodyParamsInOPTIONSHandler)
		})

		t.Run("OPTIONS-users.ix", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/OPTIONS-users.ix": `
					manifest {
						parameters: {
							id: %str
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			_, err := getFilesystemRoutingServerAPI(ctx, "/routes/")

			assert.ErrorIs(t, err, ErrUnexpectedBodyParamsInOPTIONSHandler)
		})
	})

	t.Run("parameters", func(t *testing.T) {
		t.Run("POST with a request body parameter", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/POST-users.ix": `
					manifest {
						parameters: {
							name: %str
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")

			if !assert.NoError(t, err, ErrUnexpectedBodyParamsInGETHandler) {
				return
			}

			usersEndpt := api.endpoints["/users"]
			if !assert.Len(t, usersEndpt.operations, 1) {
				return
			}

			operation := usersEndpt.operations[0]
			if !assert.Equal(t, operation.httpMethod, "POST") {
				return
			}

			if !assert.IsType(t, (*core.ObjectPattern)(nil), operation.jsonRequestBody) {
				return
			}

			pattern := operation.jsonRequestBody.(*core.ObjectPattern)
			if !assert.Equal(t, 1, pattern.EntryCount()) {
				return
			}

			namePattern, optional, ok := pattern.Entry("name")
			if !assert.True(t, ok) {
				return
			}

			assert.Equal(t, namePattern, core.STR_PATTERN)
			assert.False(t, optional)
		})

		t.Run("POST with an injected parameter and no request body parameters", func(t *testing.T) {
			ctx := setup(map[string]string{
				"/routes/POST-users.ix": `
					manifest {
						parameters: {
							_body: %reader
						}
					}
				`,
			})
			defer ctx.CancelGracefully()

			api, err := getFilesystemRoutingServerAPI(ctx, "/routes/")

			if !assert.NoError(t, err, ErrUnexpectedBodyParamsInGETHandler) {
				return
			}

			usersEndpt := api.endpoints["/users"]
			if !assert.Len(t, usersEndpt.operations, 1) {
				return
			}

			operation := usersEndpt.operations[0]
			if !assert.Equal(t, operation.httpMethod, "POST") {
				return
			}

			assert.Nil(t, operation.jsonRequestBody)
		})

	})
}