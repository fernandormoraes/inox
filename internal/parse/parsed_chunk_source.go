package parse

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// ParsedChunkSource contains an AST and the ChunkSource that was parsed to obtain it.
// ParsedChunkSource provides helper methods to find nodes in the AST and to get positions.
type ParsedChunkSource struct {
	Node      *Chunk
	Source    ChunkSource
	runes     []rune
	runesLock sync.Mutex
}

func ParseChunkSource(src ChunkSource) (*ParsedChunkSource, error) {
	runes, chunk, err := ParseChunk2(src.Code(), src.Name())

	if chunk == nil {
		return nil, err
	}

	return &ParsedChunkSource{
		Node:   chunk,
		Source: src,
		runes:  runes,
	}, err
}

func NewParsedChunkSource(node *Chunk, src ChunkSource) *ParsedChunkSource {
	return &ParsedChunkSource{
		Node:   node,
		Source: src,
	}
}

func (c *ParsedChunkSource) Name() string {
	return c.Source.Name()
}

// result should not be modified.
func (c *ParsedChunkSource) Runes() []rune {
	c.runesLock.Lock()
	defer c.runesLock.Unlock()

	runes := c.runes
	if c.Source.Code() != "" && len(runes) == 0 {
		c.runes = []rune(c.Source.Code())
	}
	return c.runes
}

func (chunk *ParsedChunkSource) GetLineColumn(node Node) (int32, int32) {
	return chunk.GetSpanLineColumn(node.Base().Span)
}

func (chunk *ParsedChunkSource) FormatNodeSpanLocation(w io.Writer, nodeSpan NodeSpan) (int, error) {
	line, col := chunk.GetSpanLineColumn(nodeSpan)
	return fmt.Fprintf(w, "%s:%d:%d:", chunk.Name(), line, col)
}

func (chunk *ParsedChunkSource) GetFormattedNodeLocation(node Node) string {
	buf := bytes.NewBuffer(nil)
	chunk.FormatNodeSpanLocation(buf, node.Base().Span)
	return buf.String()
}

func (chunk *ParsedChunkSource) GetSpanLineColumn(span NodeSpan) (int32, int32) {
	line := int32(1)
	col := int32(1)
	i := 0

	runes := chunk.Runes()

	for i < int(span.Start) && i < len(runes) {
		if runes[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}

		i++
	}

	return line, col
}

func (chunk *ParsedChunkSource) GetIncludedEndSpanLineColumn(span NodeSpan) (int32, int32) {
	line := int32(1)
	col := int32(1)
	i := 0

	runes := chunk.Runes()

	for i < int(span.End-1) && i < len(runes) {
		if runes[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}

		i++
	}

	return line, col
}

func (chunk *ParsedChunkSource) GetEndSpanLineColumn(span NodeSpan) (int32, int32) {
	line := int32(1)
	col := int32(1)
	i := 0

	runes := chunk.Runes()

	for i < int(span.End) && i < len(runes) {
		if runes[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}

		i++
	}

	return line, col
}

func (chunk *ParsedChunkSource) GetLineColumnSingeCharSpan(line, column int32) NodeSpan {
	pos := chunk.GetLineColumnPosition(line, column)
	return NodeSpan{
		Start: pos,
		End:   pos + 1,
	}
}

func (chunk *ParsedChunkSource) GetLineColumnPosition(line, column int32) int32 {
	i := int32(0)
	runes := chunk.Runes()
	length := len32(runes)

	line -= 1

	for i < length && line > 0 {
		if runes[i] == '\n' {
			line--
		}
		i++
	}

	pos := i + column - 1
	return pos
}

func (chunk *ParsedChunkSource) GetSourcePosition(span NodeSpan) SourcePositionRange {
	line, col := chunk.GetSpanLineColumn(span)
	endLine, endCol := chunk.GetEndSpanLineColumn(span)

	return SourcePositionRange{
		SourceName:  chunk.Name(),
		StartLine:   line,
		StartColumn: col,
		EndLine:     endLine,
		EndColumn:   endCol,
		Span:        span,
	}
}

// GetNodeAndChainAtSpan searches for the deepest node that includes the provided span.
// Spans of length 0 are supported, nodes whose exclusive range end is equal to the start of the provided span
// are ignored.
func (chunk *ParsedChunkSource) GetNodeAndChainAtSpan(target NodeSpan) (foundNode Node, ancestors []Node, ok bool) {

	Walk(chunk.Node, func(node, _, _ Node, chain []Node, _ bool) (TraversalAction, error) {
		span := node.Base().Span

		//if the cursor is not in the node's span we don't check the descendants of the node
		if span.Start >= target.End || span.End <= target.Start {
			return Prune, nil
		}

		if foundNode == nil || node.Base().IncludedIn(foundNode) {
			foundNode = node
			ancestors = chain
			ok = true
		}

		return ContinueTraversal, nil
	}, nil)

	return
}

// GetNodeAtSpan calls .GetNodeAndChainAtSpan and returns the found node.
func (chunk *ParsedChunkSource) GetNodeAtSpan(target NodeSpan) (foundNode Node, ok bool) {
	node, _, ok := chunk.GetNodeAndChainAtSpan(target)
	return node, ok
}

func (chunk *ParsedChunkSource) FindFirstStatementAndChainOnLine(line int) (foundNode Node, ancestors []Node, ok bool) {
	i := int32(0)
	runes := chunk.Runes()
	length := len32(runes)

	line -= 1

	for i < length && line > 0 {
		if runes[i] == '\n' {
			line--
		}
		i++
	}

	//eat leading space
	for i < length && isSpaceNotLF(runes[i]) {
		i++
	}

	if i < length && runes[i] == '\n' { //empty line
		return nil, nil, false
	}

	pos := i

	span := NodeSpan{
		Start: pos,
		End:   pos + 1,
	}
	node, ancestors, found := chunk.GetNodeAndChainAtSpan(span)
	if len(ancestors) == 0 || IsScopeContainerNode(node) {
		return nil, nil, false
	}

	if found {
		//search for closest statement

		for i := len(ancestors) - 1; i >= 0; i-- {
			ancestor := ancestors[i]
			switch ancestor.(type) {
			case *Block, *Chunk, *EmbeddedModule:

				var (
					stmt          Node
					stmtAncestors []Node
				)

				if i == len(ancestors)-1 {
					stmt = node
					stmtAncestors = ancestors
				} else {
					stmt = ancestors[i+1]
					stmtAncestors = ancestors[:i+1]
				}

				//if the statement does not start on the line we return false
				if stmt.Base().Span.Start != pos {
					return nil, nil, false
				}

				return stmt, stmtAncestors, true
			}
		}

		return nil, nil, false
	}

	return nil, nil, false
}

func (c *ParsedChunkSource) EstimatedIndentationUnit() string {
	return EstimateIndentationUnit(c.Runes(), c.Node)
}
