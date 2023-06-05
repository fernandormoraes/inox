package inox_ns

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/inoxlang/inox/internal/afs"
	"github.com/inoxlang/inox/internal/config"
	core "github.com/inoxlang/inox/internal/core"

	symbolic "github.com/inoxlang/inox/internal/core/symbolic"
	"github.com/inoxlang/inox/internal/default_state"
	"github.com/inoxlang/inox/internal/utils"
)

const (
	DEFAULT_MAX_ALLOWED_WARNINGS = 10
)

var (
	ErrExecutionAbortedTooManyWarnings = errors.New("execution was aborted because there are too many warnings")
	ErrUserRefusedExecution            = errors.New("user refused execution")
	ErrNoProvidedConfirmExecPrompt     = errors.New("risk score too high and no provided way to show confirm prompt")
)

type ScriptPreparationArgs struct {
	Fpath string

	CliArgs []string
	Args    *core.Object

	ParsingCompilationContext *core.Context
	ParentContext             *core.Context
	UseContextAsParent        bool
	IgnoreNonCriticalIssues   bool
	AllowMissingEnvVars       bool

	Out    io.Writer //defaults to os.Stdout
	LogOut io.Writer //defaults to Out

	//used during the preinit
	PreinitFilesystem afs.Filesystem

	//used to create the context, it defaults to the OS filesystem
	ScriptContextFileSystem afs.Filesystem
}

// PrepareLocalScript parses & checks a script located in the filesystem and initialize its state.
func PrepareLocalScript(args ScriptPreparationArgs) (state *core.GlobalState, mod *core.Module, manif *core.Manifest, finalErr error) {
	// parse module

	absPath, pathErr := filepath.Abs(args.Fpath)
	if pathErr != nil {
		finalErr = fmt.Errorf("failed to get absolute path of script: %w", pathErr)
		return
	}

	args.Fpath = absPath

	module, parsingErr := core.ParseLocalModule(core.LocalModuleParsingConfig{
		ModuleFilepath:                      args.Fpath,
		Context:                             args.ParsingCompilationContext,
		RecoverFromNonExistingIncludedFiles: args.IgnoreNonCriticalIssues,
	})

	mod = module

	if parsingErr != nil && mod == nil {
		finalErr = parsingErr
		return
	}

	//create context and state

	var ctx *core.Context

	var parentContext *core.Context
	if args.UseContextAsParent {
		parentContext = args.ParentContext
	}

	var manifest *core.Manifest
	var preinitState *core.TreeWalkState
	var preinitErr error
	var preinitStaticCheckErrors []*core.StaticCheckError

	if mod != nil {
		manifest, preinitState, preinitStaticCheckErrors, preinitErr = mod.PreInit(core.PreinitArgs{
			GlobalConsts:          mod.MainChunk.Node.GlobalConstantDeclarations,
			Preinit:               mod.MainChunk.Node.Preinit,
			PreinitFilesystem:     args.PreinitFilesystem,
			DefaultLimitations:    default_state.GetDefaultScriptLimitations(),
			AddDefaultPermissions: true,
			IgnoreUnknownSections: args.IgnoreNonCriticalIssues,
			IgnoreConstDeclErrors: args.IgnoreNonCriticalIssues,
		})

		if manifest == nil {
			manifest = core.NewEmptyManifest()
		}

	} else {
		manifest = core.NewEmptyManifest()
	}

	var ctxErr error

	ctx, ctxErr = default_state.NewDefaultContext(default_state.DefaultContextConfig{
		Permissions:     manifest.RequiredPermissions,
		Limitations:     manifest.Limitations,
		HostResolutions: manifest.HostResolutions,
		ParentContext:   parentContext,
		Filesystem:      args.ScriptContextFileSystem,
	})

	if ctxErr != nil {
		finalErr = ctxErr
		return
	}

	defer func() {
		if finalErr != nil {
			ctx.Cancel()
		}
	}()

	out := args.Out
	if out == nil {
		out = os.Stdout
	}

	globalState, err := default_state.NewDefaultGlobalState(ctx, default_state.DefaultGlobalStateConfig{
		EnvPattern:          manifest.EnvPattern,
		AllowMissingEnvVars: args.AllowMissingEnvVars,
		Out:                 out,
		LogOut:              args.LogOut,
	})
	if err != nil {
		finalErr = fmt.Errorf("failed to create global state: %w", err)
		return
	}
	state = globalState
	state.Module = mod
	state.PrenitStaticCheckErrors = preinitStaticCheckErrors
	state.MainPreinitError = preinitErr

	//pass patterns & host aliases of the preinit state to the state
	if preinitState != nil {
		for name, patt := range preinitState.Global.Ctx.GetNamedPatterns() {
			if _, ok := core.DEFAULT_NAMED_PATTERNS[name]; ok {
				continue
			}
			state.Ctx.AddNamedPattern(name, patt)
		}
		for name, ns := range preinitState.Global.Ctx.GetPatternNamespaces() {
			if _, ok := core.DEFAULT_PATTERN_NAMESPACES[name]; ok {
				continue
			}
			state.Ctx.AddPatternNamespace(name, ns)
		}
		for name, val := range preinitState.Global.Ctx.GetHostAliases() {
			state.Ctx.AddHostAlias(name, val)
		}
	}

	// CLI arguments | arguments of imported module
	var modArgs *core.Object
	var modArgsError error

	if args.Args != nil {
		modArgs, modArgsError = manifest.Parameters.GetArguments(ctx, args.Args)
	} else if args.CliArgs != nil {
		args, err := manifest.Parameters.GetArgumentsFromCliArgs(ctx, args.CliArgs)
		if err != nil {
			modArgsError = fmt.Errorf("%w\nusage: %s", err, manifest.Usage())
		} else {
			modArgs = args
		}
	} else {
		modArgs = core.NewObject()
	}

	if modArgsError == nil {
		state.Globals.Set(core.MOD_ARGS_VARNAME, modArgs)
	}

	// static check

	staticCheckData, staticCheckErr := core.StaticCheck(core.StaticCheckInput{
		Module:  mod,
		Node:    mod.MainChunk.Node,
		Chunk:   mod.MainChunk,
		Globals: state.Globals,
		AdditionalGlobalConsts: func() []string {
			if modArgsError != nil {
				return []string{core.MOD_ARGS_VARNAME}
			}
			return nil
		}(),
		Patterns:          state.Ctx.GetNamedPatterns(),
		PatternNamespaces: state.Ctx.GetPatternNamespaces(),
	})

	state.StaticCheckData = staticCheckData

	if finalErr == nil && staticCheckErr != nil && staticCheckData == nil {
		finalErr = staticCheckErr
		return
	}

	if parsingErr != nil {
		if len(mod.OriginalErrors) > 1 ||
			(len(mod.OriginalErrors) == 1 && !utils.SliceContains(symbolic.SUPPORTED_PARSING_ERRORS, mod.OriginalErrors[0].Kind())) {
			finalErr = parsingErr
			return
		}
		//we continue if there is a single error AND the error is supported by the symbolic evaluation
	}

	if preinitErr != nil {
		finalErr = preinitErr
		return
	}

	// symbolic check

	globals := map[string]symbolic.ConcreteGlobalValue{}
	state.Globals.Foreach(func(k string, v core.Value, isConst bool) error {
		globals[k] = symbolic.ConcreteGlobalValue{
			Value:      v,
			IsConstant: isConst,
		}
		return nil
	})

	delete(globals, core.MOD_ARGS_VARNAME)
	additionalSymbolicGlobals := map[string]symbolic.SymbolicValue{
		core.MOD_ARGS_VARNAME: manifest.Parameters.GetSymbolicArguments(),
	}

	symbolicCtx, err_ := state.Ctx.ToSymbolicValue()
	if err_ != nil {
		finalErr = parsingErr
		return
	}

	symbolicData, err_ := symbolic.SymbolicEvalCheck(symbolic.SymbolicEvalCheckInput{
		Node:                           mod.MainChunk.Node,
		Module:                         state.Module.ToSymbolic(),
		Globals:                        globals,
		AdditionalSymbolicGlobalConsts: additionalSymbolicGlobals,
		Context:                        symbolicCtx,
	})

	if symbolicData != nil {
		state.SymbolicData.AddData(symbolicData)
	}

	if parsingErr != nil { //priority to parsing error
		finalErr = parsingErr
	} else if finalErr == nil {
		switch {
		case preinitErr != nil:
			finalErr = preinitErr
		case err_ != nil:
			finalErr = err_
		case staticCheckErr != nil:
			finalErr = staticCheckErr
		case finalErr == nil && modArgsError != nil:
			finalErr = modArgsError
		}
	}

	return state, mod, manifest, finalErr
}

type RunScriptArgs struct {
	Fpath                     string
	PassedCLIArgs             []string
	PassedArgs                *core.Object
	ParsingCompilationContext *core.Context
	ParentContext             *core.Context
	UseContextAsParent        bool
	//used during the preinit
	PreinitFilesystem afs.Filesystem

	UseBytecode      bool
	OptimizeBytecode bool
	ShowBytecode     bool

	AllowMissingEnvVars bool
	IgnoreHighRiskScore bool

	//output for execution, if nil os.Stdout is used
	Out io.Writer
}

// RunLocalScript runs a script located in the filesystem.
func RunLocalScript(args RunScriptArgs) (core.Value, *core.GlobalState, *core.Module, error) {

	if args.UseContextAsParent && args.ParentContext == nil {
		return nil, nil, nil, errors.New(".UseContextAsParent is set to true but passed .Context is nil")
	}

	state, mod, manifest, err := PrepareLocalScript(ScriptPreparationArgs{
		Fpath:                     args.Fpath,
		CliArgs:                   args.PassedCLIArgs,
		Args:                      args.PassedArgs,
		ParsingCompilationContext: args.ParsingCompilationContext,
		ParentContext:             args.ParentContext,
		UseContextAsParent:        args.UseContextAsParent,
		Out:                       args.Out,
		AllowMissingEnvVars:       args.AllowMissingEnvVars,
		PreinitFilesystem:         args.PreinitFilesystem,
	})

	if err != nil {
		return nil, state, mod, err
	}

	out := state.Out

	//show warnings
	warnings := state.SymbolicData.Warnings()
	for _, warning := range warnings {
		fmt.Fprintln(out, warning.LocatedMessage)
	}

	if len(warnings) > DEFAULT_MAX_ALLOWED_WARNINGS { //TODO: make the max configurable
		return nil, nil, nil, ErrExecutionAbortedTooManyWarnings
	}

	riskScore := core.ComputeProgramRiskScore(mod, manifest)

	// if the program is risky ask the user to confirm the execution
	if !args.IgnoreHighRiskScore && riskScore > config.DEFAULT_TRUSTED_RISK_SCORE {
		waitConfirmPrompt := args.ParsingCompilationContext.GetWaitConfirmPrompt()
		if waitConfirmPrompt == nil {
			return nil, nil, nil, ErrNoProvidedConfirmExecPrompt
		}
		msg := bytes.NewBufferString(mod.Name())
		msg.WriteString("\nrisk score is ")
		msg.WriteString(riskScore.ValueAndLevel())
		msg.WriteString("\nthe program is asking for the following permissions:\n")

		for _, perm := range manifest.RequiredPermissions {
			//ignore global var permissions
			if _, ok := perm.(core.GlobalVarPermission); ok {
				continue
			}
			msg.WriteByte('\t')
			msg.WriteString(perm.String())
			msg.WriteByte('\n')
		}
		msg.WriteString("allow execution (y,yes) ? ")

		if ok, err := waitConfirmPrompt(msg.String(), []string{"y", "yes"}); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to show confirm prompt to user: %w", err)
		} else if !ok {
			return nil, nil, nil, ErrUserRefusedExecution
		}
	}

	state.InitSystemGraph()

	defer state.Ctx.Cancel()

	//execute the script

	if args.UseBytecode {
		tracer := io.Discard
		if args.ShowBytecode {
			tracer = out
		}
		res, err := core.EvalVM(state.Module, state, core.BytecodeEvaluationConfig{
			Tracer:               tracer,
			ShowCompilationTrace: args.ShowBytecode,
			OptimizeBytecode:     args.OptimizeBytecode,
			CompilationContext:   args.ParsingCompilationContext,
		})

		return res, state, mod, err
	}

	res, err := core.TreeWalkEval(state.Module.MainChunk.Node, core.NewTreeWalkStateWithGlobal(state))
	return res, state, mod, err
}

// GetCheckData returns a map that can be safely marshaled to JSON, the data has the following structure:
//
//	{
//		parsingErrors: [ ..., {text: <string>, location: <parse.SourcePosition>}, ... ]
//		staticCheckErrors: [ ..., {text: <string>, location: <parse.SourcePosition>}, ... ]
//		symbolicCheckErrors: [ ..., {text: <string>, location: <parse.SourcePosition>}, ... ]
//	}
func GetCheckData(fpath string, compilationCtx *core.Context, out io.Writer) map[string]any {
	state, mod, _, err := PrepareLocalScript(ScriptPreparationArgs{
		Fpath:                     fpath,
		Args:                      nil,
		ParsingCompilationContext: compilationCtx,
		ParentContext:             nil,
		Out:                       out,
	})

	data := map[string]any{
		"parsingErrors":       []any{},
		"staticCheckErrors":   []any{},
		"symbolicCheckErrors": []any{},
	}

	if err == nil {
		return data
	}

	if err != nil && state == nil && mod == nil {
		return data
	}

	{
		i := -1

		fmt.Fprintln(os.Stderr, len(mod.ParsingErrors), len(mod.ParsingErrorPositions))
		data["parsingErrors"] = utils.MapSlice(mod.ParsingErrors, func(err core.Error) any {
			i++
			return map[string]any{
				"text":     err.Text(),
				"location": mod.ParsingErrorPositions[i],
			}
		})
	}

	if state != nil && state.StaticCheckData != nil {
		i := -1
		data["staticCheckErrors"] = utils.MapSlice(state.StaticCheckData.Errors(), func(err *core.StaticCheckError) any {
			i++
			return map[string]any{
				"text":     err.Message,
				"location": err.Location[0],
			}
		})
		i = -1

		data["symbolicCheckErrors"] = utils.MapSlice(state.SymbolicData.Errors(), func(err symbolic.SymbolicEvaluationError) any {
			i++
			return map[string]any{
				"text":     err.Message,
				"location": err.Location[0],
			}
		})
	}

	return data
}
