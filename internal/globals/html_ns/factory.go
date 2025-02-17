package html_ns

import (
	"fmt"
	"strconv"

	"github.com/inoxlang/inox/internal/core"
	"golang.org/x/net/html"
)

func CreateHTMLNodeFromXMLElement(ctx *core.Context, arg *core.XMLElement) *HTMLNode {
	children := arg.Children()
	childNodes := make([]*HTMLNode, 0, len(children))

	rawContent := arg.RawContent() //content inside <script>, <style> tags.
	if rawContent != "" {
		childNodes = append(childNodes, CreateTextNode(core.String(rawContent)))
	}

	for _, child := range children {
		createChildNodesFromValue(ctx, child, &childNodes)
	}

	attributes := make([]html.Attribute, 0, len(arg.Attributes()))

	for _, attr := range arg.Attributes() {
		attrName := attr.Name()

		//handle pseudo htmx attributes
		if isPseudoHtmxAttribute(attrName) {
			transpilePseudoHtmxAttribute(attr, &attributes)
			//TODO: handle errors
			continue
		}

		attributes = append(attributes, html.Attribute{Key: attrName})
		index := len(attributes) - 1

		switch val := attr.Value().(type) {
		case core.StringLike:
			attributes[index].Val = val.GetOrBuildString()
		case core.Int:
			attributes[index].Val = strconv.FormatInt(int64(val), 10)
		default:
			panic(fmt.Errorf("failed to convert value of attribute '%s' to string", attr.Name()))
		}
	}

	node := NewNodeFromGoDescription(NodeDescription{
		Tag:        arg.Name(),
		Children:   childNodes,
		Attributes: attributes,
	})

	if arg.Name() == "html" {
		return NewHTML5DocumentNodeFromGoDescription(HTML5DocumentDescription{
			HtmlTagNode: node,
		})
	}
	return node
}

func createChildNodesFromValue(ctx *core.Context, child core.Value, childNodes *[]*HTMLNode) {
	switch c := child.(type) {
	case *core.XMLElement:
		*childNodes = append(*childNodes, CreateHTMLNodeFromXMLElement(ctx, c))
	case *HTMLNode:
		if c.HasParent() {
			panic(core.ErrUnreachable)
		}
		*childNodes = append(*childNodes, c)
	case core.StringLike:
		*childNodes = append(*childNodes, CreateTextNode(c))
	case core.Int:
		*childNodes = append(*childNodes, CreateTextNode(core.String(strconv.FormatInt(int64(c), 10))))
	case *core.List:
		length := c.Len()
		for i := 0; i < length; i++ {
			elem := c.At(ctx, i)
			createChildNodesFromValue(ctx, elem, childNodes)
		}
	default:
		panic(core.ErrUnreachable)
	}
}
