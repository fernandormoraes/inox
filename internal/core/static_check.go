package core

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/inoxlang/inox/internal/afs"
	permkind "github.com/inoxlang/inox/internal/core/permkind"
	"github.com/inoxlang/inox/internal/core/symbolic"
	"github.com/inoxlang/inox/internal/globals/globalnames"
	"github.com/inoxlang/inox/internal/parse"
	"github.com/inoxlang/inox/internal/utils"
)

const (
	CHECK_ERR_PREFIX  = "check: "
	MAX_NAME_BYTE_LEN = 64
)

var (
	STATIC_CHECK_DATA_PROP_NAMES = []string{"errors"}
	ErrForbiddenNodeinPreinit    = errors.New("forbidden node type in preinit block")

	_ parse.LocatedError = &StaticCheckError{}
)

// StaticCheck performs various checks on an AST, like checking duplicate declarations and keys or checking that statements like return,
// break and continue are not misplaced. No type checks are performed.
func StaticCheck(input StaticCheckInput) (*StaticCheckData, error) {
	if input.State == nil {
		return nil, errors.New("missing state")
	}

	globals := make(map[parse.Node]map[string]globalVarInfo)

	var module parse.Node //ok if nil

	switch input.Node.(type) {
	case *parse.Chunk, *parse.EmbeddedModule:
		module = input.Node
	}

	globals[module] = map[string]globalVarInfo{}
	input.Globals.Foreach(func(name string, v Value, isStartConstant bool) error {
		globals[module][name] = globalVarInfo{isConst: true, isStartConstant: true}
		return nil
	})

	for _, name := range input.AdditionalGlobalConsts {
		globals[module][name] = globalVarInfo{isConst: true}
	}

	shellLocalVars := make(map[string]bool)

	localVars := make(map[parse.Node]map[string]localVarInfo)
	localVars[module] = map[string]localVarInfo{}
	for k := range input.ShellLocalVars {
		localVars[module][k] = localVarInfo{}
		shellLocalVars[k] = true
	}

	patterns := make(map[parse.Node]map[string]int)
	patterns[module] = map[string]int{}
	for k := range input.Patterns {
		patterns[module][k] = 0
	}

	patternNamespaces := make(map[parse.Node]map[string]int)
	patternNamespaces[module] = map[string]int{}
	for k := range input.PatternNamespaces {
		patternNamespaces[module][k] = 0
	}

	checker := &checker{
		checkInput:        input,
		fnDecls:           make(map[parse.Node]map[string]int),
		structDefs:        make(map[parse.Node]map[string]int),
		globalVars:        globals,
		localVars:         localVars,
		shellLocalVars:    shellLocalVars,
		properties:        make(map[*parse.ObjectLiteral]*propertyInfo),
		hostAliases:       make(map[parse.Node]map[string]int),
		patterns:          patterns,
		patternNamespaces: patternNamespaces,
		currentModule:     input.Module,
		chunk:             input.Chunk,
		store:             make(map[parse.Node]interface{}),
		data: &StaticCheckData{
			fnData:      map[*parse.FunctionExpression]*FunctionStaticData{},
			mappingData: map[*parse.MappingExpression]*MappingStaticData{},
		},
	}

	if module != nil {
		var statements []parse.Node
		if chunk, ok := module.(*parse.Chunk); ok {
			statements = chunk.Statements
		} else {
			statements = module.(*parse.EmbeddedModule).Statements
		}

		checker.defineStructs(module, statements)
	}

	err := checker.check(input.Node)
	if err != nil {
		return nil, err
	}
	return checker.data, combineStaticCheckErrors(checker.data.errors...)
}

// see Check function.
type checker struct {
	currentModule            *Module //can be nil
	chunk                    *parse.ParsedChunk
	inclusionImportStatement *parse.InclusionImportStatement // can be nil
	moduleImportStatement    *parse.ImportStatement          //can be nil
	parentChecker            *checker                        //can be nil
	checkInput               StaticCheckInput

	//key: *parse.Chunk|*parse.EmbeddedModule
	fnDecls map[parse.Node]map[string]int

	//key: *parse.Chunk|*parse.EmbeddedModule
	structDefs map[parse.Node]map[string]int

	//key: *parse.Chunk|*parse.EmbeddedModule
	globalVars map[parse.Node]map[string]globalVarInfo

	//key: *parse.Chunk|*parse.EmbeddedModule|*parse.FunctionExpression
	localVars map[parse.Node]map[string]localVarInfo

	properties map[*parse.ObjectLiteral]*propertyInfo

	//key: *parse.Chunk|*parse.EmbeddedModule
	hostAliases map[parse.Node]map[string]int

	//key: *parse.Chunk|*parse.EmbeddedModule
	patterns map[parse.Node]map[string]int

	//key: *parse.Chunk|*parse.EmbeddedModule
	patternNamespaces map[parse.Node]map[string]int

	shellLocalVars map[string]bool

	store map[parse.Node]any

	data *StaticCheckData
}

// globalVarInfo represents the information stored about a global variable during checking.
type globalVarInfo struct {
	isConst         bool
	isStartConstant bool
	fnExpr          *parse.FunctionExpression
}

// locallVarInfo represents the information stored about a local variable during checking.
type localVarInfo struct {
	isGroupMatchingVar bool
}

// propertyInfo represents the information stored about the properties of an object literal during checking.
type propertyInfo struct {
	known map[string]bool
}

type StaticCheckError struct {
	Message        string
	LocatedMessage string
	Location       parse.SourcePositionStack
}

func NewStaticCheckError(s string, location parse.SourcePositionStack) *StaticCheckError {
	return &StaticCheckError{
		Message:        CHECK_ERR_PREFIX + s,
		LocatedMessage: CHECK_ERR_PREFIX + location.String() + s,
		Location:       location,
	}
}

func (err StaticCheckError) Error() string {
	return err.LocatedMessage
}

func (err StaticCheckError) Err() Error {
	//TODO: cache (thread safe)
	return NewError(err, createRecordFromSourcePositionStack(err.Location))

}
func (err StaticCheckError) MessageWithoutLocation() string {
	return err.Message
}

func (err StaticCheckError) LocationStack() parse.SourcePositionStack {
	return err.Location
}

func (checker *checker) makeCheckingError(node parse.Node, s string) *StaticCheckError {
	location := checker.getSourcePositionStack(node)

	return NewStaticCheckError(s, location)
}

func (checker *checker) getSourcePositionStack(node parse.Node) parse.SourcePositionStack {
	var sourcePositionStack parse.SourcePositionStack

	if checker.parentChecker != nil {
		var importStmt parse.Node
		if checker.inclusionImportStatement != nil {
			importStmt = checker.inclusionImportStatement
		} else if checker.moduleImportStatement != nil {
			importStmt = checker.moduleImportStatement
		}
		sourcePositionStack = checker.parentChecker.getSourcePositionStack(importStmt)
	}

	sourcePositionStack = append(sourcePositionStack, checker.chunk.GetSourcePosition(node.Base().Span))
	return sourcePositionStack
}

func (checker *checker) addError(node parse.Node, s string) {
	checker.data.errors = append(checker.data.errors, checker.makeCheckingError(node, s))
}

func (c *checker) defineStructs(closestModule parse.Node, statements []parse.Node) {

	//Define structs from included chunks.
	for _, stmt := range statements {
		inclusionStmt, ok := stmt.(*parse.InclusionImportStatement)
		if !ok {
			continue
		}
		includedChunk := c.currentModule.InclusionStatementMap[inclusionStmt]
		c.defineStructs(closestModule, includedChunk.Node.Statements)
	}

	//Define other structs.
	for _, stmt := range statements {
		structDef, ok := stmt.(*parse.StructDefinition)
		if !ok {
			continue
		}

		name, ok := structDef.GetName()
		if ok {
			defs := c.getModStructDefs(closestModule)
			_, alreadyDefined := defs[name]
			if alreadyDefined {
				c.addError(structDef.Name, fmtInvalidStructDefAlreadyDeclared(name))
			} else {
				defs[name] = 0
			}
		}

		if structDef.Body == nil {
			continue
		}

		//check for duplicate member definitions.
		names := make([]string, 0, len(structDef.Body.Definitions))

		for _, memberDefinition := range structDef.Body.Definitions {
			name := ""
			var nameNode parse.Node

			switch def := memberDefinition.(type) {
			case *parse.StructFieldDefinition:
				name = def.Name.Name
				nameNode = def.Name
			case *parse.FunctionDeclaration:
				name = def.Name.Name
				nameNode = def.Name
			default:
				continue
			}

			if slices.Contains(names, name) {
				c.addError(nameNode, fmtAnXFieldOrMethodIsAlreadyDefined(name))
			} else {
				names = append(names, name)
			}
		}
	}
}

func (checker *checker) check(node parse.Node) error {
	checkNode := func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		return checker.checkSingleNode(node, parent, scopeNode, ancestorChain, after), nil
	}
	postCheckNode := func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		return checker.postCheckSingleNode(node, parent, scopeNode, ancestorChain, after), nil
	}
	return parse.Walk(node, checkNode, postCheckNode)
}

func (checker *checker) getLocalVarsInScope(scopeNode parse.Node) map[string]localVarInfo {
	if !parse.IsScopeContainerNode(scopeNode) {
		panic(fmt.Errorf("a %T is not a scope container", scopeNode))
	}

	variables, ok := checker.localVars[scopeNode]
	if !ok {
		variables = make(map[string]localVarInfo)
		checker.localVars[scopeNode] = variables
	}
	return variables
}

func (checker *checker) varExists(name string, ancestorChain []parse.Node) bool {
	var closestModule parse.Node

	checkGlobalVar := false

loop:
	for i := len(ancestorChain) - 1; i >= 0; i-- {
		if !parse.IsScopeContainerNode(ancestorChain[i]) {
			continue
		}

		scopeNode := ancestorChain[i]

		if checkGlobalVar {
			switch scopeNode.(type) {
			case *parse.Chunk, *parse.EmbeddedModule:
				closestModule = scopeNode
				break loop
			}
		}

		vars, ok := checker.localVars[scopeNode]
		if ok {
			if _, ok := vars[name]; ok {
				return true
			}
		}

		checkGlobalVar = true

		switch scopeNode.(type) {
		case *parse.Chunk, *parse.EmbeddedModule:
			closestModule = scopeNode
			break loop
		}
	}

	globalVars := checker.getModGlobalVars(closestModule)
	_, ok := globalVars[name]
	return ok
}

func (checker *checker) doGlobalVarExist(name string, closestModule parse.Node) bool {
	globals := checker.getModGlobalVars(closestModule)
	_, ok := globals[name]
	return ok
}

func (checker *checker) setScopeLocalVars(scopeNode parse.Node, vars map[string]localVarInfo) {
	checker.localVars[scopeNode] = vars
}

func (checker *checker) getScopeLocalVarsCopy(scopeNode parse.Node) map[string]localVarInfo {
	variables := checker.getLocalVarsInScope(scopeNode)

	varsCopy := make(map[string]localVarInfo)
	for k, v := range variables {
		varsCopy[k] = v
	}
	return varsCopy
}

func (checker *checker) getModGlobalVars(module parse.Node) map[string]globalVarInfo {
	variables, ok := checker.globalVars[module]
	if !ok {
		variables = make(map[string]globalVarInfo)
		checker.globalVars[module] = variables
	}
	return variables
}

func (checker *checker) getModFunctionDecls(mod parse.Node) map[string]int {
	fns, ok := checker.fnDecls[mod]
	if !ok {
		fns = make(map[string]int)
		checker.fnDecls[mod] = fns
	}
	return fns
}

func (checker *checker) getModStructDefs(mod parse.Node) map[string]int {
	defs, ok := checker.structDefs[mod]
	if !ok {
		defs = make(map[string]int)
		checker.structDefs[mod] = defs
	}
	return defs
}

func (checker *checker) getModHostAliases(mod parse.Node) map[string]int {
	aliases, ok := checker.hostAliases[mod]
	if !ok {
		aliases = make(map[string]int)
		checker.hostAliases[mod] = aliases
	}
	return aliases
}

func (checker *checker) getModPatterns(mod parse.Node) map[string]int {
	patterns, ok := checker.patterns[mod]
	if !ok {
		patterns = make(map[string]int)
		checker.patterns[mod] = patterns
	}
	return patterns
}

func (checker *checker) getModPatternNamespaces(module parse.Node) map[string]int {
	namespaces, ok := checker.patternNamespaces[module]
	if !ok {
		namespaces = make(map[string]int)
		checker.patternNamespaces[module] = namespaces
	}
	return namespaces
}

func (checker *checker) getPropertyInfo(obj *parse.ObjectLiteral) *propertyInfo {
	propInfo, ok := checker.properties[obj]
	if !ok {
		propInfo = &propertyInfo{known: make(map[string]bool, 0)}
		checker.properties[obj] = propInfo
	}
	return propInfo
}

func findClosestModule(ancestorChain []parse.Node) parse.Node {
	var closestModule parse.Node

	for _, n := range ancestorChain {
		switch n.(type) {
		case *parse.Chunk, *parse.EmbeddedModule:
			closestModule = n
		}
	}

	return closestModule
}

func findClosest[T any](ancestorChain []parse.Node) T {
	var closest T

	for _, n := range ancestorChain {
		switch node := n.(type) {
		case T:
			closest = node
		}
	}

	return closest
}

func findClosestScopeContainerNode(ancestorChain []parse.Node) parse.Node {
	var closest parse.Node

	for _, n := range ancestorChain {
		if parse.IsScopeContainerNode(n) {
			closest = n
		}
	}

	return closest
}

// checkSingleNode perform checks on a single node.
func (c *checker) checkSingleNode(n, parent, scopeNode parse.Node, ancestorChain []parse.Node, _ bool) parse.TraversalAction {
	closestModule := findClosestModule(ancestorChain)
	closestAssertion := findClosest[*parse.AssertionStatement](ancestorChain)
	inPreinitBlock := findClosest[*parse.PreinitStatement](ancestorChain) != nil

	//check that the node is allowed in assertion

	if closestAssertion != nil {
		switch n := n.(type) {
		case
			//variables
			*parse.Variable, *parse.GlobalVariable, *parse.IdentifierLiteral,

			*parse.BinaryExpression, *parse.URLExpression, *parse.AtHostLiteral,
			parse.SimpleValueLiteral, *parse.IntegerRangeLiteral, *parse.FloatRangeLiteral,

			//data structure literals
			*parse.ObjectLiteral, *parse.ObjectProperty, *parse.ListLiteral, *parse.RecordLiteral,

			//member-like expressions
			*parse.MemberExpression, *parse.IdentifierMemberExpression, *parse.DoubleColonExpression,
			*parse.IndexExpression, *parse.SliceExpression,

			//patterns
			*parse.PatternIdentifierLiteral,
			*parse.ObjectPatternLiteral, *parse.ObjectPatternProperty, *parse.RecordPatternLiteral,
			*parse.ListPatternLiteral, *parse.TuplePatternLiteral,
			*parse.FunctionPatternExpression,
			*parse.PatternNamespaceIdentifierLiteral, *parse.PatternNamespaceMemberExpression,
			*parse.OptionPatternLiteral, *parse.OptionalPatternExpression,
			*parse.ComplexStringPatternPiece, *parse.PatternPieceElement, *parse.PatternGroupName,
			*parse.PatternUnion,
			*parse.PatternCallExpression:
		case *parse.CallExpression:
			allowed := false

			ident, ok := n.Callee.(*parse.IdentifierLiteral)
			if ok {
				switch ident.Name {
				case globalnames.LEN_FN:
					allowed = true
				}
			}

			if !allowed {
				c.addError(n, fmtFollowingNodeTypeNotAllowedInAssertions(n))
			}
		default:
			if !parse.NodeIsSimpleValueLiteral(n) {
				c.addError(n, fmtFollowingNodeTypeNotAllowedInAssertions(n))
			}
		}
	}

	//actually check the node

top_switch:
	switch node := n.(type) {
	case *parse.IntegerRangeLiteral:
		if upperBound, ok := node.UpperBound.(*parse.IntLiteral); ok && node.LowerBound.Value > upperBound.Value {
			c.addError(n, LOWER_BOUND_OF_INT_RANGE_LIT_SHOULD_BE_SMALLER_THAN_UPPER_BOUND)
		}
	case *parse.FloatRangeLiteral:
		if upperBound, ok := node.UpperBound.(*parse.FloatLiteral); ok && node.LowerBound.Value > upperBound.Value {
			c.addError(n, LOWER_BOUND_OF_FLOAT_RANGE_LIT_SHOULD_BE_SMALLER_THAN_UPPER_BOUND)
		}
	case *parse.QuantityLiteral:

		var prevMultiplier string
		var prevUnit string
		var prevDurationUnitValue time.Duration

		for partIndex := 0; partIndex < len(node.Values); partIndex++ {
			if node.Values[partIndex] < 0 {
				c.addError(n, ErrNegQuantityNotSupported.Error())
				return parse.ContinueTraversal
			}

			i := 0
			var multiplier string

			switch node.Units[partIndex][0] {
			case 'k', 'M', 'G', 'T':
				multiplier = node.Units[partIndex]
				i++
			default:
			}

			prevMultiplier = multiplier
			_ = prevMultiplier

			if i > 0 && len(node.Units[partIndex]) == 1 {
				c.addError(node, fmtNonSupportedUnit(node.Units[0]))
				return parse.ContinueTraversal
			}

			unit := node.Units[partIndex][i:]

			switch unit {
			case "x", LINE_COUNT_UNIT, RUNE_COUNT_UNIT, BYTE_COUNT_UNIT:
				if partIndex != 0 || prevUnit != "" {
					c.addError(node, INVALID_QUANTITY)
					return parse.ContinueTraversal
				}
				prevUnit = unit
			case "h", "mn", "s", "ms", "us", "ns":
				var durationUnitValue time.Duration

				switch unit {
				case "h":
					durationUnitValue = time.Hour
				case "mn":
					durationUnitValue = time.Minute
				case "s":
					durationUnitValue = time.Second
				case "ms":
					durationUnitValue = time.Millisecond
				case "us":
					durationUnitValue = time.Microsecond
				case "ns":
					durationUnitValue = time.Nanosecond
				}

				if prevUnit != "" && (prevDurationUnitValue == 0 || durationUnitValue >= prevDurationUnitValue) {
					c.addError(node, INVALID_QUANTITY)
					return parse.ContinueTraversal
				}

				prevDurationUnitValue = durationUnitValue
				prevUnit = unit
			case "%":
				if partIndex != 0 || prevUnit != "" {
					c.addError(node, INVALID_QUANTITY)
					return parse.ContinueTraversal
				}
				if i == 0 {
					prevUnit = unit
					break
				}
				fallthrough
			default:
				c.addError(node, fmtNonSupportedUnit(node.Units[0]))
				return parse.ContinueTraversal
			}
		}

		_, err := evalQuantity(node.Values, node.Units)
		if err != nil {
			c.addError(node, err.Error())
		}

	case *parse.RateLiteral:

		lastUnit1 := node.Units[len(node.Units)-1]
		rateUnit := node.DivUnit

		switch rateUnit {
		case "s":
			i := 0
			switch lastUnit1[0] {
			case 'k', 'M', 'G', 'T':
				i++
			default:
			}
			switch lastUnit1[i:] {
			case "x", BYTE_COUNT_UNIT:
				return parse.ContinueTraversal
			}
		}
		c.addError(node, INVALID_RATE)
		return parse.ContinueTraversal
	case *parse.URLLiteral:
		if strings.HasPrefix(node.Value, "mem://") && utils.Must(url.Parse(node.Value)).Host != MEM_HOSTNAME {
			c.addError(node, INVALID_MEM_HOST_ONLY_VALID_VALUE)
		}
	case *parse.HostLiteral:
		if strings.HasPrefix(node.Value, "mem://") && utils.Must(url.Parse(node.Value)).Host != MEM_HOSTNAME {
			c.addError(node, INVALID_MEM_HOST_ONLY_VALID_VALUE)
		}
	case *parse.ObjectLiteral:
		action, keys := shallowCheckObjectRecordProperties(node.Properties, node.SpreadElements, true, func(n parse.Node, msg string) {
			c.addError(n, msg)
		})

		if action != parse.ContinueTraversal {
			return action
		}

		propInfo := c.getPropertyInfo(node)
		for k := range keys {
			propInfo.known[k] = true
		}

		for _, metaprop := range node.MetaProperties {
			switch metaprop.Name() {
			case VISIBILITY_KEY:
				checkVisibilityInitializationBlock(propInfo, metaprop.Initialization, func(n parse.Node, msg string) {
					c.addError(n, msg)
				})
			}
		}
	case *parse.RecordLiteral:
		action, _ := shallowCheckObjectRecordProperties(node.Properties, node.SpreadElements, false, func(n parse.Node, msg string) {
			c.addError(n, msg)
		})

		if action != parse.ContinueTraversal {
			return action
		}
	case *parse.ObjectPatternLiteral, *parse.RecordPatternLiteral:
		indexKey := 0
		keys := map[string]struct{}{}

		var propertyNodes []*parse.ObjectPatternProperty
		var spreadElementsNodes []*parse.PatternPropertySpreadElement
		var otherPropsNodes []*parse.OtherPropsExpr
		var isExact bool

		switch node := node.(type) {
		case *parse.ObjectPatternLiteral:
			propertyNodes = node.Properties
			spreadElementsNodes = node.SpreadElements
			otherPropsNodes = node.OtherProperties
			isExact = node.Exact()
		case *parse.RecordPatternLiteral:
			propertyNodes = node.Properties
			spreadElementsNodes = node.SpreadElements
			otherPropsNodes = node.OtherProperties
			isExact = node.Exact()
		}

		// look for duplicate keys
		for _, prop := range propertyNodes {
			var k string

			switch n := prop.Key.(type) {
			case *parse.QuotedStringLiteral:
				k = n.Value
			case *parse.IdentifierLiteral:
				k = n.Name
			case nil:
				k = strconv.Itoa(indexKey)
				indexKey++
			}

			if len(k) > MAX_NAME_BYTE_LEN {
				c.addError(prop.Key, fmtNameIsTooLong(k))
			}

			if parse.IsMetadataKey(k) {
				c.addError(prop.Key, OBJ_REC_LIT_CANNOT_HAVE_METAPROP_KEYS)
			} else if _, found := keys[k]; found {
				c.addError(prop, fmtDuplicateKey(k))
			}

			keys[k] = struct{}{}
		}

		// also look for duplicate keys
		for _, element := range spreadElementsNodes {
			extractionExpr, ok := element.Expr.(*parse.ExtractionExpression)
			if !ok {
				continue
			}

			for _, key := range extractionExpr.Keys.Keys {
				name := key.(*parse.IdentifierLiteral).Name

				_, found := keys[name]
				if found {
					c.addError(key, fmtDuplicateKey(name))
					return parse.ContinueTraversal
				}
				keys[name] = struct{}{}
			}
		}

		//check that if the pattern is exact there are no other otherprops nodes other than otherprops(no)
		if isExact {
			for _, prop := range otherPropsNodes {
				patternIdent, ok := prop.Pattern.(*parse.PatternIdentifierLiteral)

				if !ok || patternIdent.Name != parse.NO_OTHERPROPS_PATTERN_NAME {
					c.addError(prop, UNEXPECTED_OTHER_PROPS_EXPR_OTHERPROPS_NO_IS_PRESENT)
				}
			}
		}

		return parse.ContinueTraversal
	case *parse.DictionaryLiteral:
		keys := map[string]bool{}

		// look for duplicate keys
		for _, entry := range node.Entries {

			keyNode, ok := entry.Key.(parse.SimpleValueLiteral)
			if !ok {
				//there is a parsing error
				continue
			}

			keyRepr := keyNode.ValueString()

			if keys[keyRepr] {
				c.addError(entry.Key, fmtDuplicateDictKey(keyRepr))
			} else {
				keys[keyRepr] = true
			}
		}

	case *parse.SpawnExpression:

		var globals = make(map[string]globalVarInfo)
		var globalDescNode parse.Node

		//add constant globals
		parentModuleGlobals := c.getModGlobalVars(closestModule)
		for name, info := range parentModuleGlobals {
			if info.isStartConstant {
				globals[name] = info
			}
		}

		// add globals passed by user
		if obj, ok := node.Meta.(*parse.ObjectLiteral); ok {
			if len(obj.SpreadElements) > 0 {
				c.addError(node.Meta, INVALID_SPAWN_ONLY_OBJECT_LITERALS_WITH_NO_SPREAD_ELEMENTS_SUPPORTED)
			}

			for _, prop := range obj.Properties {
				if prop.HasImplicitKey() {
					c.addError(node.Meta, INVALID_SPAWN_ONLY_OBJECT_LITERALS_WITH_NO_SPREAD_ELEMENTS_SUPPORTED)
				}
			}

			val, ok := obj.PropValue(symbolic.LTHREAD_META_GLOBALS_SECTION)
			if ok {
				globalDescNode = val
			}
		} else if node.Meta != nil {
			c.addError(node.Meta, INVALID_SPAWN_ONLY_OBJECT_LITERALS_WITH_NO_SPREAD_ELEMENTS_SUPPORTED)
		}

		switch desc := globalDescNode.(type) {
		case *parse.KeyListExpression:
			for _, ident := range desc.Keys {
				globVarName := ident.(*parse.IdentifierLiteral).Name
				if !c.doGlobalVarExist(globVarName, closestModule) {
					c.addError(globalDescNode, fmtCannotPassGlobalThatIsNotDeclaredToLThread(globVarName))
				}
				globals[globVarName] = globalVarInfo{isConst: true}
			}
		case *parse.ObjectLiteral:
			if len(desc.SpreadElements) > 0 {
				c.addError(desc, INVALID_SPAWN_GLOBALS_SHOULD_BE)
			}

			for _, prop := range desc.Properties {
				if prop.HasImplicitKey() {
					c.addError(desc, INVALID_SPAWN_GLOBALS_SHOULD_BE)
					continue
				}
				globals[prop.Name()] = globalVarInfo{isConst: true}
			}
		case *parse.NilLiteral:
		case nil:
		default:
			c.addError(node, INVALID_SPAWN_GLOBALS_SHOULD_BE)
		}

		if node.Module != nil && node.Module.SingleCallExpr {
			calleeNode := node.Module.Statements[0].(*parse.CallExpression).Callee

			switch calleeNode := calleeNode.(type) {
			case *parse.IdentifierLiteral:
				globals[calleeNode.Name] = globalVarInfo{isConst: true}
			case *parse.IdentifierMemberExpression:
				globals[calleeNode.Left.Name] = globalVarInfo{isConst: true}
			}
		}

		embeddedModuleGlobals := c.getModGlobalVars(node.Module)

		for name, info := range globals {
			embeddedModuleGlobals[name] = info
		}

		c.defineStructs(node.Module, node.Module.Statements)
	case *parse.LifetimejobExpression:
		lifetimeJobGlobals := c.getModGlobalVars(node.Module)

		for name, info := range c.getModGlobalVars(closestModule) {
			lifetimeJobGlobals[name] = info
		}

		lifetimeJobPatterns := c.getModPatterns(node.Module)

		for name, info := range c.getModPatterns(closestModule) {
			lifetimeJobPatterns[name] = info
		}

		lifetimeJobPatternNamespaces := c.getModPatternNamespaces(node.Module)

		for name, info := range c.getModPatternNamespaces(closestModule) {
			lifetimeJobPatternNamespaces[name] = info
		}

		if node.Subject != nil {
			return parse.ContinueTraversal
		}

		if prop, ok := parent.(*parse.ObjectProperty); !ok || !prop.HasImplicitKey() {
			c.addError(node, MISSING_LIFETIMEJOB_SUBJECT_PATTERN_NOT_AN_IMPLICIT_OBJ_PROP)
		}
	case *parse.ReceptionHandlerExpression:
		if prop, ok := parent.(*parse.ObjectProperty); !ok || !prop.HasImplicitKey() {
			c.addError(node, MISPLACED_RECEPTION_HANDLER_EXPRESSION)
		}

	case *parse.MappingExpression:

	case *parse.StaticMappingEntry:
		switch node.Key.(type) {
		case *parse.PatternIdentifierLiteral, *parse.PatternNamespaceMemberExpression:
		default:
			if !parse.NodeIsSimpleValueLiteral(node.Key) {
				c.addError(node.Key, INVALID_MAPPING_ENTRY_KEY_ONLY_SIMPL_LITS_AND_PATT_IDENTS)
			}
		}

	case *parse.DynamicMappingEntry:
		switch node.Key.(type) {
		case *parse.PatternIdentifierLiteral, *parse.PatternNamespaceMemberExpression:
		default:
			if !parse.NodeIsSimpleValueLiteral(node.Key) {
				c.addError(node.Key, INVALID_MAPPING_ENTRY_KEY_ONLY_SIMPL_LITS_AND_PATT_IDENTS)
			}
		}

		localVars := c.getLocalVarsInScope(node)
		varname := node.KeyVar.(*parse.IdentifierLiteral).Name
		localVars[varname] = localVarInfo{}

		if node.GroupMatchingVariable != nil {
			varname := node.GroupMatchingVariable.(*parse.IdentifierLiteral).Name
			localVars[varname] = localVarInfo{}
		}

	case *parse.ComputeExpression:

		if _, ok := scopeNode.(*parse.DynamicMappingEntry); !ok {
			c.addError(node, MISPLACED_COMPUTE_EXPR_SHOULD_BE_IN_DYNAMIC_MAPPING_EXPR_ENTRY)
		} else {
		ancestor_loop:
			for i := len(ancestorChain) - 1; i >= 0; i-- {
				ancestor := ancestorChain[i]
				if ancestor == scopeNode {
					break
				}

				switch a := ancestor.(type) {
				case *parse.StaticMappingEntry:
					c.addError(node, MISPLACED_COMPUTE_EXPR_SHOULD_BE_IN_DYNAMIC_MAPPING_EXPR_ENTRY)
					break ancestor_loop
				case *parse.DynamicMappingEntry:
					if a.Key == node || i < len(ancestorChain)-1 && ancestorChain[i+1] == a.Key {
						c.addError(node, MISPLACED_COMPUTE_EXPR_SHOULD_BE_IN_DYNAMIC_MAPPING_EXPR_ENTRY)
					}
					break ancestor_loop
				}
			}
		}

	case *parse.InclusionImportStatement:
		//if the import is performed by the preinit block, prune the traversal.
		if _, ok := parent.(*parse.Block); ok && inPreinitBlock {
			return parse.Prune
		}

		if _, ok := parent.(*parse.Chunk); !ok {
			c.addError(node, MISPLACED_INCLUSION_IMPORT_STATEMENT_TOP_LEVEL_STMT)
			return parse.ContinueTraversal
		}
		includedChunk := c.currentModule.InclusionStatementMap[node]

		globals := make(map[parse.Node]map[string]globalVarInfo)
		globals[includedChunk.Node] = map[string]globalVarInfo{}

		//add globals to child checker
		c.checkInput.Globals.Foreach(func(name string, v Value, isStartConstant bool) error {
			globals[includedChunk.Node][name] = globalVarInfo{isConst: isStartConstant}
			return nil
		})

		//add defined patterns & pattern namespaces to child checker
		patterns := make(map[parse.Node]map[string]int)
		patterns[includedChunk.Node] = map[string]int{}
		for k := range c.checkInput.Patterns {
			patterns[includedChunk.Node][k] = 0
		}

		patternNamespaces := make(map[parse.Node]map[string]int)
		patternNamespaces[includedChunk.Node] = map[string]int{}
		for k := range c.checkInput.PatternNamespaces {
			patternNamespaces[includedChunk.Node][k] = 0
		}

		chunkChecker := &checker{
			parentChecker:            c,
			checkInput:               c.checkInput,
			fnDecls:                  make(map[parse.Node]map[string]int),
			structDefs:               make(map[parse.Node]map[string]int),
			globalVars:               globals,
			localVars:                make(map[parse.Node]map[string]localVarInfo),
			properties:               make(map[*parse.ObjectLiteral]*propertyInfo),
			patterns:                 patterns,
			patternNamespaces:        patternNamespaces,
			currentModule:            c.currentModule,
			chunk:                    includedChunk.ParsedChunk,
			inclusionImportStatement: node,
			store:                    make(map[parse.Node]any),
			data: &StaticCheckData{
				fnData:      map[*parse.FunctionExpression]*FunctionStaticData{},
				mappingData: map[*parse.MappingExpression]*MappingStaticData{},
			},
		}

		err := chunkChecker.check(includedChunk.Node)
		if err != nil {
			panic(err)
		}
		if len(chunkChecker.data.errors) != 0 {
			c.data.errors = append(c.data.errors, chunkChecker.data.errors...)
		}

		for k, v := range chunkChecker.data.fnData {
			c.data.fnData[k] = v
		}

		for k, v := range chunkChecker.data.mappingData {
			c.data.mappingData[k] = v
		}

		//include all global data & top level local variables
		for k, v := range chunkChecker.fnDecls[includedChunk.Node] {
			if c.checkInput.Globals.Has(k) {
				continue
			}

			fnDecls := c.getModFunctionDecls(closestModule)
			if _, ok := fnDecls[k]; ok {
				// handled in next loop
			} else {
				fnDecls[k] = v
			}
		}

		for k, v := range chunkChecker.globalVars[includedChunk.Node] {
			if c.checkInput.Globals.Has(k) {
				continue
			}

			globalVars := c.getModGlobalVars(closestModule)
			if _, ok := globalVars[k]; ok {
				c.addError(node, fmtCannotShadowGlobalVariable(k))
			} else {
				globalVars[k] = v
			}
		}

		for k, v := range chunkChecker.localVars[includedChunk.Node] {
			localVars := c.getLocalVarsInScope(closestModule)
			if _, ok := localVars[k]; ok {
				c.addError(node, fmtCannotShadowLocalVariable(k))
			} else {
				localVars[k] = v
			}
		}

		for k, v := range chunkChecker.patterns[includedChunk.Node] {
			if _, ok := c.checkInput.Patterns[k]; ok {
				continue
			}

			patterns := c.getModPatterns(closestModule)
			if _, ok := patterns[k]; ok {
				c.addError(node, fmtPatternAlreadyDeclared(k))
			} else {
				patterns[k] = v
			}
		}

		for k, v := range chunkChecker.patternNamespaces[includedChunk.Node] {
			if _, ok := c.checkInput.PatternNamespaces[k]; ok {
				continue
			}

			namespaces := c.getModPatternNamespaces(closestModule)
			if _, ok := namespaces[k]; ok {
				c.addError(node, fmtPatternNamespaceAlreadyDeclared(k))
			} else {
				namespaces[k] = v
			}
		}

		if v, ok := chunkChecker.store[includedChunk.Node]; ok {
			panic(fmt.Errorf("data stored for included chunk %#v : %#v", includedChunk.Node, v))
		}

	//ok
	case *parse.ImportStatement:
		if c.inclusionImportStatement != nil {
			c.addError(node, MODULE_IMPORTS_NOT_ALLOWED_IN_INCLUDED_CHUNK)
			return parse.Prune
		}

		if _, ok := parent.(*parse.Chunk); !ok {
			c.addError(node, MISPLACED_MOD_IMPORT_STATEMENT_TOP_LEVEL_STMT)
			return parse.Prune
		}

		name := node.Identifier.Name
		variables := c.getModGlobalVars(closestModule)

		_, alreadyUsed := variables[name]
		if alreadyUsed {
			c.addError(node, fmtInvalidImportStmtAlreadyDeclaredGlobal(name))
			return parse.ContinueTraversal
		}
		variables[name] = globalVarInfo{isConst: true}

		if c.inclusionImportStatement != nil || node.Source == nil {
			return parse.ContinueTraversal
		}

		var importedModuleSource WrappedString

		switch node.Source.(type) {
		case *parse.URLLiteral, *parse.AbsolutePathLiteral, *parse.RelativePathLiteral:
			value, err := evalSimpleValueLiteral(node.Source.(parse.SimpleValueLiteral), nil)
			if err != nil {
				panic(ErrUnreachable)
			}
			src, err := getSourceFromImportSource(value, c.currentModule, c.checkInput.State.Ctx)
			if err != nil {
				c.addError(node, fmt.Sprintf("failed to resolve location of imported module: %s", err.Error()))
				return parse.ContinueTraversal
			}
			importedModuleSource = src
		default:
			return parse.ContinueTraversal
		}

		importedModule := c.currentModule.DirectlyImportedModules[importedModuleSource.UnderlyingString()]
		importModuleNode := importedModule.MainChunk.Node

		globals := make(map[parse.Node]map[string]globalVarInfo)
		globals[importModuleNode] = map[string]globalVarInfo{}

		//add base globals to child checker
		for globalName := range c.checkInput.State.SymbolicBaseGlobalsForImportedModule {
			globals[importModuleNode][globalName] = globalVarInfo{isConst: true, isStartConstant: true}
		}

		//add module arguments variable to child checker
		globals[importModuleNode][MOD_ARGS_VARNAME] = globalVarInfo{isConst: true, isStartConstant: true}

		//add base patterns & pattern namespaces to child checker
		basePatterns, basePatternNamespaces := c.checkInput.State.GetBasePatternsForImportedModule()

		patterns := make(map[parse.Node]map[string]int)
		patterns[importModuleNode] = map[string]int{}
		for k := range basePatterns {
			patterns[importModuleNode][k] = 0
		}

		patternNamespaces := make(map[parse.Node]map[string]int)
		patternNamespaces[importModuleNode] = map[string]int{}
		for k := range basePatternNamespaces {
			patternNamespaces[importModuleNode][k] = 0
		}

		chunkChecker := &checker{
			parentChecker:         c,
			checkInput:            c.checkInput,
			fnDecls:               make(map[parse.Node]map[string]int),
			structDefs:            make(map[parse.Node]map[string]int),
			globalVars:            globals,
			localVars:             make(map[parse.Node]map[string]localVarInfo),
			properties:            make(map[*parse.ObjectLiteral]*propertyInfo),
			patterns:              patterns,
			patternNamespaces:     patternNamespaces,
			currentModule:         importedModule,
			chunk:                 importedModule.MainChunk,
			moduleImportStatement: node,
			store:                 make(map[parse.Node]any),
			data: &StaticCheckData{
				fnData:      map[*parse.FunctionExpression]*FunctionStaticData{},
				mappingData: map[*parse.MappingExpression]*MappingStaticData{},
			},
		}

		err := chunkChecker.check(importModuleNode)
		if err != nil {
			panic(err)
		}

		if len(chunkChecker.data.errors) != 0 {
			c.data.errors = append(c.data.errors, chunkChecker.data.errors...)
		}

		if v, ok := chunkChecker.store[importModuleNode]; ok {
			panic(fmt.Errorf("data stored for included chunk %#v : %#v", importModuleNode, v))
		}

	case *parse.GlobalConstantDeclarations:
		globalVars := c.getModGlobalVars(closestModule)

		for _, decl := range node.Declarations {
			ident, ok := decl.Left.(*parse.IdentifierLiteral)
			if !ok {
				continue
			}
			name := ident.Name

			_, alreadyUsed := globalVars[name]
			if alreadyUsed {
				c.addError(decl, fmtInvalidConstDeclGlobalAlreadyDeclared(name))
				return parse.ContinueTraversal
			}
			globalVars[name] = globalVarInfo{isConst: true}
		}
	case *parse.LocalVariableDeclarations:
		localVars := c.getLocalVarsInScope(scopeNode)

		for _, decl := range node.Declarations {
			name := decl.Left.(*parse.IdentifierLiteral).Name

			globalVariables := c.getModGlobalVars(closestModule)

			if _, alreadyDefined := globalVariables[name]; alreadyDefined {
				c.addError(decl, fmtCannotShadowGlobalVariable(name))
				return parse.ContinueTraversal
			}

			_, alreadyUsed := localVars[name]
			if alreadyUsed {
				c.addError(decl, fmtInvalidLocalVarDeclAlreadyDeclared(name))
				return parse.ContinueTraversal
			}
			localVars[name] = localVarInfo{}
		}
	case *parse.GlobalVariableDeclarations:
		globalVars := c.getModGlobalVars(closestModule)

		for _, decl := range node.Declarations {
			name := decl.Left.(*parse.IdentifierLiteral).Name

			localVariables := c.getLocalVarsInScope(scopeNode)

			if _, alreadyDefined := localVariables[name]; alreadyDefined {
				c.addError(decl, fmtCannotShadowLocalVariable(name))
				return parse.ContinueTraversal
			}

			_, alreadyUsed := globalVars[name]
			if alreadyUsed {
				c.addError(decl, fmtInvalidGlobalVarDeclAlreadyDeclared(name))
				return parse.ContinueTraversal
			}
			globalVars[name] = globalVarInfo{}
		}
	case *parse.Assignment, *parse.MultiAssignment:
		var names []string

		if assignment, ok := n.(*parse.Assignment); ok {

			switch left := assignment.Left.(type) {

			case *parse.GlobalVariable:
				fns, ok := c.fnDecls[closestModule]
				if ok {
					_, alreadyUsed := fns[left.Name]
					if alreadyUsed {
						c.addError(node, fmtInvalidGlobalVarAssignmentNameIsFuncName(left.Name))
						return parse.ContinueTraversal
					}
				}

				localVars := c.getLocalVarsInScope(scopeNode)

				if _, alreadyDefined := localVars[left.Name]; alreadyDefined {
					c.addError(node, fmtCannotShadowLocalVariable(left.Name))
					return parse.ContinueTraversal
				}

				variables := c.getModGlobalVars(closestModule)

				varInfo, alreadyDefined := variables[left.Name]
				if alreadyDefined {
					if varInfo.isConst {
						c.addError(node, fmtInvalidGlobalVarAssignmentNameIsConstant(left.Name))
						return parse.ContinueTraversal
					}
				} else {
					if assignment.Operator != parse.Assign {
						c.addError(node, fmtInvalidGlobalVarAssignmentVarDoesNotExist(left.Name))
					}
					variables[left.Name] = globalVarInfo{isConst: false}
				}

			case *parse.Variable:
				if left.Name == "" { //$
					c.addError(node, INVALID_ASSIGNMENT_ANONYMOUS_VAR_CANNOT_BE_ASSIGNED)
					return parse.ContinueTraversal
				}

				globalVariables := c.getModGlobalVars(closestModule)

				if _, alreadyDefined := globalVariables[left.Name]; alreadyDefined {
					c.addError(node, fmtCannotShadowGlobalVariable(left.Name))
					return parse.ContinueTraversal
				}

				localVars := c.getLocalVarsInScope(scopeNode)

				if _, alreadyDefined := localVars[left.Name]; !alreadyDefined && assignment.Operator != parse.Assign {
					c.addError(node, fmtInvalidVariableAssignmentVarDoesNotExist(left.Name))
				}

				names = append(names, left.Name)
			case *parse.IdentifierLiteral:
				globalVariables := c.getModGlobalVars(closestModule)

				if _, alreadyDefined := globalVariables[left.Name]; alreadyDefined {
					c.addError(node, fmtCannotShadowGlobalVariable(left.Name))
					return parse.ContinueTraversal
				}

				localVars := c.getLocalVarsInScope(scopeNode)

				if _, alreadyDefined := localVars[left.Name]; !alreadyDefined && assignment.Operator != parse.Assign {
					c.addError(node, fmtInvalidVariableAssignmentVarDoesNotExist(left.Name))
				}

				names = append(names, left.Name)
			case *parse.IdentifierMemberExpression:

				for _, ident := range left.PropertyNames {
					if parse.IsMetadataKey(ident.Name) {
						c.addError(node, fmtInvalidMemberAssignmentCannotModifyMetaProperty(ident.Name))
					}
				}
			case *parse.MemberExpression:
				curr := left
				var ok bool
				for {
					if parse.IsMetadataKey(curr.PropertyName.Name) {
						c.addError(node, fmtInvalidMemberAssignmentCannotModifyMetaProperty(curr.PropertyName.Name))
						break
					}
					if curr, ok = curr.Left.(*parse.MemberExpression); !ok {
						break
					}
				}
			case *parse.SliceExpression:
				if assignment.Operator != parse.Assign {
					c.addError(node, INVALID_ASSIGNMENT_EQUAL_ONLY_SUPPORTED_ASSIGNMENT_OPERATOR_FOR_SLICE_EXPRS)
				}
			}
		} else {
			assignment := n.(*parse.MultiAssignment)

			for _, variable := range assignment.Variables {
				name := variable.(*parse.IdentifierLiteral).Name

				globalVariables := c.getModGlobalVars(closestModule)

				if _, alreadyDefined := globalVariables[name]; alreadyDefined {
					c.addError(node, fmtCannotShadowGlobalVariable(name))
				}

				names = append(names, name)
			}
		}

		for _, name := range names {
			variables := c.getLocalVarsInScope(scopeNode)
			variables[name] = localVarInfo{}
		}

	case *parse.ForStatement:
		variablesBeforeStmt := c.getScopeLocalVarsCopy(scopeNode)
		variables := c.getLocalVarsInScope(scopeNode)

		c.store[node] = variablesBeforeStmt

		if node.KeyIndexIdent != nil {
			if _, alreadyDefined := variables[node.KeyIndexIdent.Name]; alreadyDefined &&
				!c.shellLocalVars[node.KeyIndexIdent.Name] {
				c.addError(node, fmtCannotShadowVariable(node.KeyIndexIdent.Name))
				return parse.ContinueTraversal
			}
			variables[node.KeyIndexIdent.Name] = localVarInfo{}
		}

		if node.ValueElemIdent != nil {
			if _, alreadyDefined := variables[node.ValueElemIdent.Name]; alreadyDefined &&
				!c.shellLocalVars[node.ValueElemIdent.Name] {
				c.addError(node, fmtCannotShadowVariable(node.ValueElemIdent.Name))
				return parse.ContinueTraversal
			}
			variables[node.ValueElemIdent.Name] = localVarInfo{}
		}

	case *parse.WalkStatement:
		variablesBeforeStmt := c.getScopeLocalVarsCopy(scopeNode)
		variables := c.getLocalVarsInScope(scopeNode)

		c.store[node] = variablesBeforeStmt

		if node.EntryIdent != nil {
			if _, alreadyDefined := variables[node.EntryIdent.Name]; alreadyDefined &&
				!c.shellLocalVars[node.EntryIdent.Name] {
				c.addError(node, fmtCannotShadowVariable(node.EntryIdent.Name))
				return parse.ContinueTraversal
			}
			variables[node.EntryIdent.Name] = localVarInfo{}
		}

	case *parse.ReadonlyPatternExpression:
		ok := false
		switch p := parent.(type) {
		case *parse.FunctionParameter:
			ok = p.Type == n
		default:
		}

		if !ok {
			c.addError(node, MISPLACED_READONLY_PATTERN_EXPRESSION)
		}
	case *parse.FunctionDeclaration:

		switch parent.(type) {
		case *parse.Chunk, *parse.EmbeddedModule:
			fns := c.getModFunctionDecls(closestModule)
			globVars := c.getModGlobalVars(closestModule)

			_, alreadyDeclared := fns[node.Name.Name]
			if alreadyDeclared {
				c.addError(node, fmtInvalidFnDeclAlreadyDeclared(node.Name.Name))
				return parse.ContinueTraversal
			}

			_, alreadyUsed := globVars[node.Name.Name]
			if alreadyUsed {
				c.addError(node, fmtInvalidFnDeclGlobVarExist(node.Name.Name))
				return parse.ContinueTraversal
			}

			fns[node.Name.Name] = 0
			globVars[node.Name.Name] = globalVarInfo{isConst: true, fnExpr: node.Function}
		case *parse.StructBody:
			//struct method
		default:
			c.addError(node, INVALID_FN_DECL_SHOULD_BE_TOP_LEVEL_STMT)
			return parse.ContinueTraversal
		}
	case *parse.FunctionExpression:
		fnLocalVars := c.getLocalVarsInScope(node)

		//we check that the captured variable exists & is a local
		for _, e := range node.CaptureList {
			name := e.(*parse.IdentifierLiteral).Name

			if !c.varExists(name, ancestorChain) {
				c.addError(node, fmtVarIsNotDeclared(name))
			} else if c.doGlobalVarExist(name, closestModule) {
				c.addError(node, fmtCannotPassGlobalToFunction(name))
			}

			fnLocalVars[name] = localVarInfo{}
		}

		for _, p := range node.Parameters {
			name := p.Var.Name

			globalVariables := c.getModGlobalVars(closestModule)

			if _, alreadyDefined := globalVariables[name]; alreadyDefined {
				c.addError(p, fmtParameterCannotShadowGlobalVariable(name))
				return parse.ContinueTraversal
			}

			fnLocalVars[name] = localVarInfo{}
		}
	case *parse.FunctionPatternExpression:
		fnLocalVars := c.getLocalVarsInScope(node)

		for _, p := range node.Parameters {
			if p.Var == nil {
				continue
			}

			name := p.Var.Name

			globalVariables := c.getModGlobalVars(closestModule)

			if _, alreadyDefined := globalVariables[name]; alreadyDefined {
				c.addError(p, fmtParameterCannotShadowGlobalVariable(name))
				return parse.ContinueTraversal
			}

			fnLocalVars[name] = localVarInfo{}
		}

	case *parse.YieldStatement:
		ok := c.checkInput.Module != nil && c.checkInput.Module.IsEmbedded()

		for i := len(ancestorChain) - 1; i >= 0; i-- {
			if !parse.IsScopeContainerNode(ancestorChain[i]) {
				continue
			}

			if ok && ancestorChain[i] != c.checkInput.Node {
				ok = false
				break
			}

			switch ancestorChain[i].(type) {
			case *parse.EmbeddedModule:
				ok = true
			}
			break
		}

		if !ok {
			c.addError(node, MISPLACE_YIELD_STATEMENT_ONLY_ALLOWED_IN_EMBEDDED_MODULES)
		}
	case *parse.BreakStatement, *parse.ContinueStatement:
		iterativeStmtIndex := -1

		//we search for the last iterative statement in the ancestor chain
	loop0:
		for i := len(ancestorChain) - 1; i >= 0; i-- {
			switch ancestorChain[i].(type) {
			case *parse.ForStatement, *parse.WalkStatement:
				iterativeStmtIndex = i
				break loop0
			}
		}

		if iterativeStmtIndex < 0 {
			c.addError(node, INVALID_BREAK_OR_CONTINUE_STMT_SHOULD_BE_IN_A_FOR_OR_WALK_STMT)
			return parse.ContinueTraversal
		}

		for i := iterativeStmtIndex + 1; i < len(ancestorChain); i++ {
			switch ancestorChain[i].(type) {
			case *parse.IfStatement, *parse.SwitchStatement, *parse.SwitchCase,
				*parse.MatchCase, *parse.MatchStatement, *parse.Block:
			default:
				c.addError(node, INVALID_BREAK_OR_CONTINUE_STMT_SHOULD_BE_IN_A_FOR_OR_WALK_STMT)
				return parse.ContinueTraversal
			}
		}
	case *parse.PruneStatement:
		walkStmtIndex := -1
		//we search for the last walk statement in the ancestor chain
	loop1:
		for i := len(ancestorChain) - 1; i >= 0; i-- {
			switch ancestorChain[i].(type) {
			case *parse.WalkStatement:
				walkStmtIndex = i
				break loop1
			}
		}

		if walkStmtIndex < 0 {
			c.addError(node, INVALID_PRUNE_STMT_SHOULD_BE_IN_WALK_STMT)
			return parse.ContinueTraversal
		}

		for i := walkStmtIndex + 1; i < len(ancestorChain); i++ {
			switch ancestorChain[i].(type) {
			case *parse.IfStatement, *parse.SwitchStatement, *parse.MatchStatement, *parse.Block, *parse.ForStatement:
			default:
				c.addError(node, INVALID_PRUNE_STMT_SHOULD_BE_IN_WALK_STMT)
				return parse.ContinueTraversal
			}
		}
	case *parse.MatchStatement:
		variablesBeforeStmt := c.getScopeLocalVarsCopy(scopeNode)
		c.store[node] = variablesBeforeStmt
	case *parse.MatchCase:
		//define the variables named after groups if the literal is used as a case in a match statement

		if node.GroupMatchingVariable == nil {
			break
		}

		variable := node.GroupMatchingVariable.(*parse.IdentifierLiteral)

		if _, alreadyDefined := c.getModGlobalVars(closestModule)[variable.Name]; alreadyDefined {
			c.addError(variable, fmtCannotShadowGlobalVariable(variable.Name))
			return parse.ContinueTraversal
		}

		localVars := c.getLocalVarsInScope(scopeNode)

		if info, alreadyDefined := localVars[variable.Name]; alreadyDefined && info != (localVarInfo{isGroupMatchingVar: true}) {
			c.addError(variable, fmtCannotShadowLocalVariable(variable.Name))
			return parse.ContinueTraversal
		}

		localVars[variable.Name] = localVarInfo{isGroupMatchingVar: true}
	case *parse.Variable:
		if len(node.Name) > MAX_NAME_BYTE_LEN {
			c.addError(node, fmtNameIsTooLong(node.Name))
			return parse.ContinueTraversal
		}

		if node.Name == "" {
			break
		}

		if _, isLazyExpr := scopeNode.(*parse.LazyExpression); isLazyExpr {
			break
		}

		if _, ok := scopeNode.(*parse.ExtendStatement); ok {
			c.addError(node, VARS_NOT_ALLOWED_IN_PATTERN_AND_EXTENSION_OBJECT_PROPERTIES)
			return parse.ContinueTraversal
		}

		if _, ok := scopeNode.(*parse.StructDefinition); ok {
			c.addError(node, VARS_CANNOT_BE_USED_IN_STRUCT_FIELD_DEFS)
			return parse.ContinueTraversal
		}

		variables := c.getLocalVarsInScope(scopeNode)
		_, exist := variables[node.Name]

		if !exist {
			c.addError(node, fmtLocalVarIsNotDeclared(node.Name))
			return parse.ContinueTraversal
		}

	case *parse.GlobalVariable:
		if len(node.Name) > MAX_NAME_BYTE_LEN {
			c.addError(node, fmtNameIsTooLong(node.Name))
			return parse.ContinueTraversal
		}

		if _, isAssignment := parent.(*parse.Assignment); isAssignment {
			if fnExpr, ok := scopeNode.(*parse.FunctionExpression); ok {
				c.data.addFnAssigningGlobal(fnExpr)
			}
			break
		}

		if _, isLazyExpr := scopeNode.(*parse.LazyExpression); isLazyExpr {
			break
		}
		globalVars := c.getModGlobalVars(closestModule)
		globalVarInfo, exist := globalVars[node.Name]

		if _, ok := scopeNode.(*parse.ExtendStatement); ok {
			c.addError(node, VARS_NOT_ALLOWED_IN_PATTERN_AND_EXTENSION_OBJECT_PROPERTIES)
			return parse.ContinueTraversal
		}

		if _, ok := scopeNode.(*parse.StructDefinition); ok {
			c.addError(node, VARS_CANNOT_BE_USED_IN_STRUCT_FIELD_DEFS)
			return parse.ContinueTraversal
		}

		if !exist {
			c.addError(node, fmtGlobalVarIsNotDeclared(node.Name))
			return parse.ContinueTraversal
		}

		switch scope := scopeNode.(type) {
		case *parse.FunctionExpression:
			c.data.addFnCapturedGlobal(scope, node.Name, &globalVarInfo)
		case *parse.EmbeddedModule:
			embeddedModIndex := -1
			for i, ancestor := range ancestorChain {
				if ancestor == scope {
					embeddedModIndex = i
					break
				}
			}

			if embeddedModIndex < 0 {
				panic(ErrUnreachable)
			}

			if embeddedModIndex == 0 {
				break
			}

			_, ok := ancestorChain[embeddedModIndex-1].(*parse.LifetimejobExpression)
			if ok {
				parentScopeNode := findClosestScopeContainerNode(ancestorChain[:embeddedModIndex-1])
				if fnExpr, ok := parentScopeNode.(*parse.FunctionExpression); ok {
					c.data.addFnCapturedGlobal(fnExpr, node.Name, &globalVarInfo)
				}
			}
		case *parse.DynamicMappingEntry, *parse.StaticMappingEntry:
			mappingExpr := findClosest[*parse.MappingExpression](ancestorChain)
			c.data.addMappingCapturedGlobal(mappingExpr, node.Name)
		}

	case *parse.IdentifierLiteral:

		if len(node.Name) > MAX_NAME_BYTE_LEN {
			c.addError(node, fmtNameIsTooLong(node.Name))
			return parse.ContinueTraversal
		}

		if _, ok := scopeNode.(*parse.LazyExpression); ok {
			break
		}

		//we check the parent to know if the identifier refers to a variable
		switch p := parent.(type) {
		case *parse.CallExpression:
			if p.CommandLikeSyntax && !node.IncludedIn(p.Callee) {
				break top_switch
			}
		case *parse.ObjectProperty:
			if p.Key == node {
				break top_switch
			}
		case *parse.ObjectPatternProperty:
			if p.Key == node {
				break top_switch
			}
		case *parse.ObjectMetaProperty:
			if p.Key == node {
				break top_switch
			}
		case *parse.StructDefinition:
			if p.Name == node {
				break top_switch
			}

		case *parse.StructFieldDefinition:
			if p.Name == node {
				break top_switch
			}
		case *parse.NewExpression:
			if p.Type == node {
				break top_switch
			}
		case *parse.StructFieldInitialization:
			if p.Name == node {
				break top_switch
			}
		case *parse.IdentifierMemberExpression:
			if node != p.Left {
				break top_switch
			}
		case *parse.DynamicMemberExpression:
			if node != p.Left {
				break top_switch
			}
		case *parse.PatternNamespaceMemberExpression:
			break top_switch
		case *parse.DoubleColonExpression:
			if node == p.Element {
				break top_switch
			}
		case *parse.DynamicMappingEntry:
			if node == p.KeyVar || node == p.GroupMatchingVariable {
				break top_switch
			}
		case *parse.ForStatement, *parse.WalkStatement, *parse.ObjectLiteral, *parse.FunctionDeclaration, *parse.MemberExpression, *parse.QuantityLiteral, *parse.RateLiteral,
			*parse.KeyListExpression:
			break top_switch
		case *parse.XMLOpeningElement:
			if node == p.Name {
				break top_switch
			}
		case *parse.XMLClosingElement:
			if node == p.Name {
				break top_switch
			}
		case *parse.XMLAttribute:
			if node == p.Name {
				break top_switch
			}
		}

		if _, ok := scopeNode.(*parse.ExtendStatement); ok {
			c.addError(node, VARS_NOT_ALLOWED_IN_PATTERN_AND_EXTENSION_OBJECT_PROPERTIES)
			return parse.ContinueTraversal
		}

		if _, ok := scopeNode.(*parse.StructDefinition); ok {
			c.addError(node, VARS_CANNOT_BE_USED_IN_STRUCT_FIELD_DEFS)
			return parse.ContinueTraversal
		}

		if !c.varExists(node.Name, ancestorChain) {
			if node.Name == "const" {
				c.addError(node, VAR_CONST_NOT_DECLARED_IF_YOU_MEANT_TO_DECLARE_CONSTANTS_GLOBAL_CONST_DECLS_ONLY_SUPPORTED_AT_THE_START_OF_THE_MODULE)
			} else {
				c.addError(node, fmtVarIsNotDeclared(node.Name))
			}
			return parse.ContinueTraversal
		}

		// if the variable is a global in a function expression or in a mapping entry we capture it
		if c.doGlobalVarExist(node.Name, closestModule) {
			globalVarInfo := c.getModGlobalVars(closestModule)[node.Name]

			switch scope := scopeNode.(type) {
			case *parse.FunctionExpression:
				c.data.addFnCapturedGlobal(scope, node.Name, &globalVarInfo)
			case *parse.EmbeddedModule:
				embeddedModIndex := -1
				for i, ancestor := range ancestorChain {
					if ancestor == scope {
						embeddedModIndex = i
						break
					}
				}

				if embeddedModIndex < 0 {
					panic(ErrUnreachable)
				}

				if embeddedModIndex == 0 {
					break
				}

				_, ok := ancestorChain[embeddedModIndex-1].(*parse.LifetimejobExpression)
				if ok {
					parentScopeNode := findClosestScopeContainerNode(ancestorChain[:embeddedModIndex-1])
					if fnExpr, ok := parentScopeNode.(*parse.FunctionExpression); ok {
						c.data.addFnCapturedGlobal(fnExpr, node.Name, &globalVarInfo)
					}
				}
			case *parse.DynamicMappingEntry, *parse.StaticMappingEntry:
				mappingExpr := findClosest[*parse.MappingExpression](ancestorChain)
				c.data.addMappingCapturedGlobal(mappingExpr, node.Name)
			}
		}

	case *parse.SelfExpression, *parse.SendValueExpression:
		isSelfExpr := true

		var objectLiteral *parse.ObjectLiteral
		var misplacementErr = SELF_ACCESSIBILITY_EXPLANATION
		isSelfInExtensionObjectMethod := false
		isSelfInStructMethod := false

		switch node.(type) {
		case *parse.SendValueExpression:
			isSelfExpr = false
			misplacementErr = MISPLACED_SENDVAL_EXPR
		}

	loop:
		for i := len(ancestorChain) - 1; i >= 0; i-- {
			if !parse.IsScopeContainerNode(ancestorChain[i]) {
				continue
			}

			switch a := ancestorChain[i].(type) {
			case *parse.InitializationBlock:
				switch i {
				case 0:
				default:
					switch ancestorChain[i-1].(type) {
					case *parse.ObjectMetaProperty:
						if i == 1 {
							c.addError(node, CANNOT_CHECK_OBJECT_METAPROP_WITHOUT_PARENT)
							break
						}
					}

					switch ancestor := ancestorChain[i-2].(type) {
					case *parse.ObjectLiteral:
						objectLiteral = ancestor
					default:
					}
				}
				break loop
			case *parse.FunctionExpression:
				//Determine if the function is the method of an object, extension or struct.

				j := i - 1

				if j == -1 {
					break loop
				}

				if _, ok := ancestorChain[j].(*parse.ReceptionHandlerExpression); ok {
					j--
				}

				switch ancestorChain[j].(type) {
				case *parse.ObjectProperty:
					if j == 0 {
						c.addError(node, CANNOT_CHECK_OBJECT_PROP_WITHOUT_PARENT)
						break loop
					}
					j--

					switch ancestor := ancestorChain[j].(type) {
					case *parse.ObjectLiteral:
						objectLiteral = ancestor
						if j-1 >= 0 {
							isSelfInExtensionObjectMethod =
								utils.Implements[*parse.ExtendStatement](ancestorChain[j-1]) &&
									ancestorChain[j-1].(*parse.ExtendStatement).Extension == objectLiteral
						}
					default:
					}
				case *parse.FunctionDeclaration:
					if j == 0 {
						c.addError(node, CANNOT_CHECK_STRUCT_METHOD_DEF_WITHOUT_PARENT)
						break loop
					}
					_, ok := ancestorChain[j-1].(*parse.StructBody)
					isSelfInStructMethod = ok && isSelfExpr
				}

				break loop
			case *parse.EmbeddedModule: //ok if lifetime job
				if i == 0 || !utils.Implements[*parse.LifetimejobExpression](ancestorChain[i-1]) {
					c.addError(node, misplacementErr)
				}
				return parse.ContinueTraversal
			case *parse.Chunk:
				if c.currentModule != nil && c.currentModule.ModuleKind == LifetimeJobModule { // ok
					return parse.ContinueTraversal
				}
			case *parse.ExtendStatement:
				if isSelfExpr && node.Base().IncludedIn(a.Extension) { //ok
					return parse.ContinueTraversal
				}
			}
		}

		if !isSelfInStructMethod {
			if objectLiteral == nil {
				c.addError(node, misplacementErr)
				return parse.ContinueTraversal
			}

			propInfo := c.getPropertyInfo(objectLiteral)

			switch p := parent.(type) {
			case *parse.MemberExpression:
				if !propInfo.known[p.PropertyName.Name] && !isSelfInExtensionObjectMethod {
					c.addError(p, fmtObjectDoesNotHaveProp(p.PropertyName.Name))
				}
			}
		}
	case *parse.HostAliasDefinition:
		switch parent.(type) {
		case *parse.Chunk, *parse.EmbeddedModule:
		default:
			if !inPreinitBlock {
				c.addError(node, MISPLACED_HOST_ALIAS_DEF_STATEMENT_TOP_LEVEL_STMT)
				return parse.Prune
			}
		}
		aliasName := node.Left.Value[1:]
		hostAliases := c.getModHostAliases(closestModule)

		if _, alreadyDefined := hostAliases[aliasName]; alreadyDefined && !inPreinitBlock {
			c.addError(node, fmtHostAliasAlreadyDeclared(aliasName))
		} else {
			hostAliases[aliasName] = 0
		}

	case *parse.PatternDefinition:
		switch parent.(type) {
		case *parse.Chunk, *parse.EmbeddedModule:
		default:
			if !inPreinitBlock {
				c.addError(node, MISPLACED_PATTERN_DEF_STATEMENT_TOP_LEVEL_STMT)
				return parse.Prune
			}
		}

		patternName, ok := node.PatternName()
		if ok {
			patterns := c.getModPatterns(closestModule)

			if _, alreadyDefined := patterns[patternName]; alreadyDefined && !inPreinitBlock {
				c.addError(node, fmtPatternAlreadyDeclared(patternName))
			} else {
				patterns[patternName] = 0
			}
		}
	case *parse.PatternNamespaceDefinition:
		switch parent.(type) {
		case *parse.Chunk, *parse.EmbeddedModule:
		default:
			if !inPreinitBlock {
				c.addError(node, MISPLACED_PATTERN_NS_DEF_STATEMENT_TOP_LEVEL_STMT)
				return parse.Prune
			}
		}

		namespaceName, ok := node.NamespaceName()
		if ok {
			namespaces := c.getModPatternNamespaces(closestModule)
			if _, alreadyDefined := namespaces[namespaceName]; alreadyDefined && !inPreinitBlock {
				c.addError(node, fmtPatternNamespaceAlreadyDeclared(namespaceName))
			} else {
				namespaces[namespaceName] = 0
			}
		}
	case *parse.PatternNamespaceIdentifierLiteral:
		namespaceName := node.Name
		namespaces := c.getModPatternNamespaces(closestModule)

		if _, alreadyDefined := namespaces[namespaceName]; !alreadyDefined {
			c.addError(node, fmtPatternNamespaceIsNotDeclared(namespaceName))
		}
	case *parse.PatternIdentifierLiteral:

		if _, ok := parent.(*parse.OtherPropsExpr); ok && node.Name == parse.NO_OTHERPROPS_PATTERN_NAME {
			break top_switch
		}

		if def, ok := parent.(*parse.StructDefinition); ok && def.Name == node {
			break top_switch
		}

		//Check if struct type.
		stuctDefs := c.getModStructDefs(closestModule)
		_, ok := stuctDefs[node.Name]
		if ok {
			//Check that the node is not misplaced.
			errMsg := ""
			switch parent := parent.(type) {
			case *parse.PointerType, *parse.StructFieldDefinition, *parse.NewExpression:
				//ok
			case *parse.FunctionParameter:
				errMsg = STRUCT_TYPES_NOT_ALLOWED_AS_PARAMETER_TYPES
			case *parse.FunctionExpression:
				if node == parent.ReturnType {
					errMsg = STRUCT_TYPES_NOT_ALLOWED_AS_RETURN_TYPES
				} else {
					errMsg = MISPLACED_STRUCT_TYPE_NAME
				}
			default:
				errMsg = MISPLACED_STRUCT_TYPE_NAME
			}

			if errMsg != "" {
				c.addError(node, errMsg)
			}

			break top_switch
		}

		//Ignore the check if the pattern identifier refers to a pattern that is not yet defined.

		for _, a := range ancestorChain {
			if def, ok := a.(*parse.PatternDefinition); ok && def.IsLazy {
				break top_switch
			}
		}

		//Check that the pattern is declared.

		name := node.Name
		patterns := c.getModPatterns(closestModule)
		if _, ok := patterns[name]; !ok {
			errMsg := ""
			switch parent.(type) {
			case *parse.PointerType, *parse.NewExpression:
				errMsg = fmtStructTypeIsNotDefined(name)
			default:
				errMsg = fmtPatternIsNotDeclared(name)
			}
			c.addError(node, errMsg)
		}
	case *parse.RuntimeTypeCheckExpression:
		switch p := parent.(type) {
		case *parse.CallExpression:
			for _, arg := range p.Arguments {
				if n == arg {
					break top_switch //ok
				}
			}

			c.addError(node, MISPLACED_RUNTIME_TYPECHECK_EXPRESSION)
		default:
			c.addError(node, MISPLACED_RUNTIME_TYPECHECK_EXPRESSION)
		}
	case *parse.DynamicMemberExpression:
		if node.Optional {
			c.addError(node, OPTIONAL_DYN_MEMB_EXPR_NOT_SUPPORTED_YET)
		}
	case *parse.ExtendStatement:
		if _, ok := parent.(*parse.Chunk); !ok {
			c.addError(node, MISPLACED_EXTEND_STATEMENT_TOP_LEVEL_STMT)
			return parse.ContinueTraversal
		}
	case *parse.StructDefinition:
		if parent != closestModule {
			c.addError(node, MISPLACED_STRUCT_DEF_TOP_LEVEL_STMT)
			return parse.ContinueTraversal
		}
		//already defined.
		return parse.ContinueTraversal
	case *parse.NewExpression:
		typ := node.Type
		switch t := typ.(type) {
		case *parse.IdentifierLiteral:
			defs := c.getModStructDefs(closestModule)
			_, ok := defs[t.Name]
			if !ok {
				c.addError(node, fmtStructTypeIsNotDefined(t.Name))
			}
		//TODO: support slices
		case nil:
			return parse.ContinueTraversal
		default:
			c.addError(node.Type, A_STRUCT_TYPE_NAME_IS_EXPECTED)
			return parse.ContinueTraversal
		}
	case *parse.StructInitializationLiteral:
		// look for duplicate field names
		fieldNames := make([]string, 0, len(node.Fields))

		for _, field := range node.Fields {
			fieldInit, ok := field.(*parse.StructFieldInitialization)
			if ok {
				name := fieldInit.Name.Name
				if slices.Contains(fieldNames, name) {
					c.addError(fieldInit.Name, fmtDuplicateFieldName(name))
				} else {
					fieldNames = append(fieldNames, name)
				}
			}
		}
	case *parse.PointerType:
		_, ok := node.ValueType.(*parse.PatternIdentifierLiteral)
		if !ok {
			c.addError(node.ValueType, A_STRUCT_TYPE_IS_EXPECTED_AFTER_THE_STAR)
		} else {
			//Check that the node is not misplaced.
			switch parent := parent.(type) {
			case *parse.StructFieldDefinition, *parse.FunctionParameter:
				//ok
			case *parse.FunctionExpression:
				if node != parent.ReturnType {
					c.addError(node, MISPLACED_POINTER_TYPE)
				}
			case *parse.LocalVariableDeclaration:
				if node != parent.Type {
					c.addError(node, MISPLACED_POINTER_TYPE)
				}
			default:
				c.addError(node, MISPLACED_POINTER_TYPE)
			}
			break top_switch
		}
	case *parse.DereferenceExpression:
		c.addError(node, "dereference expressions are not supported yet")
	case *parse.TestSuiteExpression:
		hasSubsuiteStmt := false
		hasTestCaseStmt := false

		for _, stmt := range node.Module.Statements {
			switch stmt := stmt.(type) {
			case *parse.TestCaseExpression:
				if stmt.IsStatement {
					hasTestCaseStmt = true
				}
			case *parse.TestSuiteExpression:
				if stmt.IsStatement {
					hasSubsuiteStmt = true
				}
			}
		}

		if hasSubsuiteStmt && hasTestCaseStmt {
			for _, stmt := range node.Module.Statements {
				switch stmt := stmt.(type) {
				case *parse.TestCaseExpression:
					if stmt.IsStatement {
						c.addError(stmt, TEST_CASES_NOT_ALLOWED_IF_SUBSUITES_ARE_PRESENT)
					}
				case *parse.TestSuiteExpression:
					if stmt.IsStatement {
						hasSubsuiteStmt = true
					}
				}
			}
		}

		//check the statement is not in a testcase
		if node.IsStatement {

		search_test_case:
			for i := len(ancestorChain) - 1; i >= 0; i-- {
				switch ancestorChain[i].(type) {
				case *parse.EmbeddedModule:
					if i-1 <= 0 {
						break search_test_case
					}
					testCaseExpr, ok := ancestorChain[i-1].(*parse.TestCaseExpression)
					if ok && testCaseExpr.IsStatement {
						c.addError(n, TEST_SUITE_STMTS_NOT_ALLOWED_INSIDE_TEST_CASE_STMTS)
						break search_test_case
					}
				}
			}
		}
	case *parse.TestCaseExpression:

		inTestSuite := false

	search_test_suite:
		for i := len(ancestorChain) - 1; i >= 0; i-- {
			switch ancestorChain[i].(type) {
			case *parse.EmbeddedModule:
				if i-1 <= 0 {
					break search_test_suite
				}
				testSuiteExpr, ok := ancestorChain[i-1].(*parse.TestSuiteExpression)
				if ok {
					inTestSuite = testSuiteExpr.Module == ancestorChain[i]
					break search_test_suite
				}
			}
		}

		if !inTestSuite && node.IsStatement && (c.currentModule == nil || c.currentModule.ModuleKind != TestSuiteModule) {
			c.addError(n, TEST_CASE_STMTS_NOT_ALLOWED_OUTSIDE_OF_TEST_SUITES)
		}
	case *parse.EmbeddedModule:
		parentModule := findClosestModule(ancestorChain)
		globals := c.getModGlobalVars(n)
		patterns := c.getModPatterns(n)
		patternNamespaces := c.getModPatternNamespaces(n)
		hostAliases := c.getModHostAliases(n)

		parentModuleGlobals := c.getModGlobalVars(parentModule)
		parentModulePatterns := c.getModPatterns(parentModule)
		parentModulePatternNamespaces := c.getModPatternNamespaces(parentModule)
		parentModuleHostAliases := c.getModHostAliases(parentModule)

		switch parent.(type) {
		case *parse.TestSuiteExpression:
			//inherit globals
			for name, info := range parentModuleGlobals {
				if slices.Contains(globalnames.TEST_ITEM_NON_INHERITED_GLOBALS, name) {
					continue
				}
				globals[name] = info
			}

			//inherit patterns
			for name, info := range parentModulePatterns {
				patterns[name] = info
			}
			for name, info := range parentModulePatternNamespaces {
				patternNamespaces[name] = info
			}

			//inherit host aliases
			for name, info := range parentModuleHostAliases {
				hostAliases[name] = info
			}
		case *parse.TestCaseExpression:
			globals[globalnames.CURRENT_TEST] = globalVarInfo{isConst: true, isStartConstant: true}

			//inherit globals
			for name, info := range parentModuleGlobals {
				if slices.Contains(globalnames.TEST_ITEM_NON_INHERITED_GLOBALS, name) {
					continue
				}
				globals[name] = info
			}

			//inherit patterns
			for name, info := range parentModulePatterns {
				patterns[name] = info
			}
			for name, info := range parentModulePatternNamespaces {
				patternNamespaces[name] = info
			}

			//inherit host aliases
			for name, info := range parentModuleHostAliases {
				hostAliases[name] = info
			}
		}
	}

	return parse.ContinueTraversal
}

// checkSingleNode perform post checks on a single node.
func (checker *checker) postCheckSingleNode(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, _ bool) parse.TraversalAction {

	closestModule := findClosestModule(ancestorChain)
	_ = closestModule

	switch n := node.(type) {
	case *parse.ObjectLiteral:
		//manifest

		if utils.Implements[*parse.Manifest](parent) {
			if len(ancestorChain) < 3 {
				checker.addError(parent, CANNOT_CHECK_MANIFEST_WITHOUT_PARENT)
				break
			}

			chunk := ancestorChain[len(ancestorChain)-2]
			isEmbeddedModule := utils.Implements[*parse.EmbeddedModule](chunk)

			if isEmbeddedModule {
				var moduleKind ModuleKind
				switch ancestorChain[len(ancestorChain)-3].(type) {
				case *parse.LifetimejobExpression:
					moduleKind = LifetimeJobModule
				case *parse.SpawnExpression:
					moduleKind = UserLThreadModule
				case *parse.TestSuiteExpression:
					moduleKind = TestSuiteModule
				case *parse.TestCaseExpression:
					moduleKind = TestCaseModule
				default:
					panic(ErrUnreachable)
				}

				checkManifestObject(manifestStaticCheckArguments{
					objLit:                n,
					ignoreUnknownSections: true,
					moduleKind:            moduleKind,
					onError: func(n parse.Node, msg string) {
						checker.addError(n, msg)
					},
				})
			} //else: the manifest of regular modules is already checked during the pre-init phase
		}
	case *parse.ForStatement, *parse.WalkStatement:
		varsBefore := checker.store[node].(map[string]localVarInfo)
		checker.setScopeLocalVars(scopeNode, varsBefore)
	case *parse.MatchStatement:
		varsBefore, ok := checker.store[node]
		if ok {
			checker.setScopeLocalVars(scopeNode, varsBefore.(map[string]localVarInfo))
		}
	}
	return parse.ContinueTraversal
}

type preinitBlockCheckParams struct {
	node    *parse.PreinitStatement
	fls     afs.Filesystem
	onError func(n parse.Node, msg string)
	module  *Module
}

func checkPreinitBlock(args preinitBlockCheckParams) {
	parse.Walk(args.node.Block, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		switch n := node.(type) {
		case *parse.Block, *parse.IdentifierLiteral,
			parse.SimpleValueLiteral, *parse.URLExpression,

			//patterns
			*parse.PatternDefinition, *parse.PatternIdentifierLiteral,
			*parse.PatternNamespaceDefinition, *parse.PatternConversionExpression,
			*parse.ComplexStringPatternPiece, *parse.PatternPieceElement,
			*parse.ObjectPatternLiteral, *parse.RecordPatternLiteral, *parse.ObjectPatternProperty,
			*parse.PatternCallExpression, *parse.PatternGroupName,
			*parse.PatternUnion, *parse.ListPatternLiteral, *parse.TuplePatternLiteral,

			//host alias
			*parse.HostAliasDefinition, *parse.AtHostLiteral:
			//ok
		case *parse.InclusionImportStatement:
			includedChunk := args.module.InclusionStatementMap[n]

			checkPatternOnlyIncludedChunk(includedChunk.Node, args.onError)
		default:
			args.onError(n, fmt.Sprintf("%s: %T", ErrForbiddenNodeinPreinit, n))
			return parse.Prune, nil
		}

		return parse.ContinueTraversal, nil
	}, nil)
}

func checkPatternOnlyIncludedChunk(chunk *parse.Chunk, onError func(n parse.Node, msg string)) {
	parse.Walk(chunk, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {

		if node == chunk {
			return parse.ContinueTraversal, nil
		}

		switch n := node.(type) {
		case *parse.IncludableChunkDescription,
			//
			parse.SimpleValueLiteral,

			//patterns
			*parse.PatternDefinition, *parse.PatternIdentifierLiteral,
			*parse.PatternNamespaceDefinition, *parse.PatternConversionExpression,
			*parse.ComplexStringPatternPiece, *parse.PatternPieceElement,
			*parse.ObjectPatternLiteral, *parse.RecordPatternLiteral, *parse.ObjectPatternProperty,
			*parse.PatternCallExpression, *parse.PatternGroupName,
			*parse.PatternUnion, *parse.ListPatternLiteral, *parse.TuplePatternLiteral,

			//host alias
			*parse.HostAliasDefinition, *parse.AtHostLiteral:
		default:
			onError(n, fmt.Sprintf("%s: %T", FORBIDDEN_NODE_TYPE_IN_INCLUDABLE_CHUNK_IMPORTED_BY_PREINIT, n))
			return parse.Prune, nil
		}

		return parse.ContinueTraversal, nil
	}, nil)
}

type manifestStaticCheckArguments struct {
	objLit                *parse.ObjectLiteral
	ignoreUnknownSections bool
	moduleKind            ModuleKind
	onError               func(n parse.Node, msg string)
	project               Project
}

func checkManifestObject(args manifestStaticCheckArguments) {
	objLit := args.objLit
	ignoreUnknownSections := args.ignoreUnknownSections
	onError := args.onError

	parse.Walk(objLit, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		switch n := node.(type) {
		case *parse.ObjectLiteral:
			if len(n.SpreadElements) != 0 {
				onError(n, NO_SPREAD_IN_MANIFEST)
			}
			shallowCheckObjectRecordProperties(n.Properties, nil, true, func(n parse.Node, msg string) {
				onError(n, msg)
			})
		case *parse.RecordLiteral:
			if len(n.SpreadElements) != 0 {
				onError(n, NO_SPREAD_IN_MANIFEST)
			}
			shallowCheckObjectRecordProperties(n.Properties, nil, false, func(n parse.Node, msg string) {
				onError(n, msg)
			})
		case *parse.ListLiteral:
			if n.HasSpreadElements() {
				onError(n, NO_SPREAD_IN_MANIFEST)
			}
		}

		return parse.ContinueTraversal, nil
	}, nil)

	for _, p := range objLit.Properties {
		if p.HasImplicitKey() {
			onError(p, IMPLICIT_KEY_PROPS_NOT_ALLOWED_IN_MANIFEST)
			continue
		}

		sectionName := p.Name()
		allowedSectionNames := MODULE_KIND_TO_ALLOWED_SECTION_NAMES[args.moduleKind]
		if !slices.Contains(allowedSectionNames, sectionName) {
			onError(p.Key, fmtTheXSectionIsNotAllowedForTheCurrentModuleKind(sectionName, args.moduleKind))
			continue
		}

		switch sectionName {
		case MANIFEST_KIND_SECTION_NAME:
			kindName, ok := getUncheckedModuleKindNameFromNode(p.Value)
			if !ok {
				onError(p.Key, KIND_SECTION_SHOULD_BE_A_STRING_LITERAL)
				continue
			}

			kind, err := ParseModuleKind(kindName)
			if err != nil {
				onError(p.Key, ErrInvalidModuleKind.Error())
				continue
			}
			if kind.IsEmbedded() {
				onError(p.Key, INVALID_KIND_SECTION_EMBEDDED_MOD_KINDS_NOT_ALLOWED)
				continue
			}
		case MANIFEST_PERMS_SECTION_NAME:
			if obj, ok := p.Value.(*parse.ObjectLiteral); ok {
				checkPermissionListingObject(obj, onError)
			} else {
				onError(p, PERMS_SECTION_SHOULD_BE_AN_OBJECT)
			}
		case MANIFEST_HOST_RESOLUTION_SECTION_NAME:
			dict, ok := p.Value.(*parse.DictionaryLiteral)
			if !ok {
				onError(p, HOST_RESOL_SECTION_SHOULD_BE_A_DICT)
				continue
			}

			hasErrors := false

			parse.Walk(dict, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
				if node == dict {
					return parse.ContinueTraversal, nil
				}

				switch n := node.(type) {
				case *parse.ObjectLiteral, *parse.ObjectProperty:
				case *parse.DictionaryEntry, parse.SimpleValueLiteral, *parse.GlobalVariable,
					*parse.IdentifierMemberExpression:
				default:
					hasErrors = true
					onError(n, fmtForbiddenNodeInHostResolutionSection(n))
				}

				return parse.ContinueTraversal, nil
			}, nil)

			if !hasErrors {
				staticallyCheckHostResolutionDataFnRegistryLock.Lock()
				defer staticallyCheckHostResolutionDataFnRegistryLock.Unlock()

				for _, entry := range dict.Entries {
					key := entry.Key

					switch k := key.(type) {
					case *parse.InvalidURL:
					case *parse.HostLiteral:
						host := utils.Must(evalSimpleValueLiteral(k, nil)).(Host)
						fn, ok := staticallyCheckHostResolutionDataFnRegistry[host.Scheme()]
						if ok {
							errMsg := fn(args.project, entry.Value)
							if errMsg != "" {
								onError(entry.Value, errMsg)
							}
						} else {
							onError(k, HOST_SCHEME_NOT_SUPPORTED)
						}
					default:
						onError(k, HOST_RESOL_SECTION_SHOULD_BE_A_DICT)
					}
				}
			}
		case MANIFEST_LIMITS_SECTION_NAME:
			obj, ok := p.Value.(*parse.ObjectLiteral)

			if !ok {
				onError(p, LIMITS_SECTION_SHOULD_BE_AN_OBJECT)
				continue
			}

			parse.Walk(obj, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
				if node == obj {
					return parse.ContinueTraversal, nil
				}

				switch n := node.(type) {
				case *parse.ObjectProperty, parse.SimpleValueLiteral, *parse.GlobalVariable:
				default:
					onError(n, fmtForbiddenNodeInLimitsSection(n))
				}

				return parse.ContinueTraversal, nil
			}, nil)
		case MANIFEST_ENV_SECTION_NAME:

			if args.moduleKind.IsEmbedded() {
				onError(p, ENV_SECTION_NOT_AVAILABLE_IN_EMBEDDED_MODULE_MANIFESTS)
				continue
			}

			patt, ok := p.Value.(*parse.ObjectPatternLiteral)

			if !ok {
				onError(p, ENV_SECTION_SHOULD_BE_AN_OBJECT_PATTERN)
				continue
			}

			parse.Walk(patt, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
				if node == patt {
					return parse.ContinueTraversal, nil
				}

				switch n := node.(type) {
				case *parse.PatternIdentifierLiteral, *parse.PatternNamespaceMemberExpression,
					*parse.ObjectPatternProperty, *parse.PatternCallExpression, parse.SimpleValueLiteral, *parse.GlobalVariable:
				default:
					onError(n, fmtForbiddenNodeInEnvSection(n))
				}

				return parse.ContinueTraversal, nil
			}, nil)
		case MANIFEST_PREINIT_FILES_SECTION_NAME:
			if args.moduleKind.IsEmbedded() {
				onError(p, PREINIT_FILES_SECTION_NOT_AVAILABLE_IN_EMBEDDED_MODULE_MANIFESTS)
				continue
			}

			obj, ok := p.Value.(*parse.ObjectLiteral)

			if !ok {
				onError(p, PREINIT_FILES_SECTION_SHOULD_BE_AN_OBJECT)
				continue
			}

			checkPreinitFilesObject(obj, onError)
		case MANIFEST_DATABASES_SECTION_NAME:
			if args.moduleKind.IsEmbedded() {
				onError(p, DATABASES_SECTION_NOT_AVAILABLE_IN_EMBEDDED_MODULE_MANIFESTS)
				continue
			}

			switch propVal := p.Value.(type) {
			case *parse.ObjectLiteral:
				checkDatabasesObject(propVal, onError, nil, args.project)
			case *parse.AbsolutePathLiteral:
			default:
				onError(p, DATABASES_SECTION_SHOULD_BE_AN_OBJECT_OR_ABS_PATH)
			}
		case MANIFEST_INVOCATION_SECTION_NAME:
			if args.moduleKind.IsEmbedded() {
				onError(p, INVOCATION_SECTION_NOT_AVAILABLE_IN_EMBEDDED_MODULE_MANIFESTS)
				continue
			}

			switch propVal := p.Value.(type) {
			case *parse.ObjectLiteral:
				checkInvocationObject(propVal, objLit, onError, args.project)
			default:
				onError(p, INVOCATION_SECTION_SHOULD_BE_AN_OBJECT)
			}
		case MANIFEST_PARAMS_SECTION_NAME:
			if args.moduleKind.IsEmbedded() {
				onError(p, PARAMS_SECTION_NOT_AVAILABLE_IN_EMBEDDED_MODULE_MANIFESTS)
				continue
			}

			obj, ok := p.Value.(*parse.ObjectLiteral)

			if !ok {
				onError(p, PARAMS_SECTION_SHOULD_BE_AN_OBJECT)
				continue
			}

			checkParametersObject(obj, onError)
		default:
			if !ignoreUnknownSections {
				onError(p, fmtUnknownSectionOfManifest(p.Name()))
			}
		}
	}

}

func checkPermissionListingObject(objLit *parse.ObjectLiteral, onError func(n parse.Node, msg string)) {
	parse.Walk(objLit, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		switch n := node.(type) {
		case *parse.ObjectLiteral, *parse.ListLiteral, *parse.DictionaryLiteral, *parse.DictionaryEntry, *parse.ObjectProperty,
			parse.SimpleValueLiteral, *parse.GlobalVariable, *parse.PatternIdentifierLiteral, *parse.URLExpression, *parse.PathPatternExpression:
		default:
			onError(n, fmtForbiddenNodeInPermListing(n))
		}

		return parse.ContinueTraversal, nil
	}, nil)

	for _, p := range objLit.Properties {
		if p.HasImplicitKey() {
			onError(p, IMPLICIT_KEY_PROPS_NOT_ALLOWED_IN_PERMS_SECTION)
			continue
		}

		propName := p.Name()
		permKind, ok := permkind.PermissionKindFromString(propName)
		if !ok {
			onError(p.Key, fmtNotValidPermissionKindName(p.Name()))
			continue
		}
		checkSingleKindPermissions(permKind, p.Value, onError)
	}
}

func checkSingleKindPermissions(permKind PermissionKind, desc parse.Node, onError func(n parse.Node, msg string)) {
	checkSingleItem := func(node parse.Node) {
		switch n := node.(type) {
		case *parse.AbsolutePathExpression:
		case *parse.AbsolutePathLiteral:
		case *parse.RelativePathLiteral:
			onError(n, fmtOnlyAbsPathsAreAcceptedInPerms(n.Raw))
		case *parse.AbsolutePathPatternLiteral:
		case *parse.RelativePathPatternLiteral:
			onError(n, fmtOnlyAbsPathPatternsAreAcceptedInPerms(n.Raw))
		case *parse.URLExpression:
		case *parse.URLLiteral:
		case *parse.URLPatternLiteral:
		case *parse.HostLiteral:
		case *parse.HostPatternLiteral:
		case *parse.PatternIdentifierLiteral, *parse.PatternNamespaceIdentifierLiteral:
		case *parse.GlobalVariable, *parse.Variable, *parse.IdentifierLiteral:

		case *parse.QuotedStringLiteral, *parse.MultilineStringLiteral, *parse.UnquotedStringLiteral:
			s := n.(parse.SimpleValueLiteral).ValueString()

			if len(s) <= 1 {
				onError(n, NO_PERM_DESCRIBED_BY_STRINGS)
				break
			}

			msg := NO_PERM_DESCRIBED_BY_STRINGS + ", "
			startsWithPercent := s[0] == '%'
			stringNoPercent := s
			if startsWithPercent {
				stringNoPercent = s[1:]
			}

			for _, prefix := range []string{"/", "./", "../"} {
				if strings.HasPrefix(stringNoPercent, prefix) {
					if startsWithPercent {
						msg += MAYBE_YOU_MEANT_TO_WRITE_A_PATH_PATTERN_LITERAL
					} else {
						msg += MAYBE_YOU_MEANT_TO_WRITE_A_PATH_LITERAL
					}
					break
				}
			}

			for _, prefix := range []string{"https://", "http://"} {
				if strings.HasPrefix(stringNoPercent, prefix) {
					if startsWithPercent {
						msg += MAYBE_YOU_MEANT_TO_WRITE_A_URL_PATTERN_LITERAL
					} else {
						msg += MAYBE_YOU_MEANT_TO_WRITE_A_URL_LITERAL
					}
					break
				}
			}

			onError(n, msg)
		default:
			onError(n, NO_PERM_DESCRIBED_BY_THIS_TYPE_OF_VALUE)
		}
	}

	switch v := desc.(type) {
	case *parse.ListLiteral:
		for _, elem := range v.Elements {
			checkSingleItem(elem)
		}
	case *parse.ObjectLiteral:
		for _, prop := range v.Properties {
			if prop.HasImplicitKey() {
				checkSingleItem(prop.Value)
			} else {
				typeName := prop.Name()

				//TODO: finish
				switch typeName {
				case "dns":
				case "tcp":
				case "globals":
				case "env":
				case "threads":
				case "system-graph":
				case "commands":
				case "values":
				case "custom":
				default:
					onError(prop.Value, fmtCannotInferPermission(permKind.String(), typeName))
				}
			}
		}
	default:
		checkSingleItem(v)
	}

}

func checkPreinitFilesObject(obj *parse.ObjectLiteral, onError func(n parse.Node, msg string)) {

	hasForbiddenNodes := false

	parse.Walk(obj, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		if node == obj {
			return parse.ContinueTraversal, nil
		}

		switch n := node.(type) {
		case *parse.PatternIdentifierLiteral, *parse.PatternNamespaceMemberExpression, *parse.ObjectLiteral,
			*parse.ObjectProperty, *parse.PatternCallExpression, parse.SimpleValueLiteral, *parse.GlobalVariable,
			*parse.AbsolutePathExpression, *parse.RelativePathExpression:
		default:
			onError(n, fmtForbiddenNodeInPreinitFilesSection(n))
			hasForbiddenNodes = true
		}

		return parse.ContinueTraversal, nil
	}, nil)

	if hasForbiddenNodes {
		return
	}

	for _, p := range obj.Properties {
		if p.Value == nil {
			continue
		}
		fileDesc, ok := p.Value.(*parse.ObjectLiteral)
		if !ok {
			onError(p.Value, PREINIT_FILES__FILE_CONFIG_SHOULD_BE_AN_OBJECT)
			continue
		}

		pathNode, ok := fileDesc.PropValue(MANIFEST_PREINIT_FILE__PATH_PROP_NAME)

		if !ok {
			onError(p, fmtMissingPropInPreinitFileDescription(MANIFEST_PREINIT_FILE__PATH_PROP_NAME, p.Name()))
		} else {
			switch pathNode.(type) {
			case *parse.AbsolutePathLiteral, *parse.AbsolutePathExpression:
			default:
				onError(p, PREINIT_FILES__FILE_CONFIG_PATH_SHOULD_BE_ABS_PATH)
			}
		}

		if !fileDesc.HasNamedProp(MANIFEST_PREINIT_FILE__PATTERN_PROP_NAME) {
			onError(p, fmtMissingPropInPreinitFileDescription(MANIFEST_PREINIT_FILE__PATTERN_PROP_NAME, p.Name()))
		}

	}
}

func checkDatabasesObject(
	obj *parse.ObjectLiteral,
	onError func(n parse.Node, msg string), //optional
	onValidDatabase func(name string, scheme Scheme, resource ResourceName), //optional
	project Project,
) {

	if onError == nil {
		onError = func(n parse.Node, msg string) {}
	}

	if onValidDatabase == nil {
		onValidDatabase = func(name string, scheme Scheme, resource ResourceName) {}
	}

	parse.Walk(obj, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		if node == obj {
			return parse.ContinueTraversal, nil
		}

		switch n := node.(type) {
		case *parse.PatternIdentifierLiteral, *parse.PatternNamespaceMemberExpression, *parse.ObjectLiteral,
			*parse.ObjectProperty, *parse.PatternCallExpression, parse.SimpleValueLiteral, *parse.GlobalVariable,
			*parse.AbsolutePathExpression, *parse.RelativePathExpression:
		default:
			onError(n, fmtForbiddenNodeInDatabasesSection(n))
		}

		return parse.ContinueTraversal, nil
	}, nil)

	for _, p := range obj.Properties {
		if p.HasImplicitKey() || p.Value == nil {
			continue
		}
		dbName := p.Name()

		dbDesc, ok := p.Value.(*parse.ObjectLiteral)
		if !ok {
			onError(p.Value, DATABASES__DB_CONFIG_SHOULD_BE_AN_OBJECT)
			continue
		}

		var scheme Scheme
		var resource ResourceName
		var resourceFound bool
		var resolutionDataFound bool
		isValidDescription := true

		for _, prop := range dbDesc.Properties {
			if prop.HasImplicitKey() {
				continue
			}

			switch prop.Name() {
			case MANIFEST_DATABASE__RESOURCE_PROP_NAME:
				resourceFound = true

				switch res := prop.Value.(type) {
				case *parse.HostLiteral:
					u, _ := url.Parse(res.Value)
					if u != nil {
						scheme = Scheme(u.Scheme)
						resource = utils.Must(evalSimpleValueLiteral(res, nil)).(Host)
					}
				case *parse.URLLiteral:
					u, _ := url.Parse(res.Value)
					if u != nil {
						scheme = Scheme(u.Scheme)
						resource = utils.Must(evalSimpleValueLiteral(res, nil)).(URL)
					}
				default:
					isValidDescription = false
					onError(p, DATABASES__DB_RESOURCE_SHOULD_BE_HOST_OR_URL)
				}
			case MANIFEST_DATABASE__RESOLUTION_DATA_PROP_NAME:
				resolutionDataFound = true

				switch prop.Value.(type) {
				case *parse.NilLiteral, *parse.HostLiteral, *parse.RelativePathLiteral, *parse.AbsolutePathLiteral,
					*parse.AbsolutePathExpression, *parse.RelativePathExpression:
					if scheme == "" {
						break
					}
					checkData, ok := GetStaticallyCheckDbResolutionDataFn(scheme)
					if ok {
						errMsg := checkData(prop.Value, project)
						if errMsg != "" {
							isValidDescription = false
							onError(prop.Value, errMsg)
						}
					}
				default:
					isValidDescription = false
					onError(p, DATABASES__DB_RESOLUTION_DATA_ONLY_NIL_AND_PATHS_SUPPORTED)
				}
			case MANIFEST_DATABASE__EXPECTED_SCHEMA_UPDATE_PROP_NAME:
				switch prop.Value.(type) {
				case *parse.BooleanLiteral:
				default:
					isValidDescription = false
					onError(p, DATABASES__DB_EXPECTED_SCHEMA_UPDATE_SHOULD_BE_BOOL_LIT)
				}
			case MANIFEST_DATABASE__ASSERT_SCHEMA_UPDATE_PROP_NAME:
				switch prop.Value.(type) {
				case *parse.PatternIdentifierLiteral, *parse.ObjectPatternLiteral:
				default:
					isValidDescription = false
					onError(p, DATABASES__DB_ASSERT_SCHEMA_SHOULD_BE_PATT_IDENT_OR_OBJ_PATT)
				}
			default:
				isValidDescription = false
				onError(p, fmtUnexpectedPropOfDatabaseDescription(prop.Name()))
			}
		}

		if !resourceFound {
			onError(p, fmtMissingPropInDatabaseDescription(MANIFEST_DATABASE__RESOURCE_PROP_NAME, dbName))
		}

		if !resolutionDataFound {
			onError(p, fmtMissingPropInDatabaseDescription(MANIFEST_DATABASE__RESOLUTION_DATA_PROP_NAME, dbName))
		}

		if isValidDescription {
			onValidDatabase(dbName, scheme, resource)
		}
	}
}

func checkInvocationObject(obj *parse.ObjectLiteral, manifestObj *parse.ObjectLiteral, onError func(n parse.Node, msg string), project Project) {

	for _, p := range obj.Properties {
		if p.Value == nil {
			continue
		}

		if p.HasImplicitKey() {
			continue
		}

		switch p.Name() {
		case MANIFEST_INVOCATION__ON_ADDED_ELEM_PROP_NAME:
			if urlLit, ok := p.Value.(*parse.URLLiteral); ok {
				scheme, err := urlLit.Scheme()

				if err == nil {
					if !IsStaticallyCheckDBFunctionRegistered(Scheme(scheme)) {
						onError(manifestObj, SCHEME_NOT_DB_SCHEME_OR_IS_NOT_SUPPORTED)
					} else {
						//if the scheme corresponds to a database and the manifest does not
						//contain the databases section, we add an error
						if !manifestObj.HasNamedProp(MANIFEST_DATABASES_SECTION_NAME) {
							onError(manifestObj, THE_DATABASES_SECTION_SHOULD_BE_PRESENT)
						}
					}
				}

			} else {
				onError(p.Value, ONLY_URL_LITS_ARE_SUPPORTED_FOR_NOW)
			}
		case MANIFEST_INVOCATION__ASYNC_PROP_NAME:
			if _, ok := p.Value.(*parse.BooleanLiteral); !ok {
				onError(p.Value, A_BOOL_LIT_IS_EXPECTED)
			}
		default:
			onError(p, fmtUnexpectedPropOfInvocationDescription(p.Name()))
		}
	}
}

func checkParametersObject(objLit *parse.ObjectLiteral, onError func(n parse.Node, msg string)) {

	parse.Walk(objLit, func(node, parent, scopeNode parse.Node, ancestorChain []parse.Node, after bool) (parse.TraversalAction, error) {
		if node == objLit {
			return parse.ContinueTraversal, nil
		}

		switch n := node.(type) {
		case
			*parse.ObjectProperty, *parse.ObjectLiteral, *parse.ListLiteral,
			*parse.OptionExpression,
			parse.SimpleValueLiteral, *parse.GlobalVariable,
			//patterns
			*parse.PatternCallExpression,
			*parse.ListPatternLiteral, *parse.TuplePatternLiteral,
			*parse.ObjectPatternLiteral, *parse.ObjectPatternProperty, *parse.RecordPatternLiteral,
			*parse.PatternIdentifierLiteral, *parse.PatternNamespaceMemberExpression, *parse.PatternNamespaceIdentifierLiteral,
			*parse.PatternConversionExpression,
			*parse.PatternUnion,
			*parse.PathPatternExpression, *parse.AbsolutePathPatternLiteral, *parse.RelativePathPatternLiteral,
			*parse.URLPatternLiteral, *parse.HostPatternLiteral, *parse.OptionalPatternExpression,
			*parse.OptionPatternLiteral, *parse.FunctionPatternExpression, *parse.NamedSegmentPathPatternLiteral:
		default:
			onError(n, fmtForbiddenNodeInParametersSection(n))
		}

		return parse.ContinueTraversal, nil
	}, nil)

	positionalParamsEnd := false

	for _, prop := range objLit.Properties {
		if !prop.HasImplicitKey() { // non positional parameter
			positionalParamsEnd = true

			propValue := prop.Value
			optionPattern, isOptionPattern := prop.Value.(*parse.OptionPatternLiteral)
			if isOptionPattern {
				propValue = optionPattern.Value
			}

			switch propVal := propValue.(type) {
			case *parse.ObjectLiteral:
				if isOptionPattern {
					break
				}

				missingPropertyNames := []string{"pattern"}

				for _, paramDescProp := range propVal.Properties {
					if paramDescProp.HasImplicitKey() {
						continue
					}
					name := paramDescProp.Name()

					for i, name := range missingPropertyNames {
						if name == paramDescProp.Name() {
							missingPropertyNames[i] = ""
						}
					}

					switch name {
					case "pattern":
						if !parse.NodeIsPattern(paramDescProp.Value) {
							onError(paramDescProp, "the .pattern of a non positional parameter should be a named pattern or a pattern literal")
						}
					case "default":
					case "char-name":
						switch paramDescProp.Value.(type) {
						case *parse.RuneLiteral:
						default:
							onError(paramDescProp, "the .char-name of a non positional parameter should be a rune literal")
						}
					case "description":
						switch paramDescProp.Value.(type) {
						case *parse.QuotedStringLiteral, *parse.MultilineStringLiteral:
						default:
							onError(paramDescProp, "the .description of a non positional parameter should be a string literal")
						}
					}
				}

				missingPropertyNames = utils.FilterSlice(missingPropertyNames, func(s string) bool { return s != "" })
				if len(missingPropertyNames) > 0 {
					onError(prop, "missing properties in description of non positional parameter: "+strings.Join(missingPropertyNames, ", "))
				}
			default:
				if !parse.NodeIsPattern(prop.Value) {
					onError(prop, "the description of a non positional parameter should be a named pattern or a pattern literal")
				}
			}

		} else if positionalParamsEnd {
			onError(prop, "properties with an implicit key describe positional parameters, all implict key properties should be at the top of the 'parameters' section")
		} else { //positional parameter

			obj, ok := prop.Value.(*parse.ObjectLiteral)
			if !ok {
				onError(prop, "the description of a positional parameter should be an object")
				continue
			}

			missingPropertyNames := []string{"name", "pattern"}

			for _, paramDescProp := range obj.Properties {
				if paramDescProp.HasImplicitKey() {
					onError(paramDescProp, "the description of a positional parameter should not contain implicit keys")
					continue
				}

				propName := paramDescProp.Name()

				for i, name := range missingPropertyNames {
					if name == propName {
						missingPropertyNames[i] = ""
					}
				}

				switch propName {
				case "description":
					switch paramDescProp.Value.(type) {
					case *parse.QuotedStringLiteral, *parse.MultilineStringLiteral:
					default:
						onError(paramDescProp, "the .description property of a positional parameter should be a string literal")
					}
				case "rest":
					switch paramDescProp.Value.(type) {
					case *parse.BooleanLiteral:
					default:
						onError(paramDescProp, "the .description property of a positional parameter should be a string literal")
					}
				case "name":
					switch paramDescProp.Value.(type) {
					case *parse.UnambiguousIdentifierLiteral:
					default:
						onError(paramDescProp, "the .description property of a positional parameter should be an identifier (ex: #dir)")
					}
				case "pattern":
					if !parse.NodeIsPattern(paramDescProp.Value) {
						onError(paramDescProp, "the .pattern of a positional parameter should be a named pattern or a pattern literal")
					}
				}
			}

			missingPropertyNames = utils.FilterSlice(missingPropertyNames, func(s string) bool { return s != "" })
			if len(missingPropertyNames) > 0 {
				onError(prop, "missing properties in description of positional parameter: "+strings.Join(missingPropertyNames, ", "))
			}
			//TODO: check unique rest parameter
			_ = obj
		}
	}
}

func checkVisibilityInitializationBlock(propInfo *propertyInfo, block *parse.InitializationBlock, onError func(n parse.Node, msg string)) {
	if len(block.Statements) != 1 || !utils.Implements[*parse.ObjectLiteral](block.Statements[0]) {
		onError(block, INVALID_VISIB_INIT_BLOCK_SHOULD_CONT_OBJ)
		return
	}

	objLiteral := block.Statements[0].(*parse.ObjectLiteral)

	if len(objLiteral.MetaProperties) != 0 {
		onError(objLiteral, INVALID_VISIB_DESC_SHOULDNT_HAVE_METAPROPS)
	}

	for _, prop := range objLiteral.Properties {
		if prop.HasImplicitKey() {
			onError(objLiteral, INVALID_VISIB_DESC_SHOULDNT_HAVE_IMPLICIT_KEYS)
			return
		}

		switch prop.Name() {
		case "public":
			_, ok := prop.Value.(*parse.KeyListExpression)
			if !ok {
				onError(prop, VAL_SHOULD_BE_KEYLIST_LIT)
				return
			}
		case "visible_by":
			dict, ok := prop.Value.(*parse.DictionaryLiteral)
			if !ok {
				onError(prop, VAL_SHOULD_BE_DICT_LIT)
				return
			}

			for _, entry := range dict.Entries {
				switch keyNode := entry.Key.(type) {
				case *parse.UnambiguousIdentifierLiteral:
					switch keyNode.Name {
					case "self":
						_, ok := entry.Value.(*parse.KeyListExpression)
						if !ok {
							onError(entry, VAL_SHOULD_BE_KEYLIST_LIT)
							return
						}
					default:
						onError(entry, INVALID_VISIBILITY_DESC_KEY)
					}
				default:
					onError(entry, INVALID_VISIBILITY_DESC_KEY)
					return
				}
			}
		default:
			onError(prop, INVALID_VISIBILITY_DESC_KEY)
			return
		}
	}
}

func shallowCheckObjectRecordProperties(
	properties []*parse.ObjectProperty,
	spreadElements []*parse.PropertySpreadElement,
	isObject bool,
	addError func(n parse.Node, msg string),
) (parse.TraversalAction, map[string]bool) {
	indexKey := 0
	keys := map[string]bool{}

	// look for duplicate keys
	for _, prop := range properties {
		var k string

		var isExplicit bool

		if prop.Type != nil {
			addError(prop.Type, "type annotation of properties is not allowed")
		}

		switch n := prop.Key.(type) {
		case *parse.QuotedStringLiteral:
			k = n.Value
			isExplicit = true
		case *parse.IdentifierLiteral:
			k = n.Name
			isExplicit = true
		case nil:
			k = strconv.Itoa(indexKey)
			indexKey++
		}

		if len(k) > MAX_NAME_BYTE_LEN {
			addError(prop.Key, fmtNameIsTooLong(k))
		}

		if parse.IsMetadataKey(k) {
			addError(prop.Key, OBJ_REC_LIT_CANNOT_HAVE_METAPROP_KEYS)
		} else if prevIsExplicit, found := keys[k]; found {
			if isExplicit && !prevIsExplicit {
				if isObject {
					addError(prop, fmtObjLitExplicityDeclaresPropWithImplicitKey(k))
				} else {
					addError(prop, fmtRecLitExplicityDeclaresPropWithImplicitKey(k))
				}
			} else {
				addError(prop, fmtDuplicateKey(k))
			}
		}

		keys[k] = isExplicit
	}

	// also look for duplicate keys
	for _, element := range spreadElements {

		extractionExpr, isValid := element.Expr.(*parse.ExtractionExpression)
		if !isValid {
			continue
		}

		for _, key := range extractionExpr.Keys.Keys {
			name := key.(*parse.IdentifierLiteral).Name

			_, found := keys[name]
			if found {
				addError(key, fmtDuplicateKey(name))
				return parse.ContinueTraversal, nil
			}
			keys[name] = true
		}
	}

	return parse.ContinueTraversal, keys
}

// CombineParsingErrorValues combines errors into a single error with a multiline message.
func CombineParsingErrorValues(errs []Error, positions []parse.SourcePositionRange) error {

	if len(errs) == 0 {
		return nil
	}

	goErrors := make([]error, len(errs))
	for i, e := range errs {
		if i < len(positions) {
			goErrors[i] = fmt.Errorf("%s %w", positions[i].String(), e.goError)
		} else {
			goErrors[i] = e.goError
		}
	}

	return utils.CombineErrors(goErrors...)
}

// combineStaticCheckErrors combines static check errors into a single error with a multiline message.
func combineStaticCheckErrors(errs ...*StaticCheckError) error {

	goErrors := make([]error, len(errs))
	for i, e := range errs {
		goErrors[i] = e
	}
	return utils.CombineErrors(goErrors...)
}

type StaticCheckInput struct {
	State                  *GlobalState //mainly used when checking imported modules
	Node                   parse.Node
	Module                 *Module
	Chunk                  *parse.ParsedChunk
	ParentChecker          *checker
	Globals                GlobalVariables
	AdditionalGlobalConsts []string
	ShellLocalVars         map[string]Value
	Patterns               map[string]Pattern
	PatternNamespaces      map[string]*PatternNamespace
}

// A StaticCheckData is the immutable data produced by statically checking a module.
type StaticCheckData struct {
	errors      []*StaticCheckError
	fnData      map[*parse.FunctionExpression]*FunctionStaticData
	mappingData map[*parse.MappingExpression]*MappingStaticData

	//.errors property accessible from scripts
	errorsPropSet atomic.Bool
	errorsProp    *Tuple
}

// Errors returns all errors in the code after a static check, the result should not be modified.
func (d *StaticCheckData) Errors() []*StaticCheckError {
	return d.errors
}

func (d *StaticCheckData) ErrorTuple() *Tuple {
	if d.errorsPropSet.CompareAndSwap(false, true) {
		errors := make([]Serializable, len(d.errors))
		for i, err := range d.errors {
			errors[i] = err.Err()
		}
		d.errorsProp = NewTuple(errors)
	}
	return d.errorsProp
}

func (d *StaticCheckData) GetGoMethod(name string) (*GoFunction, bool) {
	return nil, false
}

func (d *StaticCheckData) Prop(ctx *Context, name string) Value {
	switch name {
	case "errors":
		return d.ErrorTuple()
	}

	method, ok := d.GetGoMethod(name)
	if !ok {
		panic(FormatErrPropertyDoesNotExist(name, d))
	}
	return method
}

func (*StaticCheckData) SetProp(ctx *Context, name string, value Value) error {
	return ErrCannotSetProp
}

func (*StaticCheckData) PropertyNames(ctx *Context) []string {
	return STATIC_CHECK_DATA_PROP_NAMES
}

type FunctionStaticData struct {
	capturedGlobals []string
	assignGlobal    bool
}

type MappingStaticData struct {
	referencedGlobals []string
}

func (data *StaticCheckData) addFnCapturedGlobal(fnExpr *parse.FunctionExpression, name string, optionalInfo *globalVarInfo) {
	fnData := data.fnData[fnExpr]
	if fnData == nil {
		fnData = &FunctionStaticData{}
		data.fnData[fnExpr] = fnData
	}

	if !utils.SliceContains(fnData.capturedGlobals, name) {
		fnData.capturedGlobals = append(fnData.capturedGlobals, name)
	}

	if optionalInfo != nil && optionalInfo.fnExpr != nil {
		capturedGlobalFnData := data.GetFnData(optionalInfo.fnExpr)
		if capturedGlobalFnData != nil {
			for _, name := range capturedGlobalFnData.capturedGlobals {
				if utils.SliceContains(fnData.capturedGlobals, name) {
					continue
				}

				fnData.capturedGlobals = append(fnData.capturedGlobals, name)
			}
		}
	}
}

func (data *StaticCheckData) addMappingCapturedGlobal(expr *parse.MappingExpression, name string) {
	mappingData := data.mappingData[expr]
	if mappingData == nil {
		mappingData = &MappingStaticData{}
		data.mappingData[expr] = mappingData
	}

	if !utils.SliceContains(mappingData.referencedGlobals, name) {
		mappingData.referencedGlobals = append(mappingData.referencedGlobals, name)
	}
}

func (data *StaticCheckData) addFnAssigningGlobal(fnExpr *parse.FunctionExpression) {
	fnData := data.fnData[fnExpr]
	if fnData == nil {
		fnData = &FunctionStaticData{}
		data.fnData[fnExpr] = fnData
	}

	fnData.assignGlobal = true
}

func (data *StaticCheckData) GetFnData(fnExpr *parse.FunctionExpression) *FunctionStaticData {
	return data.fnData[fnExpr]
}

func (data *StaticCheckData) GetMappingData(expr *parse.MappingExpression) *MappingStaticData {
	return data.mappingData[expr]
}
