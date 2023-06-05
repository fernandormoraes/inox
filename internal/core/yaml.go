package core

import (
	"errors"
	"fmt"
	"math"

	yaml "github.com/goccy/go-yaml/ast"
)

var ErrUnsupportedYamlNodeType = errors.New("unsupported YAML node type")

func ConvertYamlParsedFileToInoxVal(ctx *Context, f *yaml.File, immutable bool) Value {
	values := make([]Value, len(f.Docs))
	for i, doc := range f.Docs {
		values[i] = ConvertYamlNodeToInoxVal(ctx, doc, immutable)
	}

	if immutable {
		return NewTuple(values)
	}
	return NewWrappedValueListFrom(values)
}

func ConvertYamlNodeToInoxVal(ctx *Context, n yaml.Node, immutable bool) Value {
	switch n.Type() {
	case yaml.UnknownNodeType:
		panic(errors.New("unknown YAML node type"))
	case yaml.DocumentType:
		return ConvertYamlNodeToInoxVal(ctx, n.(*yaml.DocumentNode).Body, immutable)
	case yaml.NullType:
		return Nil
	case yaml.BoolType:
		return Bool(n.(*yaml.BoolNode).Value)
	case yaml.IntegerType:
		switch integer := n.(*yaml.IntegerNode).Value.(type) {
		case uint64:
			if integer > math.MaxInt64 {
				panic(errors.New("integer value is a large uint64, it is not supported"))
			}
			return Int(integer)
		case int64:
			return Int(integer)
		default:
			panic(ErrUnreachable)
		}
	case yaml.FloatType:
		return Float(n.(*yaml.FloatNode).Value)
	case yaml.InfinityType:
		return Float(n.(*yaml.InfinityNode).Value)
	case yaml.NanType:
		return Float(math.NaN())
	case yaml.StringType:
		return Str(n.(*yaml.StringNode).Value)
	case yaml.LiteralType:
		//TODO: handle start token ?
		return Str(n.(*yaml.LiteralNode).Value.Value)
	case yaml.MappingType:
		items := n.(*yaml.MappingNode).Values
		keys := make([]string, len(items))
		values := make([]Value, len(items))

		for i, item := range items {
			keys[i] = item.Key.String() //TODO: what if the string is a number ?
			values[i] = ConvertYamlNodeToInoxVal(ctx, item.Value, immutable)
		}

		if immutable {
			return NewRecordFromKeyValLists(keys, values)
		}
		return objFromLists(keys, values)
	case yaml.SequenceType:
		items := n.(*yaml.SequenceNode).Values
		values := make([]Value, len(items))

		for i, item := range items {
			values[i] = ConvertYamlNodeToInoxVal(ctx, item, immutable)
		}

		if immutable {
			return NewTuple(values)
		}
		return NewWrappedValueListFrom(values)
	case yaml.MergeKeyType:
	case yaml.MappingKeyType:
	case yaml.MappingValueType:
		node := n.(*yaml.MappingValueNode)

		val := ConvertYamlNodeToInoxVal(ctx, node.Value, immutable)
		keys := []string{node.Key.String()}
		values := []Value{val}

		if immutable {
			return NewRecordFromKeyValLists(keys, values)
		}
		return objFromLists(keys, values)
	case yaml.AnchorType:
	case yaml.AliasType:
	case yaml.DirectiveType:
	case yaml.TagType:
	case yaml.CommentType:
	case yaml.CommentGroupType:
	}
	panic(fmt.Errorf("%w: %T at YAML path %s", ErrUnsupportedYamlNodeType, n, n.GetPath()))

}
