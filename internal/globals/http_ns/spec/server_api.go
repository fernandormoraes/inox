package spec

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inoxlang/inox/internal/core"
	"github.com/inoxlang/inox/internal/globals/fs_ns"
	"github.com/inoxlang/inox/internal/inoxconsts"
	"github.com/inoxlang/inox/internal/parse"
	"github.com/inoxlang/inox/internal/utils"
)

const (
	FS_ROUTING_BODY_PARAM   = "_body"
	FS_ROUTING_METHOD_PARAM = "_method"
	FS_ROUTING_INDEX_MODULE = "index" + inoxconsts.INOXLANG_FILE_EXTENSION
)

var (
	ErrUnexpectedBodyParamsInGETHandler      = errors.New("unexpected request body parmameters in GET handler")
	ErrUnexpectedBodyParamsInOPTIONSHandler  = errors.New("unexpected request body parmameters in OPTIONS handler")
	ErrUnexpectedBodyParamsInCatchAllHandler = errors.New("unexpected request body parmameters in catch-all handler")
)

type ServerApiResolutionConfig struct {
	IgnoreModulesWithErrors bool
}

func GetFSRoutingServerAPI(ctx *core.Context, dir string, config ServerApiResolutionConfig) (*API, error) {
	preparedModuleCache := map[string]*core.GlobalState{}
	defer func() {
		for _, state := range preparedModuleCache {
			state.Ctx.CancelGracefully()
		}
	}()

	endpoints := map[string]*ApiEndpoint{}

	if dir != "" {
		err := addFilesysteDirEndpoints(ctx, config, endpoints, dir, "/", preparedModuleCache)
		if err != nil {
			return nil, err
		}
	}

	return NewAPI(endpoints)
}

// addFilesysteDirEndpoints recursively add the endpoints provided by dir and its subdirectories.
func addFilesysteDirEndpoints(
	ctx *core.Context,
	config ServerApiResolutionConfig,
	endpoints map[string]*ApiEndpoint,
	dir,
	urlDirPath string,
	preparedModuleCache map[string]*core.GlobalState,
) error {
	fls := ctx.GetFileSystem()
	entries, err := fls.ReadDir(dir)

	//Normalize the directory and the URL directory.

	dir = core.AppendTrailingSlashIfNotPresent(dir)
	urlDirPath = core.AppendTrailingSlashIfNotPresent(urlDirPath)

	if err != nil {
		return err
	}

	urlDirPathNoTrailingSlash := strings.TrimSuffix(urlDirPath, "/")
	if urlDirPath == "/" {
		urlDirPathNoTrailingSlash = "/"
	}

	parentState, _ := ctx.GetState()

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		entryName := entry.Name()
		absEntryPath := filepath.Join(dir, entryName)

		//If the entry is a directory we recursively add the endpoints defined inside it.
		if entry.IsDir() {
			subDir := absEntryPath + "/"
			urlSubDir := ""
			if entryName[0] == ':' {
				urlSubDir = filepath.Join(urlDirPath, "{"+entryName[1:]+"}") + "/"
			} else {
				urlSubDir = filepath.Join(urlDirPath, entryName) + "/"
			}

			err := addFilesysteDirEndpoints(ctx, config, endpoints, subDir, urlSubDir, preparedModuleCache)
			if err != nil {
				return err
			}
			continue
		}

		//Ignore non-Inox files and .spec.ix files.
		if !strings.HasSuffix(entryName, inoxconsts.INOXLANG_FILE_EXTENSION) || strings.HasSuffix(entryName, inoxconsts.INOXLANG_SPEC_FILE_SUFFIX) {
			continue
		}

		entryNameNoExt := strings.TrimSuffix(entryName, inoxconsts.INOXLANG_FILE_EXTENSION)

		//Determine the endpoint path and method by 'parsing' the entry name.
		var endpointPath string
		var method string //if empty the handler module supports several methods
		returnErrIfNotModule := true

		if slices.Contains(FS_ROUTING_METHODS, entryNameNoExt) { //GET.ix, POST.ix, ...
			//add operation
			method = entryNameNoExt
			endpointPath = urlDirPathNoTrailingSlash
		} else {
			beforeName, name, ok := strings.Cut(entryNameNoExt, "-")

			if ok && slices.Contains(FS_ROUTING_METHODS, beforeName) { //POST-... , GET-...
				method = beforeName
				endpointPath = filepath.Join(urlDirPath, name)
			} else if entryName == FS_ROUTING_INDEX_MODULE { //index.ix
				endpointPath = urlDirPathNoTrailingSlash
			} else { //example: about.ix
				endpointPath = filepath.Join(urlDirPath, entryNameNoExt)
				returnErrIfNotModule = false
			}
		}

		//Remove trailing slash.
		if endpointPath != "/" {
			endpointPath = strings.TrimSuffix(endpointPath, "/")
		}

		//Determine if the file is an Inox module.
		chunk, err := core.ParseFileChunk(absEntryPath, fls)
		if err != nil {
			if config.IgnoreModulesWithErrors {
				continue
			}
			return fmt.Errorf("failed to parse %q: %w", absEntryPath, err)
		}

		if chunk.Node.Manifest == nil { //not a module
			if returnErrIfNotModule {
				return fmt.Errorf("%q is not a module", absEntryPath)
			}
			continue
		}

		//Add endpoint.
		endpt := endpoints[endpointPath]
		if endpt == nil && endpointPath == "/" {
			endpt = &ApiEndpoint{
				path: "/",
			}
			endpoints[endpointPath] = endpt
		} else if endpt == nil {
			//Add endpoint into the API.
			endpt = &ApiEndpoint{
				path: endpointPath,
			}
			endpoints[endpointPath] = endpt
			if endpointPath == "" || endpointPath[0] != '/' {
				return fmt.Errorf("invalid endpoint path %q", endpointPath)
			}
		}

		//Check the same operation is not already defined.
		for _, op := range endpt.operations {
			if op.httpMethod == method || method == "" {
				if op.handlerModule != nil {
					return fmt.Errorf(
						"operation %s %q is already implemented by the module %q; unexpected module %q",
						op.httpMethod, endpointPath, op.handlerModule.Name(), absEntryPath)
				}
				return fmt.Errorf(
					"operation %s %q is already implemented; unexpected module %q",
					op.httpMethod, endpointPath, absEntryPath)
			}
		}

		endpt.operations = append(endpt.operations, ApiOperation{
			httpMethod: method,
		})
		operation := &endpt.operations[len(endpt.operations)-1]

		manifestObj := chunk.Node.Manifest.Object.(*parse.ObjectLiteral)
		dbSection, _ := manifestObj.PropValue(core.MANIFEST_DATABASES_SECTION_NAME)

		var parentCtx *core.Context = ctx

		//If the databases are defined in another module we retrieve this module.
		if path, ok := dbSection.(*parse.AbsolutePathLiteral); ok {
			if cache, ok := preparedModuleCache[path.Value]; ok {
				parentCtx = cache.Ctx

				//if false there is nothing to do as the parentCtx is already set to ctx.
			} else if parentState.Module.Name() != path.Value {

				state, _, _, err := core.PrepareLocalModule(core.ModulePreparationArgs{
					Fpath:                     path.Value,
					ParsingCompilationContext: ctx,

					ParentContext:         ctx,
					ParentContextRequired: true,
					DefaultLimits: []core.Limit{
						core.MustMakeNotAutoDepletingCountLimit(fs_ns.FS_READ_LIMIT_NAME, 10_000_000),
					},

					Out:                     io.Discard,
					DataExtractionMode:      true,
					ScriptContextFileSystem: fls,
					PreinitFilesystem:       fls,
				})

				if err != nil {
					if config.IgnoreModulesWithErrors {
						delete(endpoints, endpointPath)
						continue
					} else {
						return err
					}
				}

				preparedModuleCache[path.Value] = state
				parentCtx = state.Ctx
			}
		}

		state, mod, _, err := core.PrepareLocalModule(core.ModulePreparationArgs{
			Fpath:                     absEntryPath,
			ParsingCompilationContext: parentCtx,

			ParentContext:         parentCtx,
			ParentContextRequired: true,
			DefaultLimits: []core.Limit{
				core.MustMakeNotAutoDepletingCountLimit(fs_ns.FS_READ_LIMIT_NAME, 10_000_000),
			},

			Out:                     io.Discard,
			DataExtractionMode:      true,
			ScriptContextFileSystem: fls,
			PreinitFilesystem:       fls,
		})

		if state != nil {
			defer state.Ctx.CancelGracefully()
		}

		if err != nil {
			if config.IgnoreModulesWithErrors {
				delete(endpoints, endpointPath)
				continue
			} else {
				return err
			}
		}

		operation.handlerModule = mod

		bodyParams := utils.FilterSlice(state.Manifest.Parameters.NonPositionalParameters(), func(p core.ModuleParameter) bool {
			return !strings.HasPrefix(p.Name(), "_")
		})

		if len(bodyParams) > 0 {
			if method == "GET" {
				return fmt.Errorf("%w: module %q", ErrUnexpectedBodyParamsInGETHandler, absEntryPath)
			} else if method == "OPTIONS" {
				return fmt.Errorf("%w: module %q", ErrUnexpectedBodyParamsInOPTIONSHandler, absEntryPath)
			}

			var paramEntries []core.ObjectPatternEntry

			for _, param := range bodyParams {
				name := param.Name()
				paramEntries = append(paramEntries, core.ObjectPatternEntry{
					Name:       name,
					IsOptional: false,
					Pattern:    param.Pattern(),
				})
			}

			operation.jsonRequestBody = core.NewInexactObjectPattern(paramEntries)
		}
	}

	//update catch-all endpoints
	for _, endpt := range endpoints {
		if len(endpt.operations) == 1 && endpt.operations[0].httpMethod == "" {
			operation := endpt.operations[0]
			endpt.operations = nil
			endpt.catchAll = true
			endpt.catchAllHandler = operation.handlerModule
		}
	}

	return nil
}
